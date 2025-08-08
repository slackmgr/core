package models

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
)

var (
	slackMentionRegex         = regexp.MustCompile(`<[@!]([^>\s]+)>`)
	slackMentionEveryoneRegex = regexp.MustCompile(`(?i)<[@!]everyone>`)
)

// Issue represents one or more alerts with the same correlation ID (in the same Slack channel)
type Issue struct {
	ID                        string          `json:"id"`
	CorrelationID             string          `json:"correlationId"`
	Created                   time.Time       `json:"created"`
	FirstAlert                *Alert          `json:"firstAlert"`
	LastAlert                 *Alert          `json:"lastAlert"`
	AlertCount                int             `json:"alertCount"`
	LastAlertReceived         time.Time       `json:"lastAlertReceived"`
	LastSlackMention          string          `json:"lastSlackMention"`
	LastSlackMentionTime      time.Time       `json:"lastSlackMentionTime"`
	AutoResolvePeriod         time.Duration   `json:"autoResolvePeriod"`
	ArchiveDelay              time.Duration   `json:"archiveDelay"`
	ArchiveTime               time.Time       `json:"archiveTime"`
	Archived                  bool            `json:"archived"`
	ResolveTime               time.Time       `json:"resolveTime"`
	SlackPostID               string          `json:"slackPostId"`
	SlackPostCreated          time.Time       `json:"slackPostCreated"`
	SlackPostUpdated          time.Time       `json:"slackPostUpdated"`
	SlackPostHeader           string          `json:"slackPostHeader"`
	SlackPostText             string          `json:"slackPostText"`
	SlackPostLastAction       SlackAction     `json:"slackPostLastAction"`
	SlackPostNeedsUpdate      bool            `json:"slackPostNeedsUpdate"`
	SlackPostNeedsDelete      bool            `json:"slackPostNeedsDelete"`
	SlackAlertSentAtLeastOnce bool            `json:"slackAlertSentAtLeastOnce"`
	SlackPostDelayedUntil     time.Time       `json:"slackPostDelayedUntil"`
	IsEmojiTerminated         bool            `json:"slackPostEmojiTerminated"`
	IsEmojiResolved           bool            `json:"slackPostEmojiResolved"`
	IsEmojiInvestigated       bool            `json:"slackPostEmojiInvestigated"`
	IsEmojiMuted              bool            `json:"slackPostEmojiMuted"`
	IsEmojiButtonsActivated   bool            `json:"slackPostEmojiButtonsActivated"`
	IsMoved                   bool            `json:"isMoved"`
	TerminatedByUser          string          `json:"terminatedByUser"`
	ResolvedByUser            string          `json:"resolvedByUser"`
	InvestigatedByUser        string          `json:"investigatedByUser"`
	InvestigatedSince         time.Time       `json:"investigatedSince"`
	MutedByUser               string          `json:"mutedByUser"`
	MutedSince                time.Time       `json:"mutedSince"`
	MoveReason                MoveIssueReason `json:"moveReason"`
	MovedByUser               string          `json:"movedByUser"`
	IsEscalated               bool            `json:"isEscalated"`

	// cachedJSONBody is used to store the raw JSON body of the issue, after marshalling.
	// This is used to avoid marshalling in both the database cache middleware and the database driver.
	// The middleware uses the MarshalJSONAndCache method to marshal the issue and store the raw JSON body in this field,
	// and MUST then call ResetCachedJSONBody to reset the cachedJSONBody field before returning.
	cachedJSONBody json.RawMessage `json:"-"`
}

