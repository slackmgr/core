package manager

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postClickStubDB is an insightsStubDB that records saved issues.
type postClickStubDB struct {
	insightsStubDB

	mu    sync.Mutex
	saved []*models.Issue
}

func (d *postClickStubDB) SaveIssue(ctx context.Context, issue types.Issue) error {
	return d.SaveIssues(ctx, issue)
}

func (d *postClickStubDB) SaveIssues(_ context.Context, issues ...types.Issue) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, issue := range issues {
		if m, ok := issue.(*models.Issue); ok {
			d.saved = append(d.saved, m)
		}
	}

	return nil
}

func (d *postClickStubDB) savedIssues() []*models.Issue {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]*models.Issue{}, d.saved...)
}

var postClickAfterClick = &types.WebhookAfterClick{HideButtons: types.WebhookHideButtonsClicked, StatusText: "Restart in progress"}

// postClickIssueJSON returns the JSON of an existing issue (Slack post "1234.5678") with a "restart" webhook that has
// after-click settings. The mutate func is applied before marshalling.
func postClickIssueJSON(t *testing.T, alertTime time.Time, mutate func(issue *models.Issue)) json.RawMessage {
	t.Helper()

	return existingIssueJSON(t, types.AlertError, alertTime, func(issue *models.Issue) {
		issue.ID = "issue-1"
		issue.LastAlert.Webhooks = []*types.Webhook{{ID: "restart", URL: "https://example.com", ButtonText: "Restart", AfterClick: postClickAfterClick}}

		if mutate != nil {
			mutate(issue)
		}
	})
}

// postClickCommand returns a webhook command for the "restart" webhook, clicked at the given time.
func postClickCommand(clicked time.Time) *models.Command {
	cmd := models.NewCommand(insightsTestChannel, "1234.5678", "", "U1", "Jane", models.CommandActionWebhook, nil)
	cmd.Timestamp = clicked
	cmd.WebhookParameters = &models.WebhookCommandParams{WebhookID: "restart"}

	return cmd
}

// withPendingClick registers a pending click for cmd.
func withPendingClick(cmd *models.Command) func(issue *models.Issue) {
	return func(issue *models.Issue) {
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)
	}
}

// withActiveClick registers and activates a click for cmd.
func withActiveClick(cmd *models.Command) func(issue *models.Issue) {
	return func(issue *models.Issue) {
		issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd)
		issue.ApplyWebhookResult(models.NewWebhookResultCommand(cmd, models.WebhookOutcomeDelivered).WebhookResult)
		issue.SlackPostNeedsUpdate = false
	}
}

func TestChannelManager_WebhookResult_PostClickState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	click := postClickCommand(now.Add(-time.Second))

	tests := []struct {
		name            string
		issue           json.RawMessage
		status          models.WebhookOutcomeStatus
		wantSaved       bool
		wantSlackUpdate bool
		wantActive      bool
	}{
		{
			name:            "delivered activates the state and updates Slack",
			issue:           postClickIssueJSON(t, now.Add(-time.Minute), withPendingClick(click)),
			status:          models.WebhookOutcomeDelivered,
			wantSaved:       true,
			wantSlackUpdate: true,
			wantActive:      true,
		},
		{
			name:      "failed discards the pending state",
			issue:     postClickIssueJSON(t, now.Add(-time.Minute), withPendingClick(click)),
			status:    models.WebhookOutcomeFailed,
			wantSaved: true,
		},
		{
			name:   "no pending state (cleared by an alert) is ignored",
			issue:  postClickIssueJSON(t, now.Add(-time.Minute), nil),
			status: models.WebhookOutcomeDelivered,
		},
		{
			name:   "newer click is not activated by an older result",
			issue:  postClickIssueJSON(t, now.Add(-time.Minute), withPendingClick(postClickCommand(now))),
			status: models.WebhookOutcomeDelivered,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := &postClickStubDB{insightsStubDB: insightsStubDB{postIssue: tt.issue}}
			slackClient := &stubSlackClient{}
			cm := newTestChannelManager(t, insightsTestChannel, db, slackClient, &NoopChannelLocker{})

			require.NoError(t, cm.processCmd(t.Context(), models.NewWebhookResultCommand(click, tt.status)))

			saved := db.savedIssues()

			if !tt.wantSaved {
				assert.Empty(t, saved)
				assert.Empty(t, slackClient.singleIssueUpdateReasons())

				return
			}

			require.Len(t, saved, 1)
			assert.Nil(t, saved[0].PendingPostClick)
			assert.Equal(t, tt.wantActive, saved[0].PostClick != nil)

			if tt.wantSlackUpdate {
				assert.Equal(t, []string{"webhook post-click state"}, slackClient.singleIssueUpdateReasons())
			} else {
				assert.Empty(t, slackClient.singleIssueUpdateReasons())
			}
		})
	}
}

