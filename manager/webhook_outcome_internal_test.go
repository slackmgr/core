package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookFailureReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		err           error
		wantReason    string
		wantRetryable bool
		wantTargetErr bool
	}{
		{
			name:          "http 4xx",
			err:           fmt.Errorf("webhook POST failed: %w", &WebhookHTTPError{StatusCode: http.StatusUnprocessableEntity}),
			wantReason:    "the request was rejected by the receiving system (HTTP 422)",
			wantRetryable: false,
			wantTargetErr: true,
		},
		{
			name:          "http 429",
			err:           fmt.Errorf("webhook POST failed: %w", &WebhookHTTPError{StatusCode: http.StatusTooManyRequests}),
			wantReason:    "the receiving system is busy (HTTP 429)",
			wantRetryable: true,
			wantTargetErr: true,
		},
		{
			name:          "http 5xx",
			err:           fmt.Errorf("webhook POST failed: %w", &WebhookHTTPError{StatusCode: http.StatusBadGateway}),
			wantReason:    "the receiving system returned an error (HTTP 502)",
			wantRetryable: true,
			wantTargetErr: true,
		},
		{
			name:          "context deadline",
			err:           fmt.Errorf("webhook POST failed: %w", context.DeadlineExceeded),
			wantReason:    models.WebhookReasonTimeout,
			wantRetryable: true,
			wantTargetErr: true,
		},
		{
			name:          "net timeout",
			err:           &url.Error{Op: "Post", URL: "https://example.com/secret-token", Err: &net.DNSError{IsTimeout: true}},
			wantReason:    models.WebhookReasonTimeout,
			wantRetryable: true,
			wantTargetErr: true,
		},
		{
			name:          "dns failure",
			err:           &url.Error{Op: "Post", URL: "https://example.com/secret-token", Err: &net.DNSError{IsNotFound: true}},
			wantReason:    models.WebhookReasonUnreachable,
			wantRetryable: true,
			wantTargetErr: true,
		},
		{
			name:          "connection refused",
			err:           &url.Error{Op: "Post", URL: "https://example.com/secret-token", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
			wantReason:    models.WebhookReasonUnreachable,
			wantRetryable: true,
			wantTargetErr: true,
		},
		{
			name:          "other error",
			err:           errors.New("sqs: access denied for https://sqs.example.com/queue"),
			wantReason:    models.WebhookReasonDeliveryFailed,
			wantRetryable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reason, retryable := webhookFailureReason(tt.err)

			assert.Equal(t, tt.wantReason, reason)
			assert.Equal(t, tt.wantRetryable, retryable)
			assert.Equal(t, tt.wantTargetErr, isWebhookTargetError(tt.err))
			assert.NotContains(t, reason, "secret-token")
			assert.NotContains(t, reason, "sqs.example.com")
		})
	}
}

// funcWebhookHandler is a WebhookHandler that returns the result of a function.
type funcWebhookHandler struct {
	shouldHandle bool
	handle       func() error
}

func (h *funcWebhookHandler) ShouldHandleWebhook(_ context.Context, _ string) bool {
	return h.shouldHandle
}

func (h *funcWebhookHandler) HandleWebhook(_ context.Context, _ string, _ *types.WebhookCallback, _ types.Logger) error {
	return h.handle()
}

