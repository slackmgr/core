package slack

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	slackapi "github.com/slack-go/slack"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPostClickTestIssue returns an open issue with three webhooks, where "restart" hides itself and "rollback"
// after a click. If active is true, a click on "restart" by Jane at clicked is delivered.
func newPostClickTestIssue(t *testing.T, clicked time.Time, statusText string, active bool) *models.Issue {
	t.Helper()

	issue := newUpdateTestIssue("post-click", "")
	issue.LastAlert.Webhooks = []*types.Webhook{
		{ID: "restart", URL: "https://example.com", ButtonText: "Restart", AfterClick: &types.WebhookAfterClick{
			HideButtons:    types.WebhookHideButtonsClicked,
			HideWebhookIDs: []string{"rollback"},
			StatusText:     statusText,
		}},
		{ID: "rollback", URL: "https://example.com", ButtonText: "Rollback"},
		{ID: "ack", URL: "https://example.com", ButtonText: "Ack"},
	}

	if active {
		cmd := models.NewCommand(updateTestChannel, "", "", "U1", "Jane", models.CommandActionWebhook, nil)
		cmd.Timestamp = clicked
		cmd.WebhookParameters = &models.WebhookCommandParams{WebhookID: "restart"}

		require.True(t, issue.RegisterWebhookClick(issue.FindWebhook("restart"), cmd))
		_, visible := issue.ApplyWebhookResult(models.NewWebhookResultCommand(cmd, models.WebhookOutcomeDelivered))
		require.True(t, visible)
	}

	return issue
}

func buttonIDs(buttons []slackapi.BlockElement) []string {
	ids := []string{}

	for _, b := range buttons {
		if button, ok := b.(*slackapi.ButtonBlockElement); ok {
			ids = append(ids, button.Value)
		}
	}

	return ids
}

func TestGetWebhookButtons_PostClickState(t *testing.T) {
	t.Parallel()

	clicked := time.Now()

	issue := newPostClickTestIssue(t, clicked, "", false)
	assert.Equal(t, []string{"restart", "rollback", "ack"}, buttonIDs(getWebhookButtons(issue)))

	issue = newPostClickTestIssue(t, clicked, "", true)
	assert.Equal(t, []string{"ack"}, buttonIDs(getWebhookButtons(issue)), "clicked and listed buttons are hidden")

	issue.ExpirePostClickState(issue.PostClick.ExpiresAt)
	assert.Equal(t, []string{"restart", "rollback", "ack"}, buttonIDs(getWebhookButtons(issue)), "buttons are shown again when expired")
}

func TestClient_PostClickStatusText(t *testing.T) {
	t.Parallel()

	c := newUpdateTestClient(t, &fakeSlackAPI{})
	clicked := time.Date(2026, 11, 3, 14, 3, 5, 0, time.UTC)
	clickedText := clicked.In(c.cfg.GetLocation()).Format("2006-01-02 15:04:05")

	t.Run("no state", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, c.postClickStatusText(newPostClickTestIssue(t, clicked, "", false)))
	})

	t.Run("active with status text", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue(t, clicked, "Restart in progress", true)
		assert.Equal(t, ":hourglass_flowing_sand: Restart in progress (*Restart* was clicked by *Jane* at "+clickedText+")", c.postClickStatusText(issue))
	})

	t.Run("active without status text", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue(t, clicked, "", true)
		assert.Equal(t, ":hourglass_flowing_sand: *Restart* was clicked by *Jane* at "+clickedText+", waiting for an update.", c.postClickStatusText(issue))
	})

	t.Run("user ID when name is missing", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue(t, clicked, "", true)
		issue.PostClick.UserRealName = ""
		assert.Contains(t, c.postClickStatusText(issue), "clicked by *U1*")
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()

		issue := newPostClickTestIssue(t, clicked, "Restart in progress", true)
		issue.ExpirePostClickState(issue.PostClick.ExpiresAt)
		assert.Equal(t, ":warning: *Restart* was clicked by *Jane* at "+clickedText+", but no update was received. Retry if needed.", c.postClickStatusText(issue))
	})
}

func TestClient_MessageBlocks_PostClickState(t *testing.T) {
	t.Parallel()

	c := newUpdateTestClient(t, &fakeSlackAPI{})

	blocksJSON := func(issue *models.Issue, method postUpdateMethod) string {
		body, err := json.Marshal(c.getMessageBlocks(issue, models.ActionAlert, method))
		require.NoError(t, err)

		return string(body)
	}

	issue := newPostClickTestIssue(t, time.Now(), "Restart in progress", true)

	blocks := blocksJSON(issue, postUpdateMethodUpdate)
	assert.Contains(t, blocks, "Restart in progress")
	assert.Contains(t, blocks, `"value":"ack"`)
	assert.NotContains(t, blocks, `"value":"restart"`)
	assert.Less(t, strings.Index(blocks, `"value":"ack"`), strings.Index(blocks, "Restart in progress"), "the status is shown below the buttons")

	// Deleted posts show no buttons and no post-click status
	assert.NotContains(t, blocksJSON(issue, postUpdateMethodUpdateDeleted), "Restart in progress")
}
