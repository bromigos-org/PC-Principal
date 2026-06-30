package memory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type ErrorKind string

const (
	ErrorKindDisabled     ErrorKind = "disabled"
	ErrorKindUnauthorized ErrorKind = "unauthorized"
	ErrorKindValidation   ErrorKind = "validation"
	ErrorKindPartialBatch ErrorKind = "partial_batch"
	ErrorKindTimeout      ErrorKind = "timeout"
	ErrorKindServer       ErrorKind = "server"
	ErrorKindUnexpected   ErrorKind = "unexpected"
)

var ErrDisabled = errors.New("memory client disabled")

type Error struct {
	Kind       ErrorKind
	Operation  string
	StatusCode int
	Results    []EventIngestResult
	Err        error
}

func (e *Error) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s: gnosis returned %d (%s)", e.Operation, e.StatusCode, e.Kind)
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Operation, e.Kind, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Operation, e.Kind)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func (e *Error) Is(target error) bool {
	return target == ErrDisabled && e.Kind == ErrorKindDisabled
}

func classifySendError(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &Error{Kind: ErrorKindTimeout, Operation: operation, Err: err}
	}
	return &Error{Kind: ErrorKindUnexpected, Operation: operation, Err: err}
}

func classifyStatus(operation string, statusCode int) error {
	return &Error{Kind: errorKindForStatus(statusCode), Operation: operation, StatusCode: statusCode}
}

func errorKindForStatus(statusCode int) ErrorKind {
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrorKindUnauthorized
	case statusCode == http.StatusUnprocessableEntity || statusCode == http.StatusBadRequest:
		return ErrorKindValidation
	case statusCode >= http.StatusInternalServerError:
		return ErrorKindServer
	default:
		return ErrorKindUnexpected
	}
}

func hasPartialFailure(results []EventIngestResult) bool {
	for _, result := range results {
		if result.Status == EventIngestStatusRejected || result.Status == EventIngestStatusFailed {
			return true
		}
	}
	return false
}