func TestChannelManager_HandleWebhook_Outcome(t *testing.T) {
	t.Parallel()

	const (
		channelID = "C123456"
		userID    = "U123456"
		viewID    = "V123456"
	)

	tests := []struct {
		name          string
		webhookID     string
		payload       map[string]any
		handler       *funcWebhookHandler
		wantErr       bool
		wantStatus    models.WebhookOutcomeStatus
		wantReason    string
		wantRetryable bool
		wantButton    string
		afterClick    *types.WebhookAfterClick
		wantResult    models.WebhookOutcomeStatus // Empty if no result command is expected
	}{
		{
			name:       "delivered",
			webhookID:  "hook1",
			handler:    &funcWebhookHandler{shouldHandle: true, handle: func() error { return nil }},
			wantStatus: models.WebhookOutcomeDelivered,
			wantButton: "Go",
			afterClick: &types.WebhookAfterClick{HideButtons: types.WebhookHideButtonsClicked},
			wantResult: models.WebhookOutcomeDelivered,
		},
		{
			name:       "delivered without after-click settings",
			webhookID:  "hook1",
			handler:    &funcWebhookHandler{shouldHandle: true, handle: func() error { return nil }},
			wantStatus: models.WebhookOutcomeDelivered,
			wantButton: "Go",
		},
		{
			name:      "handler error",
			webhookID: "hook1",
			handler: &funcWebhookHandler{shouldHandle: true, handle: func() error {
				return fmt.Errorf("webhook POST failed: %w", &WebhookHTTPError{StatusCode: http.StatusBadRequest})
			}},
			wantStatus: models.WebhookOutcomeFailed,
			wantReason: "the request was rejected by the receiving system (HTTP 400)",
			wantButton: "Go",
			afterClick: &types.WebhookAfterClick{HideButtons: types.WebhookHideButtonsClicked},
			wantResult: models.WebhookOutcomeFailed,
		},
		{
			name:       "webhook not found",
			webhookID:  "unknown",
			handler:    &funcWebhookHandler{shouldHandle: true, handle: func() error { return nil }},
			wantErr:    true,
			wantStatus: models.WebhookOutcomeFailed,
			wantReason: models.WebhookReasonWebhookUnavailable,
		},
		{
			name:       "payload decryption failure",
			webhookID:  "hook1",
			payload:    map[string]any{"key": "value"}, // Not encrypted, and no encryption key configured
			handler:    &funcWebhookHandler{shouldHandle: true, handle: func() error { return nil }},
			wantErr:    true,
			wantStatus: models.WebhookOutcomeFailed,
			wantReason: models.WebhookReasonConfigError,
			wantButton: "Go",
		},
		{
			name:       "no handler",
			webhookID:  "hook1",
			handler:    &funcWebhookHandler{shouldHandle: false},
			wantErr:    true,
			wantStatus: models.WebhookOutcomeFailed,
			wantReason: models.WebhookReasonConfigError,
			wantButton: "Go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}

			queue := &recordingCommandSender{sent: make(chan sentCommand, 1)}
			cm := newTestChannelManager(t, channelID, &stubDB{}, slackClient, &NoopChannelLocker{})
			cm.webhookHandlers = []WebhookHandler{tt.handler}
			cm.commandQueue = queue

			alert, _, _ := newTestAlertFromData(t, types.Alert{
				SlackChannelID: channelID,
				Header:         "Test Alert",
				Webhooks:       []*types.Webhook{{ID: "hook1", URL: "https://example.com/hook", ButtonText: "Go", Payload: tt.payload, AfterClick: tt.afterClick}},
			})

			issue := models.NewIssue(alert, &mockLogger{})
			cmd := &models.Command{
				SlackChannelID:    channelID,
				SlackPostID:       "1700000000.000100",
				UserID:            userID,
				UserRealName:      "Test User",
				WebhookParameters: &models.WebhookCommandParams{WebhookID: tt.webhookID, ViewID: viewID},
			}

			saveNeeded, err := cm.handleWebhook(t.Context(), issue, cmd, &mockLogger{})
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// A pending post-click state is registered (and must be saved) only when a result command is expected
			assert.Equal(t, tt.wantResult != "", saveNeeded)
			assert.Equal(t, tt.wantResult != "", issue.PendingPostClick != nil)

			select {
			case outcome := <-slackClient.webhookOutcomes:
				assert.Equal(t, tt.wantStatus, outcome.Status)
				assert.Equal(t, tt.wantReason, outcome.Reason)
				assert.Equal(t, tt.wantButton, outcome.ButtonText)
				assert.Equal(t, channelID, outcome.ChannelID)
				assert.Equal(t, userID, outcome.UserID)
				assert.Equal(t, viewID, outcome.ViewID)
			case <-time.After(time.Second):
				t.Fatal("webhook outcome was not reported")
			}

			if tt.wantResult == "" {
				select {
				case <-queue.sent:
					t.Fatal("no webhook result command expected")
				case <-time.After(50 * time.Millisecond):
				}

				return
			}

			select {
			case sent := <-queue.sent:
				assert.Equal(t, channelID, sent.channelID)

				resultCmd := decodeCommand(t, sent.body)
				assert.Equal(t, sent.dedupID, resultCmd.DedupID())
				assert.Equal(t, models.CommandActionWebhookResult, resultCmd.Action)
				assert.Equal(t, channelID, resultCmd.SlackChannelID)
				assert.Equal(t, "1700000000.000100", resultCmd.SlackPostID)
				assert.Equal(t, userID, resultCmd.UserID)
				assert.Equal(t, "Test User", resultCmd.UserRealName)
				require.NotNil(t, resultCmd.WebhookResult)
				assert.Equal(t, tt.webhookID, resultCmd.WebhookResult.WebhookID)
				assert.Equal(t, tt.wantResult, resultCmd.WebhookResult.Status)
			case <-time.After(time.Second):
				t.Fatal("webhook result command was not sent")
			}
		})
	}
}