func TestChannelManager_HandleWebhook_SavesPendingClick(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()

	db := &postClickStubDB{insightsStubDB: insightsStubDB{postIssue: postClickIssueJSON(t, now.Add(-time.Minute), nil)}}
	slackClient := &stubSlackClient{webhookOutcomes: make(chan *models.WebhookOutcome, 1)}
	queue := &recordingCommandSender{sent: make(chan sentCommand, 1)}

	cm := newTestChannelManager(t, insightsTestChannel, db, slackClient, &NoopChannelLocker{})
	cm.cfg.EncryptionKey = "abcdefghijklmnopqrstuvwxyz123456"
	cm.webhookHandlers = []WebhookHandler{&funcWebhookHandler{shouldHandle: true, handle: func() error { return nil }}}
	cm.commandQueue = queue

	click := postClickCommand(now)
	require.NoError(t, cm.processCmd(t.Context(), click))

	saved := db.savedIssues()
	require.Len(t, saved, 1)
	require.NotNil(t, saved[0].PendingPostClick)
	assert.True(t, click.Timestamp.Equal(saved[0].PendingPostClick.Clicked))
	assert.Empty(t, slackClient.singleIssueUpdateReasons(), "the Slack post is not changed while pending")

	select {
	case sent := <-queue.sent:
		result := decodeCommand(t, sent.body).WebhookResult
		require.NotNil(t, result)
		assert.True(t, click.Timestamp.Equal(result.Clicked))
	case <-time.After(time.Second):
		t.Fatal("webhook result command was not sent")
	}
}

func TestChannelManager_Alert_ClearsPostClickState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	lastAlertTime := now.Add(-time.Minute)
	click := postClickCommand(now.Add(-30 * time.Second))

	tests := []struct {
		name            string
		issue           json.RawMessage
		alertTime       time.Time
		wantSaved       bool
		wantSlackUpdate bool
	}{
		{
			name:            "ignored (not newer) alert clears the active state",
			issue:           postClickIssueJSON(t, lastAlertTime, withActiveClick(click)),
			alertTime:       lastAlertTime,
			wantSaved:       true,
			wantSlackUpdate: true,
		},
		{
			name:      "ignored alert clears the pending state without a Slack update",
			issue:     postClickIssueJSON(t, lastAlertTime, withPendingClick(click)),
			alertTime: lastAlertTime,
			wantSaved: true,
		},
		{
			name:      "ignored alert without state changes nothing",
			issue:     postClickIssueJSON(t, lastAlertTime, nil),
			alertTime: lastAlertTime,
		},
		{
			name:      "accepted alert clears the active state",
			issue:     postClickIssueJSON(t, lastAlertTime, withActiveClick(click)),
			alertTime: now,
			wantSaved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := &postClickStubDB{insightsStubDB: insightsStubDB{openIssue: tt.issue}}
			slackClient := &stubSlackClient{}
			cm := newTestChannelManager(t, insightsTestChannel, db, slackClient, &NoopChannelLocker{})

			alert, acked, _ := newTestAlertFromData(t, validInsightsAlert(types.AlertError, tt.alertTime))
			require.NoError(t, cm.processAlert(t.Context(), alert))
			assert.True(t, *acked)

			saved := db.savedIssues()

			if !tt.wantSaved {
				assert.Empty(t, saved)
				assert.Empty(t, slackClient.singleIssueUpdateReasons())

				return
			}

			require.Len(t, saved, 1)
			assert.Nil(t, saved[0].PendingPostClick)
			assert.Nil(t, saved[0].PostClick)

			if tt.wantSlackUpdate {
				assert.Equal(t, []string{"webhook post-click state cleared"}, slackClient.singleIssueUpdateReasons())
			}
		})
	}
}

func TestChannelManager_ProcessActiveIssues_ExpiresPostClickState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	click := postClickCommand(now.Add(-time.Hour))

	issue := postClickIssueJSON(t, now, func(issue *models.Issue) {
		withActiveClick(click)(issue)
		issue.PostClick.ExpiresAt = now.Add(-time.Second)
	})

	db := &postClickStubDB{insightsStubDB: insightsStubDB{openIssues: map[string]json.RawMessage{"issue-1": issue}}}
	cm := newTestChannelManager(t, insightsTestChannel, db, &stubSlackClient{}, &NoopChannelLocker{})

	_, err := cm.processActiveIssues(t.Context(), time.Second)
	require.NoError(t, err)

	saved := db.savedIssues()
	require.Len(t, saved, 1)
	require.NotNil(t, saved[0].PostClick)
	assert.True(t, saved[0].PostClick.Expired)
	assert.True(t, saved[0].SlackPostNeedsUpdate)
}
