package manager

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSlackClient is a minimal stub implementing SlackClient for channelManager tests.
type stubSlackClient struct {
	isAlertChannelFn func(ctx context.Context, channelID string) (bool, string, error)
}

func (s *stubSlackClient) GetChannelName(_ context.Context, _ string) string { return "" }

func (s *stubSlackClient) IsAlertChannel(ctx context.Context, channelID string) (bool, string, error) {
	if s.isAlertChannelFn != nil {
		return s.isAlertChannelFn(ctx, channelID)
	}

	return true, "", nil
}

func (s *stubSlackClient) Update(_ context.Context, _ string, _ []*models.Issue) error { return nil }

func (s *stubSlackClient) UpdateSingleIssueWithThrottling(_ context.Context, _ *models.Issue, _ string, _ int) error {
	return nil
}

func (s *stubSlackClient) UpdateSingleIssue(_ context.Context, _ *models.Issue, _ string) error {
	return nil
}

func (s *stubSlackClient) Delete(_ context.Context, _ *models.Issue, _ string, _ bool) error {
	return nil
}

func (s *stubSlackClient) DeletePost(_ context.Context, _, _ string) error { return nil }

// alwaysFailLocker is a ChannelLocker that immediately returns an error, simulating a lock that can never be obtained.
type alwaysFailLocker struct{}

func (a *alwaysFailLocker) Obtain(_ context.Context, _ string, _ time.Duration, _ time.Duration) (ChannelLock, error) {
	return nil, ErrChannelLockUnavailable
}

// customStubDB wraps stubDB with configurable overrides for specific methods.
type customStubDB struct {
	stubDB

	findOpenIssueFn   func(ctx context.Context, channelID, correlationID string) (string, json.RawMessage, error)
	findMoveMappingFn func(ctx context.Context, channelID, correlationID string) (json.RawMessage, error)
	saveAlertFn       func(ctx context.Context, alert *types.Alert) error
}

func (c *customStubDB) FindOpenIssueByCorrelationID(ctx context.Context, channelID, correlationID string) (string, json.RawMessage, error) {
	if c.findOpenIssueFn != nil {
		return c.findOpenIssueFn(ctx, channelID, correlationID)
	}

	return "", nil, nil
}

func (c *customStubDB) FindMoveMapping(ctx context.Context, channelID, correlationID string) (json.RawMessage, error) {
	if c.findMoveMappingFn != nil {
		return c.findMoveMappingFn(ctx, channelID, correlationID)
	}

	return nil, nil
}

func (c *customStubDB) SaveAlert(ctx context.Context, alert *types.Alert) error {
	if c.saveAlertFn != nil {
		return c.saveAlertFn(ctx, alert)
	}

	return nil
}

// newTestChannelManager creates a minimal channelManager for use in unit tests.
func newTestChannelManager(t *testing.T, channelID string, db types.DB, slackClient SlackClient, locker ChannelLocker) *channelManager {
	t.Helper()

	settings := models.NewManagerSettingsWrapper(&config.ManagerSettings{
		DefaultAlertSeverity:              types.AlertError,
		DefaultPostUsername:               "testbot",
		DefaultPostIconEmoji:              ":robot:",
		DefaultIssueArchivingDelaySeconds: 600,
	})

	return newChannelManager(
		channelID,
		channelID, // use ID as name in tests; no Slack API available
		slackClient,
		db,
		locker,
		&mockLogger{},
		&types.NoopMetrics{},
		nil, // webhook handlers — a default handler is created internally
		&config.ManagerConfig{},
		settings,
		&NoopRateLimitGate{},
	)
}

// newTestAlertFromData creates a *models.Alert from a types.Alert value, wiring ack/nack tracking.
// Returns the alert and pointers to boolean flags that are set when Ack or Nack is called.
func newTestAlertFromData(t *testing.T, data types.Alert) (*models.Alert, *bool, *bool) {
	t.Helper()

	ackCalled := false
	nackCalled := false

	jsonBody, err := json.Marshal(data)
	require.NoError(t, err)

	msg, err := models.NewAlertFromQueueItem(&types.FifoQueueItem{
		Body: string(jsonBody),
		Ack:  func() { ackCalled = true },
		Nack: func() { nackCalled = true },
	})
	require.NoError(t, err)

	alert, ok := msg.(*models.Alert)
	require.True(t, ok)

	return alert, &ackCalled, &nackCalled
}

