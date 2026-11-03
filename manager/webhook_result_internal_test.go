package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookRejectedError(t *testing.T) {
	t.Parallel()

	t.Run("http", func(t *testing.T) {
		t.Parallel()

		err := &WebhookRejectedError{StatusCode: http.StatusBadRequest, Message: "bad"}

		assert.Equal(t, "webhook target rejected the request with HTTP status code 400", err.Error())

		httpErr, ok := errors.AsType[*WebhookHTTPError](fmt.Errorf("wrapped: %w", err))
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, httpErr.StatusCode)
	})

	t.Run("non-http", func(t *testing.T) {
		t.Parallel()

		err := &WebhookRejectedError{Message: "bad"}

		assert.Equal(t, "webhook target rejected the request", err.Error())
		require.NoError(t, err.Unwrap())

		_, ok := errors.AsType[*WebhookHTTPError](err)
		assert.False(t, ok)
	})

	t.Run("failure reason and target error", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("wrapped: %w", &WebhookRejectedError{StatusCode: http.StatusBadRequest})

		reason, retryable := webhookFailureReason(err)
		assert.Equal(t, "the request was rejected by the receiving system", reason)
		assert.False(t, retryable)
		assert.True(t, isWebhookTargetError(err))
		assert.True(t, isWebhookTargetError(&WebhookRejectedError{}))
	})
}

func TestHTTPWebhookHandler_HandleWebhookWithResult(t *testing.T) {
	t.Parallel()

	const validResult = `{"slackManagerResult": {"message": "  Restarting 3 pods  ", "afterClick": {"statusText": "In progress"}}}`

	tests := []struct {
		name          string
		statusCode    int
		contentType   string
		body          string
		wantMessage   string
		wantResult    bool
		wantRejected  bool
		wantHTTPError bool
	}{
		{name: "2xx with result", statusCode: http.StatusOK, contentType: "application/json", body: validResult, wantResult: true, wantMessage: "Restarting 3 pods"},
		{name: "2xx with result and charset", statusCode: http.StatusOK, contentType: "application/json; charset=utf-8", body: validResult, wantResult: true, wantMessage: "Restarting 3 pods"},
		{name: "2xx with null result", statusCode: http.StatusOK, contentType: "application/json", body: `{"slackManagerResult": null}`},
		{name: "2xx with other json", statusCode: http.StatusOK, contentType: "application/json", body: `{"status": "ok"}`},
		{name: "2xx with json array", statusCode: http.StatusOK, contentType: "application/json", body: `[1, 2]`},
		{name: "2xx with invalid json", statusCode: http.StatusOK, contentType: "application/json", body: `{`},
		{name: "2xx with wrong content type", statusCode: http.StatusOK, contentType: "text/plain", body: validResult},
		{name: "2xx without content type", statusCode: http.StatusOK, body: validResult},
		{name: "2xx without body", statusCode: http.StatusNoContent},
		{
			name:        "2xx with invalid result",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"slackManagerResult": {"message": "` + strings.Repeat("a", types.MaxWebhookResultMessageLength+1) + `"}}`,
		},
		{
			name:        "2xx with too large body",
			statusCode:  http.StatusOK,
			contentType: "application/json",
			body:        `{"slackManagerResult": {"message": "ok"}, "padding": "` + strings.Repeat("a", maxWebhookResponseBodySize) + `"}`,
		},
		{
			name:         "4xx with result is rejected",
			statusCode:   http.StatusUnprocessableEntity,
			contentType:  "application/json",
			body:         `{"slackManagerResult": {"message": "Unknown version"}}`,
			wantRejected: true,
			wantMessage:  "Unknown version",
		},
		{
			name:         "4xx with empty result is rejected",
			statusCode:   http.StatusBadRequest,
			contentType:  "application/json",
			body:         `{"slackManagerResult": {}}`,
			wantRejected: true,
		},
		{name: "4xx with other json", statusCode: http.StatusBadRequest, contentType: "application/json", body: `{"error": "bad"}`, wantHTTPError: true},
		{name: "4xx without body", statusCode: http.StatusNotFound, wantHTTPError: true},
		{
			name:          "4xx with invalid result",
			statusCode:    http.StatusBadRequest,
			contentType:   "application/json",
			body:          `{"slackManagerResult": {"afterClick": {"hideButtons": "some"}}}`,
			wantHTTPError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				}

				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			handler := NewHTTPWebhookHandler(&mockLogger{})

			result, err := handler.HandleWebhookWithResult(t.Context(), server.URL, &types.WebhookCallback{ID: "hook1"}, &mockLogger{})

			switch {
			case tt.wantRejected:
				assert.Nil(t, result)

				rejected, ok := errors.AsType[*WebhookRejectedError](err)
				require.True(t, ok, "expected a WebhookRejectedError, got %v", err)
				assert.Equal(t, tt.statusCode, rejected.StatusCode)
				assert.Equal(t, tt.wantMessage, rejected.Message)
			case tt.wantHTTPError:
				assert.Nil(t, result)

				_, isRejected := errors.AsType[*WebhookRejectedError](err)
				assert.False(t, isRejected)

				httpErr, ok := errors.AsType[*WebhookHTTPError](err)
				require.True(t, ok, "expected a WebhookHTTPError, got %v", err)
				assert.Equal(t, tt.statusCode, httpErr.StatusCode)
			case tt.wantResult:
				require.NoError(t, err)
				require.NotNil(t, result)
				assert.Equal(t, tt.wantMessage, result.Message)
				require.NotNil(t, result.AfterClick)
				assert.Equal(t, types.WebhookHideButtonsNone, result.AfterClick.HideButtons, "result must be cleaned")
			default:
				require.NoError(t, err)
				assert.Nil(t, result)
			}
		})
	}
}

func TestHTTPWebhookHandler_TooManyRequestsIsNotRejected(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"slackManagerResult": {"message": "slow down"}}`))
	}))
	t.Cleanup(server.Close)

	handler := NewHTTPWebhookHandler(&mockLogger{})
	handler.client.SetRetryWaitTime(time.Millisecond).SetRetryMaxWaitTime(time.Millisecond)

	_, err := handler.HandleWebhookWithResult(t.Context(), server.URL, &types.WebhookCallback{}, &mockLogger{})

	_, isRejected := errors.AsType[*WebhookRejectedError](err)
	assert.False(t, isRejected)

	httpErr, ok := errors.AsType[*WebhookHTTPError](err)
	require.True(t, ok)
	assert.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
}