func TestChannelManager_ExecCmd_WebhookIssueNotFound(t *testing.T) {
	t.Parallel()

	const channelID = "C123456"

	slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}
	cm := newTestChannelManager(t, channelID, &stubDB{}, slackClient, &NoopChannelLocker{})

	cmd := models.NewCommand(channelID, "1700000000.000100", "", "U123456", "Test User", models.CommandActionWebhook, nil)
	cmd.WebhookParameters = &models.WebhookCommandParams{WebhookID: "hook1", ViewID: "V123456"}

	issue, skipped, err := cm.execCmd(t.Context(), cmd)

	require.NoError(t, err)
	assert.Nil(t, issue)
	assert.True(t, skipped)

	select {
	case outcome := <-slackClient.webhookOutcomes:
		assert.Equal(t, models.WebhookOutcomeFailed, outcome.Status)
		assert.Equal(t, models.WebhookReasonIssueNotActive, outcome.Reason)
		assert.False(t, outcome.Retryable)
		assert.Equal(t, "V123456", outcome.ViewID)
	case <-time.After(time.Second):
		t.Fatal("webhook outcome was not reported")
	}
}

func TestChannelManager_ExecCmd_OtherCommandIssueNotFound(t *testing.T) {
	t.Parallel()

	const channelID = "C123456"

	slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}
	cm := newTestChannelManager(t, channelID, &stubDB{}, slackClient, &NoopChannelLocker{})

	cmd := models.NewCommand(channelID, "1700000000.000100", "", "U123456", "Test User", models.CommandActionResolveIssue, nil)

	_, skipped, err := cm.execCmd(t.Context(), cmd)

	require.NoError(t, err)
	assert.True(t, skipped)

	select {
	case <-slackClient.webhookOutcomes:
		t.Fatal("no webhook outcome expected for non-webhook commands")
	case <-time.After(50 * time.Millisecond):
	}
}

// deadlineRecordingSlackClient records the context of NotifyWebhookOutcome calls.
type deadlineRecordingSlackClient struct {
	stubSlackClient

	ctxErr      error
	hasDeadline bool
}

func (s *deadlineRecordingSlackClient) NotifyWebhookOutcome(ctx context.Context, _ *models.WebhookOutcome) {
	s.ctxErr = ctx.Err()
	_, s.hasDeadline = ctx.Deadline()
}

func TestNotifyWebhookOutcome_SurvivesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	slackClient := &deadlineRecordingSlackClient{}

	notifyWebhookOutcome(ctx, slackClient, &models.WebhookOutcome{})

	require.NoError(t, slackClient.ctxErr)
	assert.True(t, slackClient.hasDeadline)
}

func TestHTTPWebhookHandler_StatusCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "success", statusCode: http.StatusOK},
		{name: "no content", statusCode: http.StatusNoContent},
		{name: "client error", statusCode: http.StatusUnprocessableEntity, wantErr: true},
		{name: "not found", statusCode: http.StatusNotFound, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			t.Cleanup(server.Close)

			handler := NewHTTPWebhookHandler(&mockLogger{})

			err := handler.HandleWebhook(t.Context(), server.URL, &types.WebhookCallback{ID: "hook1"}, &mockLogger{})

			if !tt.wantErr {
				require.NoError(t, err)
				return
			}

			httpErr, ok := errors.AsType[*WebhookHTTPError](err)
			require.True(t, ok, "expected a WebhookHTTPError, got %v", err)
			assert.Equal(t, tt.statusCode, httpErr.StatusCode)
		})
	}
}

