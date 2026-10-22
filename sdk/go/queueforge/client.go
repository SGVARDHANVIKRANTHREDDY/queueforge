// Package queueforge is a Go client for the QueueForge HTTP API.
//
// Its main feature is safe retries. Enqueue sends an Idempotency-Key (generated automatically when
// you do not supply one) and reuses the SAME key on every retry, so a retry after a timeout or a
// 5xx cannot create a duplicate job: the server returns the original job with Idempotent-Replay.
//
//	c := queueforge.New("https://queue.example.com", os.Getenv("QUEUEFORGE_KEY"))
//	job, err := c.Enqueue(ctx, queueforge.EnqueueRequest{TaskType: "email.send", Payload: payload})
//	done, err := c.Wait(ctx, job.ID, queueforge.WaitOptions{})
package queueforge

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client talks to one QueueForge deployment. It is safe for concurrent use.
type Client struct {
	baseURL    string
	apiKey     string
	http       *http.Client
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	sleep      func(context.Context, time.Duration) error
}

// Option customises a Client.
type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }
func WithMaxRetries(n int) Option          { return func(c *Client) { c.maxRetries = n } }
func WithBackoff(base, max time.Duration) Option {
	return func(c *Client) { c.baseDelay, c.maxDelay = base, max }
}

// New creates a client. Requests time out after 30 seconds unless you supply your own http.Client.
func New(baseURL, apiKey string, opts ...Option) *Client {
	c := &Client{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second},
		maxRetries: 4, baseDelay: 200 * time.Millisecond, maxDelay: 10 * time.Second, sleep: sleepCtx}
	for _, o := range opts {
		o(c)
	}
	return c
}