func TestHTTPWebhookHandler_RetryUsesFinalResponse(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"slackManagerResult": {"message": "first"}}`))

			return
		}

		_, _ = w.Write([]byte(`{"slackManagerResult": {"message": "second"}}`))
	}))
	t.Cleanup(server.Close)

	handler := NewHTTPWebhookHandler(&mockLogger{})
	handler.client.SetRetryWaitTime(time.Millisecond).SetRetryMaxWaitTime(time.Millisecond)

	result, err := handler.HandleWebhookWithResult(t.Context(), server.URL, &types.WebhookCallback{}, &mockLogger{})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "second", result.Message)
	assert.Equal(t, int32(2), calls.Load())
}

func TestHTTPWebhookHandler_HandleWebhookIgnoresResult(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slackManagerResult": {"message": "ok"}}`))
	}))
	t.Cleanup(server.Close)

	handler := NewHTTPWebhookHandler(&mockLogger{})

	require.NoError(t, handler.HandleWebhook(t.Context(), server.URL, &types.WebhookCallback{}, &mockLogger{}))
}

func TestHTTPWebhookHandler_ConnectionError(t *testing.T) {
	t.Parallel()

	handler := NewHTTPWebhookHandler(&mockLogger{})
	handler.client.SetRetryCount(0)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	result, err := handler.HandleWebhookWithResult(ctx, "http://127.0.0.1:1", &types.WebhookCallback{}, &mockLogger{})

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "webhook POST http://127.0.0.1:1 failed")
}

// resultWebhookHandler is a [WebhookResultHandler] returning a fixed result and error.
type resultWebhookHandler struct {
	funcWebhookHandler

	result *types.WebhookResult
	err    error
}