// levelRecordingLogger records the level of each log line.
type levelRecordingLogger struct {
	mockLogger

	mu     sync.Mutex
	levels []string
}

func (l *levelRecordingLogger) Infof(_ string, _ ...any)  { l.record("info") }
func (l *levelRecordingLogger) Errorf(_ string, _ ...any) { l.record("error") }

func (l *levelRecordingLogger) record(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.levels = append(l.levels, level)
}

func TestPostWebhook_LogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantLevels []string
	}{
		{
			name:       "delivered",
			err:        nil,
			wantLevels: nil,
		},
		{
			name:       "http error from target",
			err:        fmt.Errorf("webhook POST failed: %w", &WebhookHTTPError{StatusCode: http.StatusUnprocessableEntity}),
			wantLevels: []string{"info"},
		},
		{
			name:       "timeout",
			err:        fmt.Errorf("webhook POST failed: %w", context.DeadlineExceeded),
			wantLevels: []string{"info"},
		},
		{
			name:       "network error",
			err:        &url.Error{Op: "Post", URL: "https://example.com", Err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}},
			wantLevels: []string{"info"},
		},
		{
			name:       "other error",
			err:        errors.New("sqs: access denied"),
			wantLevels: []string{"error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := &levelRecordingLogger{}
			handler := &funcWebhookHandler{shouldHandle: true, handle: func() error { return tt.err }}
			slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}

			postWebhook(t.Context(), handler, "https://example.com", &types.WebhookCallback{}, logger, nil, nil, slackClient, &models.WebhookOutcome{}, nil)

			assert.Equal(t, tt.wantLevels, logger.levels)
		})
	}
}

// sentCommand is a message sent with recordingCommandSender.
type sentCommand struct {
	channelID string
	dedupID   string
	body      string
	ctxErr    error
}

// recordingCommandSender is a commandSender that records sent messages, and returns err.
type recordingCommandSender struct {
	sent chan sentCommand
	err  error
}

func (s *recordingCommandSender) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	s.sent <- sentCommand{channelID: slackChannelID, dedupID: dedupID, body: body, ctxErr: ctx.Err()}
	return s.err
}

func decodeCommand(t *testing.T, body string) *models.Command {
	t.Helper()

	msg, err := models.NewCommandFromQueueItem(&types.FifoQueueItem{Body: body})
	require.NoError(t, err)

	cmd, ok := msg.(*models.Command)
	require.True(t, ok)

	return cmd
}

func TestChannelManager_SendWebhookResult(t *testing.T) {
	t.Parallel()

	webhookCmd := models.NewCommand("C123456", "1700000000.000100", "", "U123456", "Test User", models.CommandActionWebhook, nil)
	webhookCmd.WebhookParameters = &models.WebhookCommandParams{WebhookID: "hook1"}

	t.Run("nil queue is a no-op", func(t *testing.T) {
		t.Parallel()

		cm := newTestChannelManager(t, "C123456", &stubDB{}, &stubSlackClient{}, &NoopChannelLocker{})
		logger := &levelRecordingLogger{}

		cm.sendWebhookResult(t.Context(), webhookCmd, models.WebhookOutcomeDelivered, nil, logger)

		assert.Empty(t, logger.levels)
	})

	t.Run("sent after context cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		queue := &recordingCommandSender{sent: make(chan sentCommand, 1)}
		cm := newTestChannelManager(t, "C123456", &stubDB{}, &stubSlackClient{}, &NoopChannelLocker{})
		cm.commandQueue = queue

		cm.sendWebhookResult(ctx, webhookCmd, models.WebhookOutcomeDelivered, &types.WebhookAfterClick{StatusText: "Working"}, &levelRecordingLogger{})

		sent := <-queue.sent
		require.NoError(t, sent.ctxErr)

		result := decodeCommand(t, sent.body).WebhookResult
		assert.Equal(t, models.WebhookOutcomeDelivered, result.Status)
		require.NotNil(t, result.AfterClick)
		assert.Equal(t, "Working", result.AfterClick.StatusText)
	})

	t.Run("send error is logged", func(t *testing.T) {
		t.Parallel()

		queue := &recordingCommandSender{sent: make(chan sentCommand, 1), err: errors.New("queue down")}
		cm := newTestChannelManager(t, "C123456", &stubDB{}, &stubSlackClient{}, &NoopChannelLocker{})
		cm.commandQueue = queue
		logger := &levelRecordingLogger{}

		cm.sendWebhookResult(t.Context(), webhookCmd, models.WebhookOutcomeFailed, nil, logger)

		<-queue.sent
		assert.Equal(t, []string{"error"}, logger.levels)
	})
}