// NewIssue creates a new Issue from an Alert
func NewIssue(alert *Alert, logger common.Logger) *Issue {
	now := time.Now().UTC()

	issue := Issue{
		ID:                   internal.Hash(alert.CorrelationID, alert.SlackChannelID, now.Format(time.RFC3339Nano)),
		CorrelationID:        alert.CorrelationID,
		AlertCount:           1,
		Created:              now,
		LastAlertReceived:    now,
		FirstAlert:           alert,
		LastAlert:            alert,
		AutoResolvePeriod:    time.Duration(alert.AutoResolveSeconds) * time.Second,
		ArchiveDelay:         time.Duration(alert.ArchivingDelaySeconds) * time.Second,
		SlackPostNeedsUpdate: true,
		Archived:             false,
	}

	// For new issues, the combination of IssueFollowUpEnabled and severity Info is not allowed. There is nothing to follow up on.
	// Rather than creating an issue that is immediately resolved, we disable follow-up for this alert and treat it as a fire-and-forget info message.
	if alert.IssueFollowUpEnabled && alert.Severity == common.AlertInfo {
		alert.IssueFollowUpEnabled = false
		alert.NotificationDelaySeconds = 0

		logger.WithFields(issue.LogFields()).Info("Disable follow-up for new issue with severity info")
	}

	if alert.IssueFollowUpEnabled {
		if alert.Severity == common.AlertResolved {
			issue.ResolveTime = alert.Timestamp
		} else {
			issue.ResolveTime = alert.Timestamp.Add(issue.AutoResolvePeriod)
		}

		if alert.NotificationDelaySeconds > 0 {
			delay := time.Duration(alert.NotificationDelaySeconds) * time.Second
			issue.SlackPostDelayedUntil = now.Add(delay)
		}
	}

	issue.setArchivingTime()
	issue.sanitizeSlackMentions(false)

	return &issue
}

// ChannelID returns the Slack channel ID that this issue belongs to
func (issue *Issue) ChannelID() string {
	return issue.LastAlert.SlackChannelID
}

// UniqueID returns the ID of the Issue (for database/storage purposes)
func (issue *Issue) UniqueID() string {
	return issue.ID
}

func (issue *Issue) IsOpen() bool {
	return !issue.Archived
}

func (issue *Issue) GetCorrelationID() string {
	return issue.CorrelationID
}

func (issue *Issue) CurrentPostID() string {
	return issue.SlackPostID
}

func (issue *Issue) FollowUpEnabled() bool {
	return issue.LastAlert.IssueFollowUpEnabled
}

// AddAlert adds a new alert to an existing issue.
// Alerts that are older than the previous alert are ignored.
// Alerts with status Resolved are ignored if the issue is already resolved.
// This method is only relevant for issues that have follow-up enabled. For issues without follow-up, all new alerts are ignored.
func (issue *Issue) AddAlert(alert *Alert, logger common.Logger) bool {
	if !alert.Timestamp.After(issue.LastAlert.Timestamp) {
		return false
	}

	if issue.IsResolved() && alert.Severity == common.AlertResolved {
		return false
	}

	if !issue.LastAlert.IssueFollowUpEnabled {
		return false
	}

	// Increase the new alert severity if the issue is escalated AND the current severity is not RESOLVED or INFO
	if issue.IsEscalated &&
		alert.Severity != common.AlertResolved &&
		alert.Severity != common.AlertInfo &&
		common.SeverityPriority(issue.LastAlert.Severity) > common.SeverityPriority(alert.Severity) {
		alert.Severity = issue.LastAlert.Severity
	}

	issue.LastAlert = alert
	issue.LastAlertReceived = time.Now()
	issue.AlertCount++
	issue.AutoResolvePeriod = time.Duration(alert.AutoResolveSeconds) * time.Second
	issue.ArchiveDelay = time.Duration(alert.ArchivingDelaySeconds) * time.Second
	issue.IsEmojiTerminated = false
	issue.IsEmojiResolved = false
	issue.ResolvedByUser = ""
	issue.SlackPostNeedsUpdate = true
	issue.Archived = false

	if alert.Severity == common.AlertInfo || alert.Severity == common.AlertResolved {
		issue.ResolveTime = alert.Timestamp
	} else {
		issue.ResolveTime = alert.Timestamp.Add(issue.AutoResolvePeriod)
	}

	if alert.NotificationDelaySeconds > 0 {
		delay := time.Duration(alert.NotificationDelaySeconds) * time.Second
		issue.SlackPostDelayedUntil = issue.Created.Add(delay)
	}

	issue.setArchivingTime()
	issue.sanitizeSlackMentions(false)

	logger.WithFields(issue.LogFields()).Info("Update issue")

	return true
}

