package insights

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/slackmgr/types"
)

// Default service settings. See the corresponding [ServiceOption] functions.
const (
	DefaultCurrentStateCacheTTL = 10 * time.Second
	DefaultStaleAfter           = 24 * time.Hour
	DefaultTopN                 = 10
	DefaultTimelineRange        = 90 * 24 * time.Hour
)

// ServiceOption configures a [Service].
type ServiceOption func(*Service)

// WithDB sets a database used for current state queries when no [types.InsightsReader] is available
// (any [types.DB] implementation works, via FindActiveChannels and LoadOpenIssuesInChannel).
func WithDB(db types.DB) ServiceOption {
	return func(s *Service) { s.db = db }
}

// WithClock sets the clock used to evaluate time-dependent issue state and default time ranges. Default time.Now.
func WithClock(now func() time.Time) ServiceOption {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

// WithCurrentStateCacheTTL sets how long [Service.CurrentState] results are cached, to protect the database
// from frequent dashboard refreshes. Zero disables caching. Default [DefaultCurrentStateCacheTTL].
func WithCurrentStateCacheTTL(d time.Duration) ServiceOption {
	return func(s *Service) { s.cacheTTL = max(d, 0) }
}

// Service answers dashboard-style questions about the Slack Manager, on top of a [types.InsightsReader].
//
// Activity methods (summaries, series, breakdowns, lifecycle statistics, timelines) require an insights reader,
// and return [types.ErrInsightsNotSupported] without one. [Service.CurrentState] also works with any
// [types.DB] set with [WithDB]. A Service is safe for concurrent use.
type Service struct {
	reader   types.InsightsReader
	db       types.DB
	now      func() time.Time
	cacheTTL time.Duration

	cacheMu sync.Mutex
	cache   map[string]cachedState
}

type cachedState struct {
	state   *CurrentState
	expires time.Time
}

// New creates a [Service] on top of reader. The reader may be nil, in which case only [Service.CurrentState]
// works, and only if a database is set with [WithDB].
func New(reader types.InsightsReader, opts ...ServiceOption) *Service {
	s := &Service{
		reader:   reader,
		now:      time.Now,
		cacheTTL: DefaultCurrentStateCacheTTL,
		cache:    make(map[string]cachedState),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// NewFromDB creates a [Service] for a database. If db implements [types.InsightsReaderProvider] and returns a
// non-nil reader, all methods are supported; otherwise only [Service.CurrentState] is.
func NewFromDB(db types.DB, opts ...ServiceOption) *Service {
	var reader types.InsightsReader

	if provider, ok := db.(types.InsightsReaderProvider); ok {
		reader = provider.InsightsReader()
	}

	return New(reader, append([]ServiceOption{WithDB(db)}, opts...)...)
}

// Supported returns true if activity queries are supported, i.e. the service has an insights reader.
func (s *Service) Supported() bool {
	return s.reader != nil
}

// Filter selects the time range [From, To) and the channels of a query. Empty ChannelIDs means all channels.
type Filter struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	ChannelIDs []string  `json:"channelIds,omitempty"`
}

func (f Filter) toTypes() types.InsightsFilter {
	return types.InsightsFilter{From: f.From, To: f.To, ChannelIDs: f.ChannelIDs}
}

// Metric identifies a series of events: an event type, optionally restricted to one reason.
type Metric struct {
	Type   types.InsightsEventType `json:"type"`
	Reason string                  `json:"reason,omitempty"`
}

// String returns "type" or "type/reason".
func (m Metric) String() string {
	if m.Reason == "" {
		return string(m.Type)
	}

	return string(m.Type) + "/" + m.Reason
}

// Count is a value with its count, returned by breakdowns.
type Count struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// --- Summary ---

// AlertSummary counts alert outcomes. See the types.InsightsEventAlert* constants for precise definitions.
type AlertSummary struct {
	// Accepted is the number of alerts fully processed by the manager (NewIssue + Grouped + Ignored).
	Accepted int64 `json:"accepted"`
	NewIssue int64 `json:"newIssue"`
	Grouped  int64 `json:"grouped"`
	Ignored  int64 `json:"ignored"`
	Dropped  int64 `json:"dropped"`
	Retried  int64 `json:"retried"`

	// Queued, Filtered and Rejected are only recorded when the API server has an insights sink.
	Queued   int64 `json:"queued"`
	Filtered int64 `json:"filtered"`
	Rejected int64 `json:"rejected"`

	IgnoredByReason  map[string]int64 `json:"ignoredByReason"`
	DroppedByReason  map[string]int64 `json:"droppedByReason"`
	RetriedByReason  map[string]int64 `json:"retriedByReason"`
	FilteredByReason map[string]int64 `json:"filteredByReason"`
	RejectedByReason map[string]int64 `json:"rejectedByReason"`
}

// IssueSummary counts issue lifecycle events. See the types.InsightsEventIssue* constants for precise definitions.
type IssueSummary struct {
	Created int64 `json:"created"`

	// Updates is the number of alert-driven issue updates; UpdatedIssues the number of distinct issues updated.
	Updates       int64 `json:"updates"`
	UpdatedIssues int64 `json:"updatedIssues"`

	Resolved  int64 `json:"resolved"`
	Reopened  int64 `json:"reopened"`
	Escalated int64 `json:"escalated"`
	Moved     int64 `json:"moved"`
	Archived  int64 `json:"archived"`

	ResolvedByReason map[string]int64 `json:"resolvedByReason"`
	ReopenedByReason map[string]int64 `json:"reopenedByReason"`
	MovedByReason    map[string]int64 `json:"movedByReason"`
	ArchivedByReason map[string]int64 `json:"archivedByReason"`
}

// CommandCounts counts the outcomes of one command action.
type CommandCounts struct {
	Executed int64 `json:"executed"`
	Failed   int64 `json:"failed"`
	Skipped  int64 `json:"skipped"`
}

// SlackPostSummary counts Slack post operations.
type SlackPostSummary struct {
	Created        int64            `json:"created"`
	Updated        int64            `json:"updated"`
	Deleted        int64            `json:"deleted"`
	Throttled      int64            `json:"throttled"`
	Failed         int64            `json:"failed"`
	FailedByReason map[string]int64 `json:"failedByReason"`
}

// WebhookSummary counts webhook deliveries.
type WebhookSummary struct {
	Succeeded int64 `json:"succeeded"`
	Failed    int64 `json:"failed"`
}

// Summary holds all activity counts for a time range and set of channels.
type Summary struct {
	Filter     Filter                   `json:"filter"`
	Alerts     AlertSummary             `json:"alerts"`
	Issues     IssueSummary             `json:"issues"`
	Commands   map[string]CommandCounts `json:"commands"`
	SlackPosts SlackPostSummary         `json:"slackPosts"`
	Webhooks   WebhookSummary           `json:"webhooks"`

	// LostEvents is the number of insights events lost in the time range (from gap events, all channels).
	// A non-zero value means the counts may be incomplete.
	LostEvents int64 `json:"lostEvents"`
}

// Summary returns all activity counts for the filter.
func (s *Service) Summary(ctx context.Context, f Filter) (*Summary, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	rows, err := s.count(ctx, &types.InsightsCountQuery{
		InsightsFilter:   f.toTypes(),
		InsightsGrouping: types.InsightsGrouping{GroupBy: []types.InsightsDimension{types.InsightsDimensionType, types.InsightsDimensionReason}},
	})
	if err != nil {
		return nil, err
	}

	sum := &Summary{
		Filter: f,
		Alerts: AlertSummary{
			IgnoredByReason: map[string]int64{}, DroppedByReason: map[string]int64{}, RetriedByReason: map[string]int64{},
			FilteredByReason: map[string]int64{}, RejectedByReason: map[string]int64{},
		},
		Issues: IssueSummary{
			ResolvedByReason: map[string]int64{}, ReopenedByReason: map[string]int64{},
			MovedByReason: map[string]int64{}, ArchivedByReason: map[string]int64{},
		},
		Commands:   map[string]CommandCounts{},
		SlackPosts: SlackPostSummary{FailedByReason: map[string]int64{}},
	}

	for _, r := range rows {
		sum.add(r.Type, r.Reason, r.Count)
	}

	sum.Alerts.Accepted = sum.Alerts.NewIssue + sum.Alerts.Grouped + sum.Alerts.Ignored

	distinct, err := s.count(ctx, &types.InsightsCountQuery{
		InsightsFilter: withTypes(f.toTypes(), types.InsightsEventIssueUpdated),
		Distinct:       types.InsightsDimensionIssue,
	})
	if err != nil {
		return nil, err
	}

	if len(distinct) > 0 {
		sum.Issues.UpdatedIssues = distinct[0].Count
	}

	// Gap events are not channel specific, so they are counted across all channels.
	lost, err := s.stats(ctx, &types.InsightsStatsQuery{
		InsightsFilter: withTypes(types.InsightsFilter{From: f.From, To: f.To}, types.InsightsEventGap),
		ValueKey:       types.InsightsValueLostEvents,
		Aggregates:     []types.InsightsAggregate{types.InsightsAggregateSum},
	})
	if err != nil {
		return nil, err
	}

	if len(lost) > 0 {
		sum.LostEvents = int64(lost[0].Values[types.InsightsAggregateSum])
	}

	return sum, nil
}

func (sum *Summary) add(t types.InsightsEventType, reason string, n int64) {
	a, i, p := &sum.Alerts, &sum.Issues, &sum.SlackPosts

	switch t {
	case types.InsightsEventAlertNewIssue:
		a.NewIssue += n
	case types.InsightsEventAlertGrouped:
		a.Grouped += n
	case types.InsightsEventAlertIgnored:
		a.Ignored += n
		a.IgnoredByReason[reason] += n
	case types.InsightsEventAlertDropped:
		a.Dropped += n
		a.DroppedByReason[reason] += n
	case types.InsightsEventAlertRetried:
		a.Retried += n
		a.RetriedByReason[reason] += n
	case types.InsightsEventAlertQueued:
		a.Queued += n
	case types.InsightsEventAlertFiltered:
		a.Filtered += n
		a.FilteredByReason[reason] += n
	case types.InsightsEventAlertRejected:
		a.Rejected += n
		a.RejectedByReason[reason] += n
	case types.InsightsEventIssueCreated:
		i.Created += n
	case types.InsightsEventIssueUpdated:
		i.Updates += n
	case types.InsightsEventIssueResolved:
		i.Resolved += n
		i.ResolvedByReason[reason] += n
	case types.InsightsEventIssueReopened:
		i.Reopened += n
		i.ReopenedByReason[reason] += n
	case types.InsightsEventIssueEscalated:
		i.Escalated += n
	case types.InsightsEventIssueMoved:
		i.Moved += n
		i.MovedByReason[reason] += n
	case types.InsightsEventIssueArchived:
		i.Archived += n
		i.ArchivedByReason[reason] += n
	case types.InsightsEventCommandExecuted, types.InsightsEventCommandFailed, types.InsightsEventCommandSkipped:
		c := sum.Commands[reason]

		switch t { //nolint:exhaustive // Only command types reach here
		case types.InsightsEventCommandExecuted:
			c.Executed += n
		case types.InsightsEventCommandFailed:
			c.Failed += n
		default:
			c.Skipped += n
		}

		sum.Commands[reason] = c
	case types.InsightsEventWebhookSucceeded:
		sum.Webhooks.Succeeded += n
	case types.InsightsEventWebhookFailed:
		sum.Webhooks.Failed += n
	case types.InsightsEventSlackPostCreated:
		p.Created += n
	case types.InsightsEventSlackPostUpdated:
		p.Updated += n
	case types.InsightsEventSlackPostDeleted:
		p.Deleted += n
	case types.InsightsEventSlackPostThrottled:
		p.Throttled += n
	case types.InsightsEventSlackPostFailed:
		p.Failed += n
		p.FailedByReason[reason] += n
	case types.InsightsEventChannelStats, types.InsightsEventGap:
		// Not part of the activity counts; gap events are reported as LostEvents.
	default:
		// Unknown event types (e.g. added in a newer version) are ignored.
	}
}

func withTypes(f types.InsightsFilter, eventTypes ...types.InsightsEventType) types.InsightsFilter {
	f.Types = eventTypes
	return f
}

// --- Series ---

// SeriesQuery selects time series of event counts.
type SeriesQuery struct {
	Filter

	// Bucket is the time bucket size. Buckets are aligned to the Unix epoch (UTC).
	Bucket time.Duration

	// Metrics lists the series to return. Required.
	Metrics []Metric

	// ByChannel splits each metric into one series per channel.
	ByChannel bool
}

// Series holds zero-filled time series sharing the same buckets.
type Series struct {
	Bucket time.Duration `json:"bucket"`

	// Buckets holds the start time of each bucket, in ascending order.
	Buckets []time.Time `json:"buckets"`

	// Values holds one value per bucket for each series. Keys are the metric (see [Metric.String]), or
	// "<metric>@<channel ID>" for per-channel series.
	Values map[string][]float64 `json:"values"`
}

// Series returns time series of event counts.
//
// The underlying query returns one row per bucket, event type, reason (only if a metric has a reason) and
// channel (only with ByChannel). If that exceeds [types.MaxInsightsRows], it fails with
// [types.ErrInsightsTooManyRows]; use a shorter time range, a larger bucket or fewer channels.
func (s *Service) Series(ctx context.Context, q SeriesQuery) (*Series, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	if len(q.Metrics) == 0 {
		return nil, errors.New("at least one metric is required")
	}

	groupBy := []types.InsightsDimension{types.InsightsDimensionBucket, types.InsightsDimensionType}

	// Grouping by reason multiplies the number of rows, so only do it if a metric needs it.
	if slices.ContainsFunc(q.Metrics, func(m Metric) bool { return m.Reason != "" }) {
		groupBy = append(groupBy, types.InsightsDimensionReason)
	}

	if q.ByChannel {
		groupBy = append(groupBy, types.InsightsDimensionChannel)
	}

	eventTypes := make([]types.InsightsEventType, 0, len(q.Metrics))
	for _, m := range q.Metrics {
		if !slices.Contains(eventTypes, m.Type) {
			eventTypes = append(eventTypes, m.Type)
		}
	}

	rows, err := s.count(ctx, &types.InsightsCountQuery{
		InsightsFilter:   withTypes(q.toTypes(), eventTypes...),
		InsightsGrouping: types.InsightsGrouping{GroupBy: groupBy, Bucket: q.Bucket},
	})
	if err != nil {
		return nil, err
	}

	series := newSeries(q.From, q.To, q.Bucket)

	for _, m := range q.Metrics {
		if !q.ByChannel {
			series.ensure(m.String())
		}
	}

	for _, r := range rows {
		for _, m := range q.Metrics {
			if r.Type != m.Type || (m.Reason != "" && r.Reason != m.Reason) {
				continue
			}

			key := m.String()
			if q.ByChannel {
				key += "@" + r.ChannelID
			}

			series.add(key, r.Bucket, float64(r.Count))
		}
	}

	return series, nil
}

// OpenIssuesSeries returns the number of open issues over time, per channel, from channel snapshots.
// Series keys are channel IDs. The value of a bucket is the highest snapshot in it; buckets without a
// snapshot are zero, so choose a bucket at least as large as the channel stats interval (default 5 minutes).
// If the number of buckets times the number of channels exceeds [types.MaxInsightsRows], it fails with
// [types.ErrInsightsTooManyRows]; use a shorter time range, a larger bucket or fewer channels.
func (s *Service) OpenIssuesSeries(ctx context.Context, f Filter, bucket time.Duration) (*Series, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	rows, err := s.stats(ctx, &types.InsightsStatsQuery{
		InsightsFilter: withTypes(f.toTypes(), types.InsightsEventChannelStats),
		InsightsGrouping: types.InsightsGrouping{
			GroupBy: []types.InsightsDimension{types.InsightsDimensionBucket, types.InsightsDimensionChannel},
			Bucket:  bucket,
		},
		ValueKey:   types.InsightsValueOpen,
		Aggregates: []types.InsightsAggregate{types.InsightsAggregateMax},
	})
	if err != nil {
		return nil, err
	}

	series := newSeries(f.From, f.To, bucket)

	for _, r := range rows {
		series.add(r.ChannelID, r.Bucket, r.Values[types.InsightsAggregateMax])
	}

	return series, nil
}

func newSeries(from, to time.Time, bucket time.Duration) *Series {
	series := &Series{Bucket: bucket, Values: map[string][]float64{}}

	if bucket <= 0 {
		return series
	}

	for t := alignBucket(from, bucket); t.Before(to); t = t.Add(bucket) {
		series.Buckets = append(series.Buckets, t)
	}

	return series
}

func alignBucket(t time.Time, bucket time.Duration) time.Time {
	seconds := int64(bucket / time.Second)
	if seconds <= 0 {
		return t.UTC()
	}

	unix := t.Unix()

	return time.Unix(unix-((unix%seconds)+seconds)%seconds, 0).UTC()
}

func (s *Series) ensure(key string) []float64 {
	values, ok := s.Values[key]
	if !ok {
		values = make([]float64, len(s.Buckets))
		s.Values[key] = values
	}

	return values
}

func (s *Series) add(key string, bucket time.Time, value float64) {
	idx := sort.Search(len(s.Buckets), func(i int) bool { return !s.Buckets[i].Before(bucket) })
	if idx >= len(s.Buckets) || !s.Buckets[idx].Equal(bucket) {
		return
	}

	s.ensure(key)[idx] += value
}

// --- Breakdown ---

// Breakdown returns the top values of a dimension for events of a metric, highest count first.
// For example, the noisiest route keys for new issues, or the most common reasons for dropped alerts.
// A limit of zero means [DefaultTopN].
func (s *Service) Breakdown(ctx context.Context, f Filter, metric Metric, dimension types.InsightsDimension, limit int) ([]Count, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	if dimension == types.InsightsDimensionBucket {
		return nil, errors.New("bucket is not a valid breakdown dimension; use Series")
	}

	if limit <= 0 {
		limit = DefaultTopN
	}

	filter := withTypes(f.toTypes(), metric.Type)
	if metric.Reason != "" {
		filter.Reasons = []string{metric.Reason}
	}

	rows, err := s.count(ctx, &types.InsightsCountQuery{
		InsightsFilter:   filter,
		InsightsGrouping: types.InsightsGrouping{GroupBy: []types.InsightsDimension{dimension}, Limit: limit},
		OrderByCountDesc: true,
	})
	if err != nil {
		return nil, err
	}

	result := make([]Count, 0, len(rows))
	for _, r := range rows {
		result = append(result, Count{Key: groupKeyValue(r.InsightsGroupKey, dimension), Count: r.Count})
	}

	return result, nil
}

func groupKeyValue(k types.InsightsGroupKey, d types.InsightsDimension) string {
	switch d { //nolint:exhaustive // Bucket is rejected by the callers
	case types.InsightsDimensionChannel:
		return k.ChannelID
	case types.InsightsDimensionRelatedChannel:
		return k.RelatedChannelID
	case types.InsightsDimensionType:
		return string(k.Type)
	case types.InsightsDimensionReason:
		return k.Reason
	case types.InsightsDimensionSeverity:
		return string(k.Severity)
	case types.InsightsDimensionRouteKey:
		return k.RouteKey
	case types.InsightsDimensionAlertType:
		return k.AlertType
	case types.InsightsDimensionHost:
		return k.Host
	case types.InsightsDimensionAuthor:
		return k.Author
	case types.InsightsDimensionActor:
		return k.ActorID
	case types.InsightsDimensionCorrelationID:
		return k.CorrelationID
	case types.InsightsDimensionIssue:
		return k.IssueID
	default:
		return ""
	}
}

// --- Lifecycle ---

// ValueStats holds aggregates of a numeric value over a set of issues.
type ValueStats struct {
	Count int64   `json:"count"`
	Avg   float64 `json:"avg"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

// LifecycleStats describes issues archived in the time range.
type LifecycleStats struct {
	Filter Filter `json:"filter"`

	// Archived is the number of archived issues, and ByResolution how they ended (see [types.InsightsEventIssueArchived]).
	Archived     int64            `json:"archived"`
	ByResolution map[string]int64 `json:"byResolution"`

	// OpenSeconds is the time from creation to archiving.
	OpenSeconds ValueStats `json:"openSeconds"`

	// TimeToResolveSeconds is the time from creation to resolution (issues with follow-up, excluding terminated).
	TimeToResolveSeconds ValueStats `json:"timeToResolveSeconds"`

	// TimeToFirstInvestigationSeconds is the time from creation to the first investigation (investigated issues only).
	TimeToFirstInvestigationSeconds ValueStats `json:"timeToFirstInvestigationSeconds"`

	// AlertsPerIssue is the number of alerts per issue.
	AlertsPerIssue ValueStats `json:"alertsPerIssue"`

	// ReopensPerIssue is the number of reopens per issue.
	ReopensPerIssue ValueStats `json:"reopensPerIssue"`

	// Notified is the number of archived issues for which an alert Slack post was sent. Archived - Notified
	// includes issues whose notification was delayed and never sent.
	Notified int64 `json:"notified"`
}

// Lifecycle returns statistics about issues archived in the time range.
func (s *Service) Lifecycle(ctx context.Context, f Filter) (*LifecycleStats, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	filter := withTypes(f.toTypes(), types.InsightsEventIssueArchived)

	rows, err := s.count(ctx, &types.InsightsCountQuery{
		InsightsFilter:   filter,
		InsightsGrouping: types.InsightsGrouping{GroupBy: []types.InsightsDimension{types.InsightsDimensionReason}},
	})
	if err != nil {
		return nil, err
	}

	result := &LifecycleStats{Filter: f, ByResolution: map[string]int64{}}

	for _, r := range rows {
		result.ByResolution[r.Reason] += r.Count
		result.Archived += r.Count
	}

	targets := map[string]*ValueStats{
		types.InsightsValueOpenSeconds:                     &result.OpenSeconds,
		types.InsightsValueTimeToResolveSeconds:            &result.TimeToResolveSeconds,
		types.InsightsValueTimeToFirstInvestigationSeconds: &result.TimeToFirstInvestigationSeconds,
		types.InsightsValueAlertCount:                      &result.AlertsPerIssue,
		types.InsightsValueReopenCount:                     &result.ReopensPerIssue,
	}

	for key, target := range targets {
		stats, err := s.stats(ctx, &types.InsightsStatsQuery{
			InsightsFilter: filter,
			ValueKey:       key,
			Aggregates: []types.InsightsAggregate{
				types.InsightsAggregateAvg, types.InsightsAggregateP50, types.InsightsAggregateP90,
				types.InsightsAggregateP99, types.InsightsAggregateMax,
			},
		})
		if err != nil {
			return nil, err
		}

		if len(stats) > 0 {
			v := stats[0].Values
			*target = ValueStats{
				Count: stats[0].Count,
				Avg:   v[types.InsightsAggregateAvg],
				P50:   v[types.InsightsAggregateP50],
				P90:   v[types.InsightsAggregateP90],
				P99:   v[types.InsightsAggregateP99],
				Max:   v[types.InsightsAggregateMax],
			}
		}
	}

	notified, err := s.stats(ctx, &types.InsightsStatsQuery{
		InsightsFilter: filter,
		ValueKey:       types.InsightsValueNotified,
		Aggregates:     []types.InsightsAggregate{types.InsightsAggregateSum},
	})
	if err != nil {
		return nil, err
	}

	if len(notified) > 0 {
		result.Notified = int64(notified[0].Values[types.InsightsAggregateSum])
	}

	return result, nil
}

// --- Flapping and moves ---

// IssueCount is an issue with a count, e.g. the number of reopens.
type IssueCount struct {
	IssueID       string `json:"issueId"`
	ChannelID     string `json:"channelId"`
	CorrelationID string `json:"correlationId"`
	Count         int64  `json:"count"`
}

// Flapping returns the issues reopened at least minReopens times in the time range, most reopens first.
// A minReopens below 1 is treated as 1, and a limit of zero means [DefaultTopN].
func (s *Service) Flapping(ctx context.Context, f Filter, minReopens int64, limit int) ([]IssueCount, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = DefaultTopN
	}

	rows, err := s.count(ctx, &types.InsightsCountQuery{
		InsightsFilter: withTypes(f.toTypes(), types.InsightsEventIssueReopened),
		InsightsGrouping: types.InsightsGrouping{
			GroupBy: []types.InsightsDimension{types.InsightsDimensionIssue, types.InsightsDimensionChannel, types.InsightsDimensionCorrelationID},
			Limit:   limit,
		},
		OrderByCountDesc: true,
	})
	if err != nil {
		return nil, err
	}

	result := []IssueCount{}

	for _, r := range rows {
		if r.Count >= max(minReopens, 1) {
			result = append(result, IssueCount{IssueID: r.IssueID, ChannelID: r.ChannelID, CorrelationID: r.CorrelationID, Count: r.Count})
		}
	}

	return result, nil
}

// MoveFlow counts issue moves from one channel to another.
type MoveFlow struct {
	SourceChannelID string `json:"sourceChannelId"`
	TargetChannelID string `json:"targetChannelId"`
	Reason          string `json:"reason"`
	Count           int64  `json:"count"`
}

// MoveFlows returns issue moves between channels, most frequent first. If the filter has channels, moves
// both from and to those channels are included.
func (s *Service) MoveFlows(ctx context.Context, f Filter) ([]MoveFlow, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	grouping := types.InsightsGrouping{GroupBy: []types.InsightsDimension{
		types.InsightsDimensionChannel, types.InsightsDimensionRelatedChannel, types.InsightsDimensionReason,
	}}

	filters := []types.InsightsFilter{withTypes(f.toTypes(), types.InsightsEventIssueMoved)}

	if len(f.ChannelIDs) > 0 {
		incoming := withTypes(types.InsightsFilter{From: f.From, To: f.To, RelatedChannelIDs: f.ChannelIDs}, types.InsightsEventIssueMoved)
		filters = append(filters, incoming)
	}

	flows := map[MoveFlow]int64{}

	for _, filter := range filters {
		rows, err := s.count(ctx, &types.InsightsCountQuery{InsightsFilter: filter, InsightsGrouping: grouping})
		if err != nil {
			return nil, err
		}

		for _, r := range rows {
			// Moves between two selected channels are returned by both queries; keep one.
			key := MoveFlow{SourceChannelID: r.ChannelID, TargetChannelID: r.RelatedChannelID, Reason: r.Reason}
			flows[key] = max(flows[key], r.Count)
		}
	}

	result := make([]MoveFlow, 0, len(flows))
	for flow, count := range flows {
		flow.Count = count
		result = append(result, flow)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}

		return moveFlowKey(result[i]) < moveFlowKey(result[j])
	})

	return result, nil
}

func moveFlowKey(f MoveFlow) string {
	return strings.Join([]string{f.SourceChannelID, f.TargetChannelID, f.Reason}, "\x00")
}

// --- Events and timeline ---

// TimelineQuery selects the events of one issue.
type TimelineQuery struct {
	// IssueID is the issue to return events for. Required.
	IssueID string

	// From and To limit the time range. Defaults: the last [DefaultTimelineRange] up to now.
	From time.Time
	To   time.Time

	// Cursor continues a previous page. Limit is the page size (zero means [types.DefaultInsightsPageSize]).
	Cursor string
	Limit  int
}

// IssueTimeline returns the events of one issue, oldest first, for a drill-down view.
func (s *Service) IssueTimeline(ctx context.Context, q TimelineQuery) (*types.InsightsEventPage, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	if q.IssueID == "" {
		return nil, errors.New("issue ID is required")
	}

	to := q.To
	if to.IsZero() {
		to = s.now().Add(time.Minute)
	}

	from := q.From
	if from.IsZero() {
		from = to.Add(-DefaultTimelineRange)
	}

	return s.Events(ctx, &types.InsightsListQuery{
		InsightsFilter: types.InsightsFilter{From: from, To: to, IssueID: q.IssueID},
		Cursor:         q.Cursor,
		Limit:          q.Limit,
	})
}

// Events returns a page of events matching the query.
func (s *Service) Events(ctx context.Context, q *types.InsightsListQuery) (*types.InsightsEventPage, error) {
	if err := s.requireReader(); err != nil {
		return nil, err
	}

	if err := q.Validate(); err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}

	page, err := s.reader.ListInsightsEvents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to list insights events: %w", err)
	}

	return page, nil
}

func (s *Service) requireReader() error {
	if s.reader == nil {
		return types.ErrInsightsNotSupported
	}

	return nil
}

func (s *Service) count(ctx context.Context, q *types.InsightsCountQuery) ([]*types.InsightsCountRow, error) {
	if err := q.Validate(); err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}

	rows, err := s.reader.CountInsightsEvents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to count insights events: %w", err)
	}

	return rows, nil
}

func (s *Service) stats(ctx context.Context, q *types.InsightsStatsQuery) ([]*types.InsightsStatsRow, error) {
	if err := q.Validate(); err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}

	rows, err := s.reader.InsightsValueStats(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to compute insights value stats: %w", err)
	}

	return rows, nil
}
