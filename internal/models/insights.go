package models

import (
	"time"

	"github.com/slackmgr/types"
)

// InsightsEvent returns an insights event of the given type and reason, describing this alert as processed
// in channelID. RelatedChannelID is set to the original channel if the alert was redirected by a move mapping.
func (a *Alert) InsightsEvent(eventType types.InsightsEventType, reason, channelID string) types.InsightsEvent {
	e := types.InsightsEvent{
		Type:          eventType,
		Reason:        reason,
		ChannelID:     channelID,
		CorrelationID: a.CorrelationID,
		Severity:      a.Severity,
		RouteKey:      a.RouteKey,
		AlertType:     a.Type,
		Host:          a.Host,
		Author:        a.Author,
	}

	if a.OriginalSlackChannelID != "" && a.OriginalSlackChannelID != channelID {
		e.RelatedChannelID = a.OriginalSlackChannelID
	}

	return e
}

// InsightsEvent returns an insights event of the given type and reason, describing this issue in its current channel.
// Count is set to the issue alert count.
func (issue *Issue) InsightsEvent(eventType types.InsightsEventType, reason string) types.InsightsEvent {
	return types.InsightsEvent{
		Type:          eventType,
		Reason:        reason,
		ChannelID:     issue.LastAlert.SlackChannelID,
		IssueID:       issue.ID,
		CorrelationID: issue.CorrelationID,
		Severity:      issue.LastAlert.Severity,
		RouteKey:      issue.LastAlert.RouteKey,
		AlertType:     issue.LastAlert.Type,
		Host:          issue.LastAlert.Host,
		Author:        issue.LastAlert.Author,
		Count:         issue.AlertCount,
	}
}

// Resolution returns how the issue ended (or would end, if archived now), as one of the types.InsightsReason*
// values used by [types.InsightsEventIssueArchived].
func (issue *Issue) Resolution() string {
	switch {
	case issue.IsEmojiTerminated:
		return types.InsightsReasonTerminated
	case !issue.LastAlert.IssueFollowUpEnabled:
		return types.InsightsReasonFireAndForget
	case issue.IsEmojiResolved:
		return types.InsightsReasonUserResolved
	case issue.LastAlert.Severity == types.AlertResolved || issue.LastAlert.Severity == types.AlertInfo:
		return types.InsightsReasonAlertResolved
	case issue.IsResolvedAsInconclusive():
		return types.InsightsReasonAutoResolvedInconclusive
	default:
		return types.InsightsReasonAutoResolved
	}
}

// ArchivedInsightsEvent returns the [types.InsightsEventIssueArchived] event for this issue, archived at the given time.
// The event reason is the [Issue.Resolution], and the values hold lifecycle durations and counts.
func (issue *Issue) ArchivedInsightsEvent(archivedAt time.Time) types.InsightsEvent {
	e := issue.InsightsEvent(types.InsightsEventIssueArchived, issue.Resolution())
	e.Time = archivedAt

	notified := 0.0
	if issue.SlackAlertSentAtLeastOnce {
		notified = 1
	}

	e.Values = map[string]float64{
		types.InsightsValueOpenSeconds: nonNegativeSeconds(archivedAt.Sub(issue.Created)),
		types.InsightsValueAlertCount:  float64(issue.AlertCount),
		types.InsightsValueReopenCount: float64(issue.ReopenCount),
		types.InsightsValueNotified:    notified,
	}

	// Time to resolve is only meaningful for issues with follow-up that were resolved (not terminated).
	if issue.LastAlert.IssueFollowUpEnabled && !issue.IsEmojiTerminated && !issue.ResolveTime.IsZero() && !issue.ResolveTime.After(archivedAt) {
		e.Values[types.InsightsValueTimeToResolveSeconds] = nonNegativeSeconds(issue.ResolveTime.Sub(issue.Created))
	}

	if !issue.FirstInvestigatedAt.IsZero() {
		e.Values[types.InsightsValueTimeToFirstInvestigationSeconds] = nonNegativeSeconds(issue.FirstInvestigatedAt.Sub(issue.Created))
	}

	return e
}

func nonNegativeSeconds(d time.Duration) float64 {
	return max(d.Seconds(), 0)
}
