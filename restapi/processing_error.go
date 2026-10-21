package restapi

import "fmt"

// processingError represents an error that occurred during processing,
// with an indication of whether the error is retryable.
type processingError struct {
	isRetryable bool
	reason      string
	err         error
}

// newRetryableProcessingError creates a new retryable processing error.
func newRetryableProcessingError(format string, a ...any) *processingError {
	return &processingError{
		isRetryable: true,
		err:         fmt.Errorf(format, a...),
	}
}

// newNonRetryableProcessingError creates a new non-retryable processing error.
func newNonRetryableProcessingError(format string, a ...any) *processingError {
	return &processingError{
		isRetryable: false,
		err:         fmt.Errorf(format, a...),
	}
}

// Error implements the error interface.
func (e *processingError) Error() string {
	return e.err.Error()
}

// IsRetryable indicates whether the error is retryable.
func (e *processingError) IsRetryable() bool {
	return e.isRetryable
}

// Unwrap returns the underlying error.
func (e *processingError) Unwrap() error {
	return e.err
}

// withReason sets the alert rejection reason for a non-retryable error and returns the error.
func (e *processingError) withReason(reason string) *processingError {
	e.reason = reason
	return e
}