func TestChannelManager_ProcessAlert_AckNack(t *testing.T) {
	t.Parallel()

	const channelID = "C123456"

	// validAlert is the base alert used by most test cases. It passes all validation checks.
	validAlert := types.Alert{
		SlackChannelID: channelID,
		Header:         "Test Alert",
		Severity:       types.AlertError,
		CorrelationID:  "corr-123",
	}

	tests := []struct {
		name         string
		db           types.DB
		slackClient  SlackClient
		locker       ChannelLocker
		alertData    types.Alert
		alertSetupFn func(alert *models.Alert)
		assertFn     func(t *testing.T)
		expectedAck  bool
		expectedNack bool
	}{
		{
			name:         "success",
			db:           &stubDB{},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			expectedAck:  true,
			expectedNack: false,
		},
		{
			name:         "lock_failure",
			db:           &stubDB{},
			slackClient:  &stubSlackClient{},
			locker:       &alwaysFailLocker{},
			alertData:    validAlert,
			expectedAck:  false,
			expectedNack: true,
		},
		{
			name:        "validation_failure",
			db:          &stubDB{},
			slackClient: &stubSlackClient{},
			locker:      &NoopChannelLocker{},
			alertData: types.Alert{
				SlackChannelID: channelID,
				Severity:       types.AlertError,
				CorrelationID:  "corr-123",
				// Header and Text both empty → ValidateHeaderAndText() fails.
			},
			expectedAck:  true,
			expectedNack: false,
		},
		{
			name: "db_find_error",
			db: &customStubDB{
				findOpenIssueFn: func(_ context.Context, _, _ string) (string, json.RawMessage, error) {
					return "", nil, errors.New("db error")
				},
			},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			expectedAck:  false,
			expectedNack: true,
		},
		{
			name: "unmarshal_error",
			db: &customStubDB{
				findOpenIssueFn: func(_ context.Context, _, _ string) (string, json.RawMessage, error) {
					return "issue-1", json.RawMessage("invalid json"), nil
				},
			},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			expectedAck:  true,
			expectedNack: false,
		},
		{
			name: "nil_last_alert",
			db: &customStubDB{
				findOpenIssueFn: func(_ context.Context, _, _ string) (string, json.RawMessage, error) {
					body := json.RawMessage(`{"id":"issue-1","correlationId":"corr-123","lastAlert":null}`)
					return "issue-1", body, nil
				},
			},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			expectedAck:  true,
			expectedNack: false,
		},
		{
			name: "clean_escalations_error",
			db:   &stubDB{},
			slackClient: &stubSlackClient{
				isAlertChannelFn: func(_ context.Context, _ string) (bool, string, error) {
					return false, "", errors.New("slack api error")
				},
			},
			locker: &NoopChannelLocker{},
			alertData: types.Alert{
				SlackChannelID: channelID,
				Header:         "Test Alert",
				Severity:       types.AlertError,
				CorrelationID:  "corr-123",
				Escalation: []*types.Escalation{
					{
						DelaySeconds:  60,
						Severity:      types.AlertError,
						MoveToChannel: "CMOVE123",
					},
				},
			},
			expectedAck:  false,
			expectedNack: true,
		},
		{
			name: "save_alert_error",
			db: &customStubDB{
				saveAlertFn: func(_ context.Context, _ *types.Alert) error {
					return errors.New("save alert error")
				},
			},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			expectedAck:  false,
			expectedNack: true,
		},
		{
			name: "find_move_mapping_error_nacks_alert",
			db: &customStubDB{
				findMoveMappingFn: func(_ context.Context, _, _ string) (json.RawMessage, error) {
					return nil, errors.New("db error")
				},
			},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			alertSetupFn: func(alert *models.Alert) { alert.OriginalSlackChannelID = channelID },
			expectedAck:  false,
			expectedNack: true,
		},
		{
			name: "misrouted_alert_nacked_when_move_mapping_points_elsewhere",
			db: &customStubDB{
				findMoveMappingFn: func(_ context.Context, _, _ string) (json.RawMessage, error) {
					m := models.NewMoveMapping("corr-123", channelID, "C-ESCALATED", models.MoveIssueReasonEscalation)
					b, err := json.Marshal(m)
					if err != nil {
						return nil, err
					}
					return b, nil
				},
			},
			slackClient:  &stubSlackClient{},
			locker:       &NoopChannelLocker{},
			alertData:    validAlert,
			alertSetupFn: func(alert *models.Alert) { alert.OriginalSlackChannelID = channelID },
			expectedAck:  false,
			expectedNack: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			alert, ackCalled, nackCalled := newTestAlertFromData(t, tt.alertData)
			if tt.alertSetupFn != nil {
				tt.alertSetupFn(alert)
			}
			cm := newTestChannelManager(t, channelID, tt.db, tt.slackClient, tt.locker)

			_ = cm.processAlert(context.Background(), alert)

			assert.Equal(t, tt.expectedAck, *ackCalled, "ack called mismatch")
			assert.Equal(t, tt.expectedNack, *nackCalled, "nack called mismatch")
			if tt.assertFn != nil {
				tt.assertFn(t)
			}
		})
	}
}