// GetSlackAction returns the Slack action needed for this issue
func (issue *Issue) GetSlackAction() SlackAction {
	if issue.Archived {
		return ActionNone
	}

	if issue.SlackPostDelayedUntil.After(time.Now()) {
		return ActionNone
	}

	if issue.IsEmojiTerminated {
		return ActionNone
	}

	if !issue.LastAlert.IssueFollowUpEnabled {
		if issue.SlackPostNeedsUpdate {
			return ActionAlert
		}
		return ActionNone
	}

	if !issue.IsInfoOrResolved() && issue.SlackPostNeedsUpdate {
		return ActionAlert
	}

	if issue.IsInfoOrResolved() && issue.SlackAlertSentAtLeastOnce && (issue.SlackPostNeedsUpdate || issue.SlackPostLastAction != ActionResolve) {
		return ActionResolve
	}

	return ActionNone
}

// RegisterSlackPostCreatedOrUpdated registers that a Slack post has been created or updated for this issue
func (issue *Issue) RegisterSlackPostCreatedOrUpdated(slackPostID string, action SlackAction) {
	now := time.Now().UTC()

	if issue.SlackPostID != slackPostID {
		issue.SlackPostID = slackPostID
		issue.SlackPostCreated = now
	}

	issue.SlackPostUpdated = now
	issue.SlackPostNeedsUpdate = false
	issue.SlackPostLastAction = action
	issue.SlackPostHeader = issue.LastAlert.Header
	issue.SlackPostText = issue.LastAlert.Text

	if action == ActionAlert {
		issue.SlackAlertSentAtLeastOnce = true
	}
}

// RegisterSlackPostDeleted registers that the Slack post connected to this issue has been deleted
func (issue *Issue) RegisterSlackPostDeleted() {
	issue.SlackPostNeedsUpdate = true
	issue.SlackPostNeedsDelete = false
	issue.SlackPostID = ""
	issue.SlackPostCreated = time.Time{}
	issue.SlackPostUpdated = time.Time{}
	issue.SlackPostHeader = ""
	issue.SlackPostText = ""
}

// RegisterSlackPostInvalidBlocks registers that a Slack post could not be created due to invalid blocks (i.e. bad message formatting).
// It zeros out the Slack post information, but sets the SlackPostNeedsUpdate flag to false, to avoid trying again until the next alert.
func (issue *Issue) RegisterSlackPostInvalidBlocks() {
	issue.SlackPostNeedsUpdate = false
	issue.SlackPostNeedsDelete = false
	issue.SlackPostID = ""
	issue.SlackPostCreated = time.Time{}
	issue.SlackPostUpdated = time.Time{}
	issue.SlackPostHeader = ""
	issue.SlackPostText = ""
}

// RegisterTerminationRequest registers that the Slack post connected to this issue has been marked for termination via Slack reaction/emoji
func (issue *Issue) RegisterTerminationRequest(user string) {
	issue.IsEmojiTerminated = true
	issue.TerminatedByUser = user

	issue.setArchivingTime()
}

func (issue *Issue) RegisterResolveRequest(user string) {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return
	}

	issue.IsEmojiResolved = true
	issue.ResolvedByUser = user
	issue.ResolveTime = time.Now()
	issue.SlackPostNeedsUpdate = true

	issue.setArchivingTime()
}

func (issue *Issue) RegisterUnresolveRequest() {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return
	}

	issue.IsEmojiResolved = false
	issue.ResolvedByUser = ""
	issue.ResolveTime = issue.LastAlert.Timestamp.Add(issue.AutoResolvePeriod)
	issue.SlackPostNeedsUpdate = true

	issue.setArchivingTime()
}

func (issue *Issue) RegisterInvestigateRequest(user string) {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return
	}

	issue.IsEmojiInvestigated = true
	issue.InvestigatedByUser = user
	issue.InvestigatedSince = time.Now()
	issue.SlackPostNeedsUpdate = true
}