func (h *resultWebhookHandler) HandleWebhookWithResult(_ context.Context, _ string, _ *types.WebhookCallback, _ types.Logger) (*types.WebhookResult, error) {
	return h.result, h.err
}

func TestCallWebhookHandler(t *testing.T) {
	t.Parallel()

	t.Run("plain handler", func(t *testing.T) {
		t.Parallel()

		wantErr := errors.New("boom")
		handler := &funcWebhookHandler{shouldHandle: true, handle: func() error { return wantErr }}

		result, err := callWebhookHandler(t.Context(), handler, "x", &types.WebhookCallback{}, &mockLogger{})

		require.ErrorIs(t, err, wantErr)
		assert.Nil(t, result)
	})

	t.Run("result is cleaned", func(t *testing.T) {
		t.Parallel()

		handler := &resultWebhookHandler{result: &types.WebhookResult{Message: " hi ", AfterClick: &types.WebhookAfterClick{}}}

		result, err := callWebhookHandler(t.Context(), handler, "x", &types.WebhookCallback{}, &mockLogger{})

		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, "hi", result.Message)
		assert.Equal(t, types.WebhookHideButtonsNone, result.AfterClick.HideButtons)
	})

	t.Run("invalid result is ignored", func(t *testing.T) {
		t.Parallel()

		handler := &resultWebhookHandler{result: &types.WebhookResult{Message: strings.Repeat("a", types.MaxWebhookResultMessageLength+1)}}

		result, err := callWebhookHandler(t.Context(), handler, "x", &types.WebhookCallback{}, &mockLogger{})

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("nil result", func(t *testing.T) {
		t.Parallel()

		result, err := callWebhookHandler(t.Context(), &resultWebhookHandler{}, "x", &types.WebhookCallback{}, &mockLogger{})

		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()

		wantErr := &WebhookRejectedError{Message: "no"}
		handler := &resultWebhookHandler{result: &types.WebhookResult{Message: "ignored"}, err: wantErr}

		result, err := callWebhookHandler(t.Context(), handler, "x", &types.WebhookCallback{}, &mockLogger{})

		require.ErrorIs(t, err, wantErr)
		assert.Nil(t, result)
	})
}

func TestPostWebhook_ResultOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		handler     WebhookHandler
		wantStatus  models.WebhookOutcomeStatus
		wantMessage string
		wantResult  models.WebhookOutcomeStatus
	}{
		{
			name:        "delivered with message",
			handler:     &resultWebhookHandler{result: &types.WebhookResult{Message: "Restarting"}},
			wantStatus:  models.WebhookOutcomeDelivered,
			wantMessage: "Restarting",
			wantResult:  models.WebhookOutcomeDelivered,
		},
		{
			name:       "delivered without result",
			handler:    &resultWebhookHandler{},
			wantStatus: models.WebhookOutcomeDelivered,
			wantResult: models.WebhookOutcomeDelivered,
		},
		{
			name:        "rejected",
			handler:     &resultWebhookHandler{err: fmt.Errorf("wrapped: %w", &WebhookRejectedError{StatusCode: 400, Message: " Unknown version "})},
			wantStatus:  models.WebhookOutcomeRejected,
			wantMessage: "Unknown version",
			wantResult:  models.WebhookOutcomeFailed,
		},
		{
			name:       "rejected with too long message",
			handler:    &resultWebhookHandler{err: &WebhookRejectedError{Message: strings.Repeat("a", types.MaxWebhookResultMessageLength+1)}},
			wantStatus: models.WebhookOutcomeRejected,
			wantResult: models.WebhookOutcomeFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}

			var resultStatus models.WebhookOutcomeStatus

			sendResult := func(_ context.Context, status models.WebhookOutcomeStatus) { resultStatus = status }

			postWebhook(t.Context(), tt.handler, "https://example.com", &types.WebhookCallback{}, &mockLogger{}, nil, nil, slackClient, &models.WebhookOutcome{}, sendResult)

			outcome := <-slackClient.webhookOutcomes
			assert.Equal(t, tt.wantStatus, outcome.Status)
			assert.Equal(t, tt.wantMessage, outcome.Message)
			assert.Equal(t, tt.wantResult, resultStatus)
		})
	}
}