func TestPostWebhook_SendResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus models.WebhookOutcomeStatus
	}{
		{name: "delivered", err: nil, wantStatus: models.WebhookOutcomeDelivered},
		{name: "failed", err: errors.New("boom"), wantStatus: models.WebhookOutcomeFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &funcWebhookHandler{shouldHandle: true, handle: func() error { return tt.err }}
			slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}

			var got []models.WebhookOutcomeStatus

			sendResult := func(_ context.Context, status models.WebhookOutcomeStatus, _ *types.WebhookAfterClick) {
				got = append(got, status)
			}

			postWebhook(t.Context(), handler, "https://example.com", &types.WebhookCallback{}, &mockLogger{}, nil, nil, slackClient, &models.WebhookOutcome{}, sendResult)

			assert.Equal(t, []models.WebhookOutcomeStatus{tt.wantStatus}, got)
		})
	}
}

// saveCountingDB is a stubDB that returns a fixed issue for Slack post lookups, and counts issue saves.
type saveCountingDB struct {
	stubDB

	postIssue json.RawMessage
	saves     int
}

func (d *saveCountingDB) FindIssueBySlackPostID(_ context.Context, _, _ string) (string, json.RawMessage, error) {
	return "", d.postIssue, nil
}

func (d *saveCountingDB) SaveIssue(_ context.Context, _ types.Issue) error {
	d.saves++
	return nil
}

func TestChannelManager_ExecCmd_WebhookResult(t *testing.T) {
	t.Parallel()

	const channelID = insightsTestChannel // The channel of existingIssueJSON

	webhookCmd := models.NewCommand(channelID, "1234.5678", "", "U123456", "Test User", models.CommandActionWebhook, nil)
	webhookCmd.WebhookParameters = &models.WebhookCommandParams{WebhookID: "hook1", ViewID: "V123456"}

	t.Run("issue found", func(t *testing.T) {
		t.Parallel()

		db := &saveCountingDB{postIssue: existingIssueJSON(t, types.AlertError, time.Now().UTC(), nil)}
		slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}
		cm := newTestChannelManager(t, channelID, db, slackClient, &NoopChannelLocker{})

		issue, skipped, err := cm.execCmd(t.Context(), models.NewWebhookResultCommand(webhookCmd, models.WebhookOutcomeDelivered))

		require.NoError(t, err)
		assert.NotNil(t, issue)
		assert.False(t, skipped)
		assert.Zero(t, db.saves, "the issue is not changed yet")
	})

	t.Run("missing result parameters", func(t *testing.T) {
		t.Parallel()

		db := &saveCountingDB{postIssue: existingIssueJSON(t, types.AlertError, time.Now().UTC(), nil)}
		cm := newTestChannelManager(t, channelID, db, &stubSlackClient{}, &NoopChannelLocker{})

		cmd := models.NewWebhookResultCommand(webhookCmd, models.WebhookOutcomeDelivered)
		cmd.WebhookResult = nil

		_, _, err := cm.execCmd(t.Context(), cmd)

		require.Error(t, err)
	})

	t.Run("issue not found is skipped without notifying the user", func(t *testing.T) {
		t.Parallel()

		slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}
		cm := newTestChannelManager(t, channelID, &stubDB{}, slackClient, &NoopChannelLocker{})

		issue, skipped, err := cm.execCmd(t.Context(), models.NewWebhookResultCommand(webhookCmd, models.WebhookOutcomeDelivered))

		require.NoError(t, err)
		assert.Nil(t, issue)
		assert.True(t, skipped)

		select {
		case <-slackClient.webhookOutcomes:
			t.Fatal("no webhook outcome expected for webhook result commands")
		case <-time.After(50 * time.Millisecond):
		}
	})
}
