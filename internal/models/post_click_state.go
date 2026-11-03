package models

import (
	"slices"
	"time"

	"github.com/slackmgr/types"
)

// unregisteredClickAlertMargin is the minimum time between the last alert and a click, for a post-click state from the
// receiving system to be applied without a pending state (see [Issue.ApplyWebhookResult]). It allows for clock skew
// between manager instances: applying the state after a newer alert would hide buttons that the alert just updated.
const unregisteredClickAlertMargin = 5 * time.Second

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

	issue.PendingPostClick = &PostClickState{
		WebhookID:    webhook.ID,
		ButtonText:   webhook.ButtonText,
		UserID:       cmd.UserID,
		UserRealName: cmd.UserRealName,
		Clicked:      cmd.Timestamp,
		AfterClick:   sanitizeAfterClick(webhook.AfterClick),
	}

	return true
}

// ApplyWebhookResult applies a webhook result command (see [CommandActionWebhookResult]) to the post-click state.
//
// A result matches the pending post-click state if it has the same webhook ID and click time. A delivered webhook
// activates the matching pending state, with the after-click settings from the receiving system (if any) replacing the
// webhook's own. A failed webhook discards the matching pending state.
//
// A delivered webhook with after-click settings from the receiving system is also applied without a pending state
// (e.g. when the webhook itself has no after-click settings), but only if the click is still the latest: the webhook
// still exists, no newer click is pending or active, and no alert was received since the click (with a margin for
// clock skew). An older pending state is then discarded, since the latest click wins.
//
// Other results are ignored, e.g. when an alert was received in the meantime, or the button was clicked again.
// It returns whether the issue was changed, and whether the Slack post must be updated.
func (issue *Issue) ApplyWebhookResult(cmd *Command) (bool, bool) {
	if cmd == nil || cmd.WebhookResult == nil {
		return false, false
	}

	result := cmd.WebhookResult
	pending := issue.PendingPostClick
	matches := pending != nil && pending.WebhookID == result.WebhookID && pending.Clicked.Equal(result.Clicked)

	if result.Status != WebhookOutcomeDelivered {
		if !matches {
			return false, false
		}

		issue.PendingPostClick = nil

		return true, false
	}

	var state *PostClickState

	switch {
	case matches:
		state = pending

		if result.AfterClick != nil {
			state.AfterClick = sanitizeAfterClick(result.AfterClick)
		}
	case result.AfterClick != nil && issue.isLatestClick(result):
		webhook := issue.FindWebhook(result.WebhookID)

		state = &PostClickState{
			WebhookID:    webhook.ID,
			ButtonText:   webhook.ButtonText,
			UserID:       cmd.UserID,
			UserRealName: cmd.UserRealName,
			Clicked:      result.Clicked,
			AfterClick:   sanitizeAfterClick(result.AfterClick),
		}
	default:
		return false, false
	}

	state.ExpiresAt = nowFunc().UTC().Add(state.AfterClick.ResetAfter())
	state.Expired = false
	issue.PendingPostClick = nil
	issue.PostClick = state
	issue.SlackPostNeedsUpdate = true

	return true, true
}

// isLatestClick returns true if a result without a matching pending state may be applied: the webhook still exists,
// no newer click is pending or active, and no alert was received since the click (see [unregisteredClickAlertMargin]).
func (issue *Issue) isLatestClick(result *WebhookResultParams) bool {
	if issue.FindWebhook(result.WebhookID) == nil {
		return false
	}

	if pending := issue.PendingPostClick; pending != nil && !pending.Clicked.Before(result.Clicked) {
		return false
	}

	if state := issue.PostClick; state != nil && !state.Clicked.Before(result.Clicked) {
		return false
	}

	return issue.LastAlertReceived.Before(result.Clicked.Add(-unregisteredClickAlertMargin))
}

// sanitizeAfterClick returns a copy of the after-click settings, with Slack mentions in the status text neutralised.
func sanitizeAfterClick(afterClick *types.WebhookAfterClick) types.WebhookAfterClick {
	c := *afterClick
	c.HideWebhookIDs = slices.Clone(c.HideWebhookIDs)
	c.StatusText = slackMentionRegex.ReplaceAllString(c.StatusText, "*$1*")

	return c
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
