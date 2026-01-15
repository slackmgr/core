package internal

import (
	"errors"
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestRetryableError(t *testing.T) {
	t.Parallel()

	err := &slackapi.RateLimitedError{}
	assert.True(t, isTransientError(err))

	error503 := &testError{Code: 503}
	assert.True(t, isTransientError(error503))

	error499 := &testError{Code: 499}
	assert.False(t, isTransientError(error499))

	assert.True(t, isTransientError(errors.New("service_unavailable")))
	assert.False(t, isTransientError(errors.New("foo")))
}

type testError struct {
	Code   int
	Status string
}

func (t testError) Error() string {
	return ""
}

func (t testError) Retryable() bool {
	return t.Code >= 500
}
