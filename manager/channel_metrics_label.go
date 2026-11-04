package manager

import (
	"context"
	"time"
)

// channelLabelMaxWait is how long per-channel metrics are held in memory while the Slack channel name is unknown.
// After that, the channel ID is used as metrics label, until the name is resolved.
const channelLabelMaxWait = 5 * time.Minute

// maxPendingObservations caps the histogram observations held while the label is pending. Observations beyond the
// cap are dropped.
const maxPendingObservations = 1000

// channelLabelState is the state of a channel manager's metrics label (the "channel" label of per-channel metrics).
type channelLabelState int

const (
	// channelLabelPending: the Slack channel name is unknown. Per-channel metrics are held in memory, so that no
	// series with the channel ID as label is created when the name is resolved soon (e.g. after a Slack rate limit
	// at startup).
	channelLabelPending channelLabelState = iota

	// channelLabelID: the name was not resolved within [channelLabelMaxWait], and the channel ID is the label. The
	// label switches to the name when it is resolved, see [channelManager.switchMetricsLabel].
	channelLabelID

	// channelLabelName: the Slack channel name is the label. It is not changed by later renames.
	channelLabelName
)

// pendingObservation is a histogram observation held while the label is pending.
type pendingObservation struct {
	name  string
	value float64
}

// initChannelName looks up the Slack channel name when the channel manager starts, and sets the metrics label if the
// lookup succeeds. The lookup runs in the channel manager goroutine, not in the coordinator, so that a slow lookup or
// a conversations.info backoff never delays the routing of alerts to other channels. If the lookup fails, metrics
// are held until the name is resolved (or [channelLabelMaxWait] has passed), and the name is looked up again on
// first use. Must only be called from the channel manager goroutine.
func (c *channelManager) initChannelName(ctx context.Context) {
	if c.labelState != channelLabelPending {
		return
	}

	lookupCtx, cancel := context.WithTimeout(ctx, channelNameLookupTimeout)
	name := c.slackClient.GetChannelName(lookupCtx, c.channelID)
	cancel()

	if name == "" {
		return
	}

	c.slackChannelName = name
	c.channelNameNextRefresh = time.Now().Add(channelNameRefreshInterval(c.cfg))
	c.nameResolved(name)
}

// nameResolved sets the metrics label after a successful name lookup, unless the name is already the label.
func (c *channelManager) nameResolved(name string) {
	switch c.labelState {
	case channelLabelPending:
		c.useLabel(name, channelLabelName)
	case channelLabelID:
		c.switchMetricsLabel(name)
	case channelLabelName:
	}
}

// labelReady reports whether the metrics label is known, i.e. metrics can be emitted. A pending label becomes the
// channel ID when [channelLabelMaxWait] has passed.
func (c *channelManager) labelReady() bool {
	if c.labelState == channelLabelPending && !time.Now().Before(c.labelDeadline) {
		c.logger.WithField("max_wait", channelLabelMaxWait).Info("Slack channel name not resolved, using the channel ID as metrics label")
		c.useLabel(c.channelID, channelLabelID)
	}

	return c.labelState != channelLabelPending
}

// useLabel sets the metrics label, pre-warms the per-channel series so that they appear at /metrics before the first
// event, and emits the metrics held while the label was pending.
func (c *channelManager) useLabel(label string, state channelLabelState) {
	c.channelName = label
	c.labelState = state

	for _, name := range []string{
		alertsTotal,
		processActiveIssuesSkippedMetric,
		issuesArchivedTotalMetric,
		issuesEscalatedTotalMetric,
		issuesCreatedTotalMetric,
		channelLockAcquireFailuresTotalMetric,
		alertsGroupedTotalMetric,
		alertsIgnoredTotalMetric,
		alertsResolvedWithoutIssueTotalMetric,
	} {
		c.metrics.CounterAdd(name, c.pendingCounters[name], label)
	}

	for _, o := range c.pendingObservations {
		c.metrics.Observe(o.name, o.value, label)
	}

	c.pendingCounters = nil
	c.pendingObservations = nil

	c.metrics.GaugeSet(channelOpenIssuesMetric, c.openIssues, label)
}

// switchMetricsLabel switches the metrics label from the channel ID to the Slack channel name, when the name is
// resolved after [channelLabelMaxWait]. Counters and histograms continue under the name; the series under the channel
// ID keep their last value. The open issues gauge is set to zero under the channel ID ([types.Metrics] cannot delete
// a series), so that it is not stuck at its last value, and carried over to the name.
func (c *channelManager) switchMetricsLabel(name string) {
	c.metrics.GaugeSet(channelOpenIssuesMetric, 0, c.channelName)
	c.useLabel(name, channelLabelName)
	c.logger.WithField("channel_name", name).Info("Channel metrics label switched from channel ID to channel name")
}

// counterAdd adds value to a per-channel counter, or holds it while the label is pending.
func (c *channelManager) counterAdd(name string, value float64) {
	if !c.labelReady() {
		c.pendingCounters[name] += value
		return
	}

	c.metrics.CounterAdd(name, value, c.channelName)
}

// observe records a per-channel histogram observation, or holds it while the label is pending.
func (c *channelManager) observe(name string, value float64) {
	if !c.labelReady() {
		if len(c.pendingObservations) < maxPendingObservations {
			c.pendingObservations = append(c.pendingObservations, pendingObservation{name: name, value: value})
		}

		return
	}

	c.metrics.Observe(name, value, c.channelName)
}

// setOpenIssues sets the per-channel open issues gauge. While the label is pending, the value is kept and emitted
// when the label is known.
func (c *channelManager) setOpenIssues(value float64) {
	c.openIssues = value

	if c.labelReady() {
		c.metrics.GaugeSet(channelOpenIssuesMetric, value, c.channelName)
	}
}
