package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// CanonicalEnqueueRequest represents the normalized fields used to compute idempotency request_hash.
type CanonicalEnqueueRequest struct {
	TaskType       string          `json:"task_type"`
	Queue          string          `json:"queue"`
	Payload        json.RawMessage `json:"payload"`
	Priority       int16           `json:"priority"`
	RunAtSentinel  string          `json:"run_at"`
	MaxAttempts    int             `json:"max_attempts"`
	TimeoutSeconds int             `json:"timeout_seconds"`
}

// CanonicalizeJSON sorts map keys recursively so JSON serialization is deterministic.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("{}"), nil
	}
	var val any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&val); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}

	cleaned := sortJSONValues(val)
	return json.Marshal(cleaned)
}

func sortJSONValues(val any) any {
	switch v := val.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]any, len(v))
		for _, k := range keys {
			ordered[k] = sortJSONValues(v[k])
		}
		return ordered
	case []any:
		list := make([]any, len(v))
		for i, item := range v {
			list[i] = sortJSONValues(item)
		}
		return list
	default:
		return v
	}
}

// ComputeRequestHash computes the deterministic SHA-256 hash of a validated enqueue request.
// If isExplicitRunAt is false, "immediate" is hashed as specified in TDD.
func ComputeRequestHash(taskType, queue string, rawPayload []byte, priority int16, runAt time.Time, isExplicitRunAt bool, maxAttempts, timeoutSeconds int) ([]byte, error) {
	canonPayload, err := CanonicalizeJSON(rawPayload)
	if err != nil {
		return nil, err
	}

	runAtSentinel := "immediate"
	if isExplicitRunAt {
		runAtSentinel = runAt.UTC().Format(time.RFC3339Nano)
	}

	req := CanonicalEnqueueRequest{
		TaskType:       taskType,
		Queue:          queue,
		Payload:        canonPayload,
		Priority:       priority,
		RunAtSentinel:  runAtSentinel,
		MaxAttempts:    maxAttempts,
		TimeoutSeconds: timeoutSeconds,
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	h := sha256.Sum256(encoded)
	return h[:], nil
}