func (issue *Issue) RegisterUninvestigateRequest() {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return
	}

	issue.IsEmojiInvestigated = false
	issue.InvestigatedByUser = ""
	issue.InvestigatedSince = time.Time{}
	issue.SlackPostNeedsUpdate = true
}

func (issue *Issue) RegisterMuteRequest(user string) {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return
	}

	issue.IsEmojiMuted = true
	issue.MutedByUser = user
	issue.MutedSince = time.Now()
	issue.SlackPostNeedsUpdate = true
}

func (issue *Issue) RegisterUnmuteRequest() {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return
	}

	issue.IsEmojiMuted = false
	issue.MutedByUser = ""
	issue.MutedSince = time.Time{}
	issue.SlackPostNeedsUpdate = true
}

func (issue *Issue) RegisterShowOptionButtonsRequest() {
	issue.IsEmojiButtonsActivated = true
	issue.SlackPostNeedsUpdate = true
}

func (issue *Issue) RegisterHideOptionButtonsRequest() {
	issue.IsEmojiButtonsActivated = false
	issue.SlackPostNeedsUpdate = true
}

func (issue *Issue) RegisterMoveRequest(reason MoveIssueReason, user, newChannelID, newChannelName string) {
	issue.LastAlert.SlackChannelID = newChannelID
	issue.LastAlert.SlackChannelName = newChannelName

	if issue.LastAlert.OriginalSlackChannelID != newChannelID {
		issue.IsMoved = true
		issue.MoveReason = reason
		issue.MovedByUser = user
	} else {
		issue.IsMoved = false
		issue.MoveReason = MoveIssueReason("")
		issue.MovedByUser = ""
	}

	issue.SlackPostNeedsUpdate = true
}

// IsReadyForArchiving returns true if the issue is ready to be archived
func (issue *Issue) IsReadyForArchiving() bool {
	if !issue.LastAlert.IssueFollowUpEnabled {
		return time.Now().After(issue.ArchiveTime)
	}

	// We can't archive an issue with a connected Slack post, which hasn't been resolved yet
	if !issue.IsEmojiTerminated && (issue.SlackPostID != "" && issue.SlackPostLastAction != ActionResolve) {
		return false
	}

	return time.Now().After(issue.ArchiveTime)
}

// RegisterArchiving registers that the issue has been archived
func (issue *Issue) RegisterArchiving() {
	issue.Archived = true
}

// HasSlackPost returns true if this issue has a connected Slack post at this moment
func (issue *Issue) HasSlackPost() bool {
	return issue.SlackPostID != ""
}

// LastAlertHasActiveMentions returns true if the last alert has active Slack mentions, that are not muted
func (issue *Issue) LastAlertHasActiveMentions() bool {
	return slackMentionRegex.MatchString(issue.LastAlert.Text)
}

// IsLowerPriorityThan returns true if this issue has lower priority than the other issue
func (issue *Issue) IsLowerPriorityThan(other *Issue) bool {
	thisPriority := common.SeverityPriority(issue.LastAlert.Severity)
	otherPriority := common.SeverityPriority(other.LastAlert.Severity)

	// Muted issues are treated as resolved when it comes to sorting
	if issue.IsInfoOrResolved() || issue.IsEmojiMuted {
		thisPriority = common.SeverityPriority(common.AlertResolved)
	}

	// Muted issues are treated as resolved when it comes to sorting
	if other.IsInfoOrResolved() || other.IsEmojiMuted {
		otherPriority = common.SeverityPriority(common.AlertResolved)
	}

	// When priority is the same AND neither issue has an existing Slack post, we take the timestamp into account when sorting
	if thisPriority == otherPriority && !issue.HasSlackPost() && !other.HasSlackPost() {
		return issue.LastAlertReceived.Before(other.LastAlertReceived)
	}

	return thisPriority < otherPriority
}

func (issue *Issue) IsResolvedAsInconclusive() bool {
	if !issue.LastAlert.AutoResolveAsInconclusive {
		return false
	}

	if issue.LastAlert.Severity == common.AlertInfo || issue.LastAlert.Severity == common.AlertResolved {
		return false
	}

	if issue.IsEmojiResolved {
		return false
	}

	return true
}

