package manager

import (
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal/models"
	"github.com/slackmgr/types"
)

// recordAlert records an alert outcome event, if insights are enabled.
func (c *channelManager) recordAlert(alert *models.Alert, eventType types.InsightsEventType, reason string) {
	if !c.insights.Enabled() || eventType == "" {
		return
	}

	c.insights.Record(alert.InsightsEvent(eventType, reason, c.channelID))
}

// recordCommand records the outcome of a command, if insights are enabled.
func (c *channelManager) recordCommand(cmd *models.Command, issue *models.Issue, skipped bool, cmdErr error) {
	if !c.insights.Enabled() {
		return
	}

	eventType := types.InsightsEventCommandExecuted

	switch {
	case cmdErr != nil:
		eventType = types.InsightsEventCommandFailed
	case skipped:
		eventType = types.InsightsEventCommandSkipped
	}

	var e types.InsightsEvent

	if issue != nil && issue.LastAlert != nil {
		e = issue.InsightsEvent(eventType, string(cmd.Action))
		e.ChannelID = c.channelID
	} else {
		e = types.InsightsEvent{Type: eventType, Reason: string(cmd.Action), ChannelID: c.channelID}
	}

	e.ActorID = cmd.UserID
	e.ActorName = cmd.UserRealName

	c.insights.Record(e)
}

// recordResolveTransition records an issue resolved or reopened event if the resolved state of the issue changed.
// The actor is taken from cmd, if non-nil.
func (c *channelManager) recordResolveTransition(issue *models.Issue, wasResolved bool, reason string, cmd *models.Command) {
	if !c.insights.Enabled() {
		return
	}

	isResolved := issue.IsResolved()
	if isResolved == wasResolved {
		return
	}

	eventType := types.InsightsEventIssueResolved
	if !isResolved {
		eventType = types.InsightsEventIssueReopened
	}

	e := issue.InsightsEvent(eventType, reason)

	if cmd != nil {
		e.ActorID = cmd.UserID
		e.ActorName = cmd.UserRealName
	}

	c.insights.Record(e)
}

// recordChannelStats records a channel stats snapshot of the given open issues, if insights are enabled and
// the stats interval has passed since the last snapshot. Archived issues are ignored. A final snapshot with
// zero open issues is recorded when the channel becomes empty, so that time series drop to zero.
func (c *channelManager) recordChannelStats(now time.Time, issues []*models.Issue, channelName string) {
	if !c.insights.Enabled() {
		return
	}

	var open, alerting, resolved, muted, investigated, escalated int

	for _, issue := range issues {
		if issue.Archived {
			continue
		}

		open++

		if !issue.IsInfoOrResolved() {
			alerting++
		}

		if issue.IsResolved() {
			resolved++
		}

		if issue.IsEmojiMuted {
			muted++
		}

		if issue.IsEmojiInvestigated {
			investigated++
		}

		if issue.IsEscalated {
			escalated++
		}
	}

	interval := c.channelStatsInterval
	if interval <= 0 {
		interval = time.Duration(config.DefaultInsightsChannelStatsIntervalSeconds) * time.Second
	}

	becameEmpty := open == 0 && c.lastChannelStatsOpen > 0
	if !becameEmpty && (open == 0 || now.Sub(c.lastChannelStats) < interval) {
		return
	}

	c.lastChannelStats = now
	c.lastChannelStatsOpen = open

	if channelName == "" {
		channelName = c.channelName
	}

	c.insights.Record(types.InsightsEvent{
		Time:      now,
		Type:      types.InsightsEventChannelStats,
		ChannelID: c.channelID,
		Count:     open,
		Detail:    channelName,
		Values: map[string]float64{
			types.InsightsValueOpen:         float64(open),
			types.InsightsValueAlerting:     float64(alerting),
			types.InsightsValueResolved:     float64(resolved),
			types.InsightsValueMuted:        float64(muted),
			types.InsightsValueInvestigated: float64(investigated),
			types.InsightsValueEscalated:    float64(escalated),
		},
	})
}
