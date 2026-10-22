package domain

import (
	"errors"
	"fmt"
)

// PermanentError marks an error as non-retryable by the worker.
type PermanentError struct {
	Err error
}

func (e PermanentError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "permanent failure"
}

func (e PermanentError) Unwrap() error {
	return e.Err
}

func NewPermanentError(err error) error {
	return PermanentError{Err: err}
}

// AppError is a structured application error with machine-readable code
type AppError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
	RequestID  string `json:"request_id,omitempty"`
	Err        error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Err
}

var (
	ErrNotFound            = errors.New("resource not found")
	ErrUnauthorized        = errors.New("authentication required")
	ErrForbidden           = errors.New("permission denied")
	ErrConflict            = errors.New("conflict")
	ErrRateLimited         = errors.New("rate limit exceeded")
	ErrQuotaExceeded       = errors.New("tenant quota exceeded")
	ErrPayloadTooLarge     = errors.New("payload exceeds maximum size")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different request")
	ErrLeaseLost           = errors.New("lease ownership lost or expired")
	ErrInvalidState        = errors.New("invalid state transition")
)

func NewAppError(code, message string, httpStatus int, err error) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		Err:        err,
	}
}