func (issue *Issue) IsResolved() bool {
	return issue.LastAlert.Severity == common.AlertResolved || (issue.LastAlert.IssueFollowUpEnabled && time.Now().After(issue.ResolveTime))
}

func (issue *Issue) IsInfoOrResolved() bool {
	return issue.LastAlert.Severity == common.AlertInfo || issue.IsResolved()
}

func (issue *Issue) LogFields() map[string]any {
	if issue == nil {
		return nil
	}

	correlationID := issue.CorrelationID

	if len(correlationID) > 100 {
		correlationID = correlationID[0:100]
	}

	fields := map[string]any{
		"context":              "Issue processing",
		"correlation_id":       correlationID,
		"channel_id":           issue.LastAlert.SlackChannelID,
		"channel_name":         issue.LastAlert.SlackChannelName,
		"last_alert_timestamp": issue.LastAlert.Timestamp.Format(time.RFC3339),
		"last_alert_received":  issue.LastAlertReceived.Format(time.RFC3339),
		"alert_count":          issue.AlertCount,
		"resolve_time":         issue.ResolveTime.Format(time.RFC3339),
		"archive_time":         issue.ArchiveTime.Format(time.RFC3339),
		"slack_post_id":        issue.SlackPostID,
		"follow_up_enabled":    issue.LastAlert.IssueFollowUpEnabled,
	}

	return fields
}

func (issue *Issue) ApplyEscalationRules() *EscalationResult {
	result := &EscalationResult{
		Issue:     issue,
		Escalated: false,
	}

	// Issues that are info/resolved or archived should never be escalated
	if issue.IsInfoOrResolved() || issue.Archived {
		return result
	}

	a := issue.LastAlert

	// No escalation rules -> do nothing
	if len(a.Escalation) == 0 {
		return result
	}

	var escalation *common.Escalation
	issueAge := int(time.Since(issue.Created).Seconds())

	// The alerts API ensures that escalation rules are sorted by delay (ascending)
	// Find the last rule that has a delay smaller than the current issue age
	for _, e := range a.Escalation {
		if e.DelaySeconds <= issueAge {
			escalation = e
		}
	}

	// No active rule found at the current time -> do nothing
	if escalation == nil {
		return result
	}

	result.Escalated = true

	// Note the fact that we are escalating the issue
	issue.IsEscalated = true

	// Add mentions, if any
	if len(escalation.SlackMentions) > 0 {
		issue.LastAlert.Text = issue.LastAlert.OriginalText + "\n*Att:* " + strings.Join(escalation.SlackMentions, " ")
	}

	// Determine the current issue priority (which may be the result of a previous escalation)
	currentPriority := common.SeverityPriority(issue.LastAlert.Severity)

	// Determine the priority of the current escalation rule
	escalatedPriority := common.SeverityPriority(escalation.Severity)

	// Update the issue severity
	issue.LastAlert.Severity = escalation.Severity

	// Register the new channel to move the issue to, if applicable
	if escalation.MoveToChannel != "" && escalation.MoveToChannel != issue.ChannelID() {
		result.MoveToChannel = escalation.MoveToChannel
	}

	escalationHasIncreased := escalatedPriority > currentPriority

	// Flag that the Slack post needs an update, if the escalation has gone up
	if escalationHasIncreased {
		issue.SlackPostNeedsUpdate = true

		// If there are mentions, we also need to flag that the Slack post must be deleted (and re-created). This is to ensure that a mention triggers a user notification.
		if len(escalation.SlackMentions) > 0 {
			issue.SlackPostNeedsDelete = true
		}
	}

	// Sanitize the Slack mentions, and skip muting if the escalation has increased
	issue.sanitizeSlackMentions(escalationHasIncreased)

	return result
}

func (issue *Issue) FindWebhook(id string) *common.Webhook {
	for _, hook := range issue.LastAlert.Webhooks {
		if hook.ID == id {
			return hook
		}
	}

	return nil
}

