package restapi

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/slackmgr/types"
)

const (
	alertsRejectedTotalMetric = "alerts_rejected_total"
	alertsFilteredTotalMetric = "alerts_filtered_total"
)

// Reasons used as label values for alertsRejectedTotalMetric and alertsFilteredTotalMetric.
const (
	rejectReasonInvalidBody          = "invalid_body"
	rejectReasonEmptyRequest         = "empty_request"
	rejectReasonValidation           = "validation"
	rejectReasonNoRoute              = "no_route"
	rejectReasonTooManyInRequest     = "too_many_in_request"
	rejectReasonChannelLookupFailed  = "channel_lookup_failed"
	rejectReasonChannelNotFound      = "channel_not_found"
	rejectReasonChannelArchived      = "channel_archived"
	rejectReasonChannelReadOnly      = "channel_read_only"
	rejectReasonNotInChannel         = "not_in_channel"
	rejectReasonTooManyUsers         = "too_many_users"
	rejectReasonRateLimited          = "rate_limited"
	rejectReasonMissingEncryptionKey = "missing_encryption_key"
	rejectReasonEncryptionFailed     = "encryption_failed"
	rejectReasonQueueError           = "queue_error"

	filterReasonIgnoreTerm = "ignore_term"
)

// allRejectReasons lists every rejection reason, used to pre-warm metric label combinations.
var allRejectReasons = []string{ //nolint:gochecknoglobals // Constant list
	rejectReasonInvalidBody, rejectReasonEmptyRequest, rejectReasonValidation, rejectReasonNoRoute,
	rejectReasonTooManyInRequest, rejectReasonChannelLookupFailed, rejectReasonChannelNotFound,
	rejectReasonChannelArchived, rejectReasonChannelReadOnly, rejectReasonNotInChannel, rejectReasonTooManyUsers, rejectReasonRateLimited,
	rejectReasonMissingEncryptionKey, rejectReasonEncryptionFailed, rejectReasonQueueError,
}

// alertRejectionError is an error that carries the reason an alert was rejected.
type alertRejectionError struct {
	reason string
	err    error
}

func newAlertRejection(reason string, format string, a ...any) *alertRejectionError {
	return &alertRejectionError{reason: reason, err: fmt.Errorf(format, a...)}
}

// Error implements the error interface.
func (r *alertRejectionError) Error() string {
	return r.err.Error()
}

// Unwrap returns the underlying error.
func (r *alertRejectionError) Unwrap() error {
	return r.err
}

// rejectReasonOf returns the rejection reason carried by err, or fallback if err carries none.
func rejectReasonOf(err error, fallback string) string {
	if r, ok := errors.AsType[*alertRejectionError](err); ok {
		return r.reason
	}

	if p, ok := errors.AsType[*processingError](err); ok && p.reason != "" {
		return p.reason
	}

	return fallback
}

// registerAlertIntakeMetrics registers the alert rejection and filter metrics, and pre-warms the label combinations.
func (s *Server) registerAlertIntakeMetrics() {
	s.metrics.RegisterCounter(alertsRejectedTotalMetric, "Total alerts rejected by the API before queuing, by reason. A request body that cannot be parsed counts as one alert", "reason")
	s.metrics.RegisterCounter(alertsFilteredTotalMetric, "Total valid alerts discarded by the API before queuing, by reason (e.g. matching an ignoreIfTextContains term)", "reason")

	for _, reason := range allRejectReasons {
		s.metrics.CounterAdd(alertsRejectedTotalMetric, 0, reason)
	}

	s.metrics.CounterAdd(alertsFilteredTotalMetric, 0, filterReasonIgnoreTerm)
}

// rejectAlerts records that the given alerts were rejected for the given reason, as metrics and insights events.
// With no alerts (e.g. an unparseable request body), one rejection without alert details is recorded.
func (s *Server) rejectAlerts(reason string, alerts ...*types.Alert) {
	if len(alerts) == 0 {
		s.metrics.CounterInc(alertsRejectedTotalMetric, reason)

		if s.insights.Enabled() {
			s.insights.Record(types.InsightsEvent{Type: types.InsightsEventAlertRejected, Reason: reason})
		}

		return
	}

	s.metrics.CounterAdd(alertsRejectedTotalMetric, float64(len(alerts)), reason)

	if s.insights.Enabled() {
		for _, alert := range alerts {
			s.insights.Record(alertInsightsEvent(types.InsightsEventAlertRejected, reason, alert))
		}
	}
}

// filterAlert records that an alert was filtered for the given reason, as a metric and an insights event.
func (s *Server) filterAlert(reason string, alert *types.Alert) {
	s.metrics.CounterInc(alertsFilteredTotalMetric, reason)

	if s.insights.Enabled() {
		s.insights.Record(alertInsightsEvent(types.InsightsEventAlertFiltered, reason, alert))
	}
}

// slackChannelIDRegex matches values in the format of a Slack channel ID: C (public), G (private) or D (direct
// message), followed by at least 8 letters and digits. Channel IDs in alerts are upper-cased by [types.Alert.Clean],
// so a channel name without '-' or '_' (e.g. "deployments") can also match.
var slackChannelIDRegex = regexp.MustCompile(`^[CGD][A-Z0-9]{8,}$`)

// recordQueued records an insights event for an alert that was put on the alert queue.
func (s *Server) recordQueued(alert *types.Alert) {
	if s.insights.Enabled() {
		s.insights.Record(alertInsightsEvent(types.InsightsEventAlertQueued, "", alert))
	}
}

// alertInsightsEvent returns an insights event of the given type and reason describing the alert.
//
// Filtered and rejected alerts may not have a valid channel ID: a channel name that the API cannot map to an ID (e.g.
// a new or renamed channel, before the managed channel list is refreshed) is kept as is. Such a value is recorded in
// Detail instead of ChannelID, so that insights are not grouped by channel names or invalid values. A value is treated
// as a channel ID if it has the format of one, unless Slack reported that the channel does not exist: conversations.info
// only accepts IDs, so an unmapped channel name is always rejected as not found. Queued alerts always have a verified
// channel ID.
func alertInsightsEvent(eventType types.InsightsEventType, reason string, alert *types.Alert) types.InsightsEvent {
	channelID, detail := alert.SlackChannelID, ""

	if eventType != types.InsightsEventAlertQueued && channelID != "" &&
		(reason == rejectReasonChannelNotFound || !slackChannelIDRegex.MatchString(channelID)) {
		channelID, detail = "", channelID
	}

	return types.InsightsEvent{
		Type:          eventType,
		Reason:        reason,
		ChannelID:     channelID,
		Detail:        detail,
		CorrelationID: alert.CorrelationID,
		Severity:      alert.Severity,
		RouteKey:      alert.RouteKey,
		AlertType:     alert.Type,
		Host:          alert.Host,
		Author:        alert.Author,
	}
}
