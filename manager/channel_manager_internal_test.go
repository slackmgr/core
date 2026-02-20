package manager

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"
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

func (s *stubSlackClient) Delete(_ context.Context, _ *models.Issue, _ string, _ bool, _ *semaphore.Weighted) error {
	return nil
}

func (s *stubSlackClient) DeletePost(_ context.Context, _, _ string) error { return nil }

// alwaysFailLocker is a ChannelLocker that immediately returns an error, simulating a lock that can never be obtained.
type alwaysFailLocker struct{}

func (a *alwaysFailLocker) Obtain(_ context.Context, _ string, _ time.Duration, _ time.Duration) (ChannelLock, error) { //nolint:ireturn
	return nil, ErrChannelLockUnavailable
}

// customStubDB wraps stubDB with configurable overrides for specific methods.
type customStubDB struct {
	stubDB

	findOpenIssueFn func(ctx context.Context, channelID, correlationID string) (string, json.RawMessage, error)
	saveAlertFn     func(ctx context.Context, alert *types.Alert) error
}

func (c *customStubDB) FindOpenIssueByCorrelationID(ctx context.Context, channelID, correlationID string) (string, json.RawMessage, error) {
	if c.findOpenIssueFn != nil {
		return c.findOpenIssueFn(ctx, channelID, correlationID)
	}

	return "", nil, nil
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
		slackClient,
		db,
		locker,
		&mockLogger{},
		&types.NoopMetrics{},
		nil, // webhook handlers — a default handler is created internally
		&config.ManagerConfig{},
		settings,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			alert, ackCalled, nackCalled := newTestAlertFromData(t, tt.alertData)
			cm := newTestChannelManager(t, channelID, tt.db, tt.slackClient, tt.locker)

			_ = cm.processAlert(context.Background(), alert)

			assert.Equal(t, tt.expectedAck, *ackCalled, "ack called mismatch")
			assert.Equal(t, tt.expectedNack, *nackCalled, "nack called mismatch")
		})
	}
}
