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

// channelStatsPrevRun is the persisted channel processing state of the previous processing run.
type channelStatsPrevRun struct {
	processed  time.Time
	openIssues int
}

// recordChannelStats records a channel stats snapshot of the given open issues, if insights are enabled and
// this is the first processing run in the current stats interval window (windows are aligned to the interval).
// Archived issues are ignored. A snapshot with zero open issues is recorded on the first run that finds the
// channel empty, so that time series drop to zero.
//
// The decision is based on the persisted state of the previous run, not on in-memory state, because processing
// runs of a channel alternate between manager instances. Each snapshot is therefore recorded once, by whichever
// instance runs first in the window.
func (c *channelManager) recordChannelStats(now time.Time, prev channelStatsPrevRun, issues []*models.Issue, channelName string) {
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

	// Issues archived by this run leave open at zero, but the next run (which finds the channel empty) records
	// the zero snapshot, so it is recorded exactly once.
	becameEmpty := len(issues) == 0 && prev.openIssues > 0
	newWindow := prev.processed.IsZero() || !now.Truncate(interval).Equal(prev.processed.Truncate(interval))

	if !becameEmpty && (open == 0 || !newWindow) {
		return
	}

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
