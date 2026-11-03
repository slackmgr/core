package models

import (
	"slices"
	"time"

	"github.com/slackmgr/types"
)

// pendingPostClickGrace is added to the reset delay before a pending post-click state is considered stale, so that
// slow deliveries (timeouts, retries) are not discarded with short reset delays.
const pendingPostClickGrace = time.Minute

// PostClickState is the state of an issue after a webhook button with [types.Webhook.AfterClick] was clicked.
//
// The lifecycle is:
//   - Pending ([Issue.PendingPostClick]): the button was clicked and the webhook is being delivered. Nothing is shown.
//   - Active ([Issue.PostClick], Expired false): the webhook was delivered. Buttons are hidden and a status is shown.
//   - Expired ([Issue.PostClick], Expired true): no alert was received in time. Buttons are shown again, with a note.
//
// Any alert matched to the issue clears both the pending and the active/expired state.
type PostClickState struct {
	// WebhookID is the ID of the clicked webhook.
	WebhookID string `json:"webhookId"`

	// ButtonText is the label of the clicked webhook button.
	ButtonText string `json:"buttonText"`

	// UserID is the Slack user who clicked the webhook button.
	UserID string `json:"userId"`

	// UserRealName is the name of the Slack user who clicked the webhook button.
	UserRealName string `json:"userRealName"`

	// Clicked is the time of the click, i.e. the timestamp of the webhook command. It identifies the click.
	Clicked time.Time `json:"clicked"`

	// AfterClick is a copy of the webhook's after-click settings at the time of the click.
	// StatusText is sanitized (no Slack mentions).
	AfterClick types.WebhookAfterClick `json:"afterClick"`

	// ExpiresAt is the time when an active state expires. Zero while pending.
	ExpiresAt time.Time `json:"expiresAt"`

	// Expired is true when the state expired without an alert being received.
	Expired bool `json:"expired"`
}

// RegisterWebhookClick registers a pending post-click state for the given webhook command, if the webhook has
// after-click settings. It replaces any previous pending state (the latest click wins), but keeps the current
// active state until the new click is delivered. It returns true if the issue was changed.
func (issue *Issue) RegisterWebhookClick(webhook *types.Webhook, cmd *Command) bool {
	if webhook == nil || webhook.AfterClick == nil || cmd == nil {
		return false
	}

	afterClick := *webhook.AfterClick
	afterClick.HideWebhookIDs = slices.Clone(afterClick.HideWebhookIDs)
	afterClick.StatusText = slackMentionRegex.ReplaceAllString(afterClick.StatusText, "*$1*")

	issue.PendingPostClick = &PostClickState{
		WebhookID:    webhook.ID,
		ButtonText:   webhook.ButtonText,
		UserID:       cmd.UserID,
		UserRealName: cmd.UserRealName,
		Clicked:      cmd.Timestamp,
		AfterClick:   afterClick,
	}

	return true
}

// ApplyWebhookResult applies a webhook result to the pending post-click state with the same webhook ID and click
// time. A delivered webhook activates the state, a failed webhook discards it. Results without a matching pending
// state are ignored, e.g. when an alert was received in the meantime, or the button was clicked again.
//
// It returns whether the issue was changed, and whether the Slack post must be updated.
func (issue *Issue) ApplyWebhookResult(result *WebhookResultParams) (bool, bool) {
	pending := issue.PendingPostClick

	if result == nil || pending == nil || pending.WebhookID != result.WebhookID || !pending.Clicked.Equal(result.Clicked) {
		return false, false
	}

	issue.PendingPostClick = nil

	if result.Status != WebhookOutcomeDelivered {
		return true, false
	}

	pending.ExpiresAt = nowFunc().UTC().Add(pending.AfterClick.ResetAfter())
	pending.Expired = false
	issue.PostClick = pending
	issue.SlackPostNeedsUpdate = true

	return true, true
}

// ClearPostClickState clears the pending and active/expired post-click state, typically because an alert was
// received for the issue. It returns whether the issue was changed, and whether the Slack post must be updated.
func (issue *Issue) ClearPostClickState() (bool, bool) {
	changed := issue.PendingPostClick != nil || issue.PostClick != nil
	visible := issue.PostClick != nil

	issue.PendingPostClick = nil
	issue.PostClick = nil

	if visible {
		issue.SlackPostNeedsUpdate = true
	}

	return changed, visible
}

// ExpirePostClickState expires an active post-click state when its reset time has passed, and discards a pending
// state that never got a result (e.g. a lost result command) within the reset delay plus a grace period.
// It returns true if the issue was changed.
// The Slack post is flagged for update when an active state expires.
func (issue *Issue) ExpirePostClickState(now time.Time) bool {
	changed := false

	if pending := issue.PendingPostClick; pending != nil && now.After(pending.Clicked.Add(pending.AfterClick.ResetAfter()+pendingPostClickGrace)) {
		issue.PendingPostClick = nil
		changed = true
	}

	if state := issue.PostClick; state != nil && !state.Expired && !now.Before(state.ExpiresAt) {
		state.Expired = true
		issue.SlackPostNeedsUpdate = true
		changed = true
	}

	return changed
}

// IsWebhookHidden returns true if the button of the webhook with the given ID is hidden by an active post-click state.
func (issue *Issue) IsWebhookHidden(webhookID string) bool {
	state := issue.PostClick

	if state == nil || state.Expired {
		return false
	}

	return state.AfterClick.HidesWebhook(state.WebhookID, webhookID)
}
