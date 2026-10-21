package restapi

import (
	"errors"
	"fmt"
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
	rejectReasonChannelArchived, rejectReasonNotInChannel, rejectReasonTooManyUsers, rejectReasonRateLimited,
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

// countRejected records that count alerts were rejected for the given reason.
func (s *Server) countRejected(reason string, count int) {
	if count <= 0 {
		return
	}

	s.metrics.CounterAdd(alertsRejectedTotalMetric, float64(count), reason)
}

// countFiltered records that an alert was filtered for the given reason.
func (s *Server) countFiltered(reason string) {
	s.metrics.CounterInc(alertsFilteredTotalMetric, reason)
}