// EnqueueRequest describes a job to submit.
type EnqueueRequest struct {
	TaskType       string          `json:"task_type"`
	Queue          string          `json:"queue,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Priority       *int            `json:"priority,omitempty"`
	RunAt          *time.Time      `json:"run_at,omitempty"`
	MaxAttempts    *int            `json:"max_attempts,omitempty"`
	TimeoutSeconds *int            `json:"timeout_seconds,omitempty"`

	// IdempotencyKey makes submission safe to repeat. Empty = generated for you (and reused across
	// this call's retries). Supply your own to make *separate* calls idempotent too.
	IdempotencyKey string `json:"-"`
	// TraceParent is an optional W3C traceparent header to join an existing trace.
	TraceParent string `json:"-"`
}

// Job is the server's view of a job.
type Job struct {
	ID              string          `json:"id"`
	Status          string          `json:"status"`
	TaskType        string          `json:"task_type"`
	Queue           string          `json:"queue"`
	Result          json.RawMessage `json:"result"`
	AttemptsStarted int             `json:"attempts_started"`
	MaxAttempts     int             `json:"max_attempts"`
	LastErrorCode   *string         `json:"last_error_code"`
	LastErrorMsg    *string         `json:"last_error_message"`
	TraceID         string          `json:"trace_id"`
	CreatedAt       time.Time       `json:"created_at"`
	// IdempotentReplay is true when the server recognised the idempotency key and returned the
	// original job instead of creating a new one.
	IdempotentReplay bool `json:"-"`
}

// Terminal reports whether the job will never change state again (without a replay).
func (j *Job) Terminal() bool {
	switch j.Status {
	case "succeeded", "dead", "cancelled":
		return true
	}
	return false
}

// APIError is returned for any non-2xx response.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RequestID  string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("queueforge: HTTP %d %s: %s (request %s)", e.Status, e.Code, e.Message, e.RequestID)
}

// Retryable reports whether repeating the request could succeed.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// IsNotFound / IsConflict are convenience checks for errors.As targets.
func IsNotFound(err error) bool { var e *APIError; return errors.As(err, &e) && e.Status == 404 }
func IsConflict(err error) bool { var e *APIError; return errors.As(err, &e) && e.Status == 409 }

// Enqueue submits a job. It retries 429, 5xx and network errors with exponential backoff and full
// jitter (honouring Retry-After), always with the same idempotency key.
func (c *Client) Enqueue(ctx context.Context, req EnqueueRequest) (*Job, error) {
	if req.TaskType == "" {
		return nil, errors.New("queueforge: TaskType is required")
	}
	key := req.IdempotencyKey
	if key == "" {
		key = "sdk-" + randomHex(16)
	}
	hdr := map[string]string{"Idempotency-Key": key}
	if req.TraceParent != "" {
		hdr["traceparent"] = req.TraceParent
	}
	var out Job
	replay, err := c.do(ctx, http.MethodPost, "/v1/jobs", req, hdr, &out, true)
	if err != nil {
		return nil, err
	}
	out.IdempotentReplay = replay
	if out.TaskType == "" {
		out.TaskType = req.TaskType
	}
	return &out, nil
}

// EnqueueBatch submits up to 100 jobs atomically: either every job is accepted or none is. Retries
// (429, 5xx, network) reuse one idempotency key, so a retried batch never creates duplicates. Pass
// your own idempotencyKey to make separate calls idempotent too; empty means one is generated.
func (c *Client) EnqueueBatch(ctx context.Context, reqs []EnqueueRequest, idempotencyKey string) ([]*Job, error) {
	if len(reqs) == 0 || len(reqs) > 100 {
		return nil, errors.New("queueforge: a batch must contain between 1 and 100 jobs")
	}
	for i := range reqs {
		if reqs[i].TaskType == "" {
			return nil, fmt.Errorf("queueforge: reqs[%d].TaskType is required", i)
		}
	}
	if idempotencyKey == "" {
		idempotencyKey = "sdk-batch-" + randomHex(16)
	}
	body := struct {
		Jobs []EnqueueRequest `json:"jobs"`
	}{Jobs: reqs}
	var out struct {
		Jobs []Job `json:"jobs"`
	}
	replay, err := c.do(ctx, http.MethodPost, "/v1/jobs/batch", body, map[string]string{"Idempotency-Key": idempotencyKey}, &out, true)
	if err != nil {
		return nil, err
	}
	jobs := make([]*Job, len(out.Jobs))
	for i := range out.Jobs {
		out.Jobs[i].IdempotentReplay = replay
		if i < len(reqs) && out.Jobs[i].TaskType == "" {
			out.Jobs[i].TaskType = reqs[i].TaskType
		}
		jobs[i] = &out.Jobs[i]
	}
	return jobs, nil
}

// Get fetches a job.
func (c *Client) Get(ctx context.Context, id string) (*Job, error) {
	var j Job
	if _, err := c.do(ctx, http.MethodGet, "/v1/jobs/"+url.PathEscape(id), nil, nil, &j, true); err != nil {
		return nil, err
	}
	return &j, nil
}

// Cancel cancels a queued job (or requests cancellation of a running one).
func (c *Client) Cancel(ctx context.Context, id string) error {
	_, err := c.do(ctx, http.MethodPost, "/v1/jobs/"+url.PathEscape(id)+"/cancel", nil, nil, nil, true)
	return err
}

// WaitOptions controls Wait.
type WaitOptions struct {
	PollInterval time.Duration // default 250ms, growing to 2s
}

// Wait polls until the job reaches a terminal state or ctx ends.
func (c *Client) Wait(ctx context.Context, id string, o WaitOptions) (*Job, error) {
	delay := o.PollInterval
	if delay <= 0 {
		delay = 250 * time.Millisecond
	}
	for {
		j, err := c.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if j.Terminal() {
			return j, nil
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, err
		}
		if delay < 2*time.Second {
			delay = delay * 3 / 2
		}
	}
}

// do performs one logical request with retries. It returns whether the server flagged an
// idempotent replay. Only requests that are safe to repeat set retry=true.
func (c *Client) do(ctx context.Context, method, path string, body any, hdr map[string]string, out any, retry bool) (bool, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return false, err
		}
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return false, err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "queueforge-go/0.1")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := c.http.Do(req)
		var wait time.Duration
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			lastErr = err // network error: the request may or may not have reached the server
		} else {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			resp.Body.Close()
			if resp.StatusCode/100 == 2 {
				if out != nil && len(raw) > 0 {
					if err := json.Unmarshal(raw, out); err != nil {
						return false, fmt.Errorf("queueforge: decoding response: %w", err)
					}
				}
				return resp.Header.Get("Idempotent-Replay") == "true", nil
			}
			apiErr := &APIError{Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID")}
			var env struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					RequestID string `json:"request_id"`
				} `json:"error"`
			}
			if json.Unmarshal(raw, &env) == nil {
				apiErr.Code, apiErr.Message = env.Error.Code, env.Error.Message
				if apiErr.RequestID == "" {
					apiErr.RequestID = env.Error.RequestID
				}
			}
			if s := resp.Header.Get("Retry-After"); s != "" {
				if n, err := strconv.Atoi(s); err == nil {
					apiErr.RetryAfter = time.Duration(n) * time.Second
				}
			}
			if !apiErr.Retryable() {
				return false, apiErr
			}
			lastErr, wait = apiErr, apiErr.RetryAfter
		}
		if !retry || attempt >= c.maxRetries {
			return false, lastErr
		}
		if wait <= 0 {
			// exponential backoff with full jitter
			ceil := float64(c.baseDelay) * math.Pow(2, float64(attempt))
			if ceil > float64(c.maxDelay) {
				ceil = float64(c.maxDelay)
			}
			wait = time.Duration(mrand.Float64() * ceil)
		}
		if err := c.sleep(ctx, wait); err != nil {
			return false, err
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