func TestChannelManager_ProcessAlert_LegacyDeletionDelay(t *testing.T) {
	t.Parallel()

	const channelID = "C123456"

	tests := []struct {
		name                  string
		legacyEnabled         bool
		archivingDelaySeconds int
		deletionDelaySeconds  int
		expectedArchiveDelay  int
	}{
		{name: "legacy_disabled_ignores_deletion_delay", legacyEnabled: false, deletionDelaySeconds: 3600, expectedArchiveDelay: 600},
		{name: "legacy_enabled_uses_deletion_delay", legacyEnabled: true, deletionDelaySeconds: 3600, expectedArchiveDelay: 3600},
		{name: "legacy_enabled_prefers_archiving_delay", legacyEnabled: true, archivingDelaySeconds: 120, deletionDelaySeconds: 3600, expectedArchiveDelay: 120},
		{name: "legacy_enabled_without_deletion_delay_uses_default", legacyEnabled: true, expectedArchiveDelay: 600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			savedArchiveDelay := -1
			db := &customStubDB{
				saveAlertFn: func(_ context.Context, alert *types.Alert) error {
					savedArchiveDelay = alert.ArchivingDelaySeconds
					return nil
				},
			}

			cm := newTestChannelManager(t, channelID, db, &stubSlackClient{}, &NoopChannelLocker{})
			cm.cfg.EnableLegacyFeatures = tt.legacyEnabled

			alert, _, _ := newTestAlertFromData(t, types.Alert{
				SlackChannelID:        channelID,
				Header:                "Test Alert",
				Severity:              types.AlertError,
				CorrelationID:         "corr-123",
				ArchivingDelaySeconds: tt.archivingDelaySeconds,
				DeletionDelaySeconds:  tt.deletionDelaySeconds, //nolint:staticcheck // legacy compatibility
			})

			require.NoError(t, cm.processAlert(t.Context(), alert))
			assert.Equal(t, tt.expectedArchiveDelay, savedArchiveDelay)
		})
	}
}

// captureWebhookHandler is a WebhookHandler that forwards the callback data to a channel.
type captureWebhookHandler struct {
	ch chan *types.WebhookCallback
}

func (h *captureWebhookHandler) ShouldHandleWebhook(_ context.Context, _ string) bool { return true }

func (h *captureWebhookHandler) HandleWebhook(_ context.Context, _ string, data *types.WebhookCallback, _ types.Logger) error {
	h.ch <- data
	return nil
}

func TestChannelManager_HandleWebhook_LegacyInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		legacyEnabled bool
		expectedInput map[string]string
	}{
		{name: "legacy_disabled_omits_input", legacyEnabled: false, expectedInput: nil},
		{name: "legacy_enabled_copies_plain_text_input", legacyEnabled: true, expectedInput: map[string]string{"reason": "because"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := &captureWebhookHandler{ch: make(chan *types.WebhookCallback, 1)}

			cm := newTestChannelManager(t, "C123456", &stubDB{}, &stubSlackClient{}, &NoopChannelLocker{})
			cm.webhookHandlers = []WebhookHandler{handler}
			cm.cfg.EnableLegacyFeatures = tt.legacyEnabled

			alert, _, _ := newTestAlertFromData(t, types.Alert{
				SlackChannelID: "C123456",
				Header:         "Test Alert",
				Webhooks:       []*types.Webhook{{ID: "hook1", URL: "https://example.com/hook", ButtonText: "Go"}},
			})

			issue := models.NewIssue(alert, &mockLogger{})
			cmd := &models.Command{
				WebhookParameters: &models.WebhookCommandParams{
					WebhookID:      "hook1",
					PlainTextInput: map[string]string{"reason": "because"},
				},
			}

			_, err := cm.handleWebhook(t.Context(), issue, cmd, &mockLogger{})
			require.NoError(t, err)

			select {
			case data := <-handler.ch:
				assert.Equal(t, map[string]string{"reason": "because"}, data.PlainTextInput)
				assert.Equal(t, tt.expectedInput, data.Input) //nolint:staticcheck // legacy compatibility
			case <-time.After(time.Second):
				t.Fatal("webhook handler was not called")
			}
		})
	}
}

func TestChannelManager_ProcessAlert_ResolvedWithoutIssue(t *testing.T) {
	t.Parallel()

	const channelID = "C654321"

	var savedAlerts atomic.Int32

	db := &customStubDB{
		saveAlertFn: func(_ context.Context, _ *types.Alert) error {
			savedAlerts.Add(1)
			return nil
		},
	}

	cm := newTestChannelManager(t, channelID, db, &stubSlackClient{}, &NoopChannelLocker{})
	metrics := newRecordingMetrics()
	cm.metrics = metrics

	alert, ackCalled, nackCalled := newTestAlertFromData(t, types.Alert{
		SlackChannelID:       channelID,
		Header:               "Resolved",
		Severity:             types.AlertResolved,
		CorrelationID:        "corr-resolved",
		IssueFollowUpEnabled: true,
		AutoResolveSeconds:   3600,
	})

	require.NoError(t, cm.processAlert(context.Background(), alert))

	assert.True(t, *ackCalled)
	assert.False(t, *nackCalled)
	assert.Equal(t, int32(1), savedAlerts.Load(), "the alert is still saved to the alerts table")
	assert.InDelta(t, 1.0, metrics.counter(alertsResolvedWithoutIssueTotalMetric, channelID), 0)
	assert.InDelta(t, 0.0, metrics.counter(issuesCreatedTotalMetric, channelID), 0)
}