func (issue *Issue) MarshalJSON() ([]byte, error) {
	// If the cached JSON body is set, return it directly to avoid re-marshalling
	if issue.cachedJSONBody != nil {
		return issue.cachedJSONBody, nil
	}

	type Alias Issue

	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(issue),
	})
}

func (issue *Issue) MarshalJSONAndCache() ([]byte, error) {
	data, err := issue.MarshalJSON()
	if err != nil {
		return nil, err
	}

	issue.cachedJSONBody = data

	return data, nil
}

func (issue *Issue) ResetCachedJSONBody() {
	issue.cachedJSONBody = nil
}

func (issue *Issue) setArchivingTime() {
	// Manually terminated issues are archived immediately
	if issue.IsEmojiTerminated {
		issue.ArchiveTime = time.Now()
		return
	}

	// Fire-and-forget issues are archived after a small delay (to account for alert spam situations)
	if !issue.LastAlert.IssueFollowUpEnabled {
		issue.ArchiveTime = issue.LastAlertReceived.Add(30 * time.Second)
		return
	}

	// Resolved issues are archived after ArchiveDelay, measured from the resolve timestamp
	if issue.IsInfoOrResolved() {
		issue.ArchiveTime = issue.ResolveTime.Add(issue.ArchiveDelay)
		return
	}

	// All other issues are archived after AutoResolvePeriod+ArchivingDelay, measured from the last alert
	issue.ArchiveTime = issue.LastAlert.Timestamp.Add(issue.AutoResolvePeriod + issue.ArchiveDelay)
}

// sanitizeSlackMentions ensures that no illegal mentions are present, and that mentions are muted when required
func (issue *Issue) sanitizeSlackMentions(skipMuting bool) {
	// Mentions are only allowed in the alert text
	issue.LastAlert.Header = slackMentionRegex.ReplaceAllString(issue.LastAlert.Header, "*$1*")
	issue.LastAlert.HeaderWhenResolved = slackMentionRegex.ReplaceAllString(issue.LastAlert.HeaderWhenResolved, "*$1*")
	issue.LastAlert.Author = slackMentionRegex.ReplaceAllString(issue.LastAlert.Author, "*$1*")
	issue.LastAlert.Host = slackMentionRegex.ReplaceAllString(issue.LastAlert.Host, "*$1*")
	issue.LastAlert.Footer = slackMentionRegex.ReplaceAllString(issue.LastAlert.Footer, "*$1*")
	issue.LastAlert.FallbackText = slackMentionRegex.ReplaceAllString(issue.LastAlert.FallbackText, "*$1*")
	issue.LastAlert.TextWhenResolved = slackMentionRegex.ReplaceAllString(issue.LastAlert.TextWhenResolved, "*$1*")

	for _, f := range issue.LastAlert.Fields {
		f.Title = slackMentionRegex.ReplaceAllString(f.Title, "*$1*")
		f.Value = slackMentionRegex.ReplaceAllString(f.Value, "*$1*")
	}

	// @everyone is never allowed, even in the text field
	issue.LastAlert.Text = slackMentionEveryoneRegex.ReplaceAllString(issue.LastAlert.Text, "*everyone*")

	// Find all mentions in the alert text
	mentions := slackMentionRegex.FindAllString(issue.LastAlert.Text, -1)

	if mentions == nil {
		return
	}

	// Concat the mentions to a single string (for comparison)
	mentionsString := strings.Join(mentions, ",")

	// If skipMuting is true, we keep the mentions regardless of previous mention actions
	if skipMuting {
		issue.LastSlackMentionTime = time.Now()
		issue.LastSlackMention = mentionsString
		return
	}

	// Allow mentions if the mention string has changed OR at least 60 minutes have passed since the last mention
	if issue.LastSlackMention != mentionsString || time.Since(issue.LastSlackMentionTime) > time.Hour {
		issue.LastSlackMentionTime = time.Now()
		issue.LastSlackMention = mentionsString
	} else {
		issue.LastAlert.Text = slackMentionRegex.ReplaceAllString(issue.LastAlert.Text, "*$1*")
	}
}
