package client

// AlertSeverity represents the severity for a given alert
type AlertSeverity string

const (
	// AlertPanic is used for panic situations (panic icon in Slack).
	AlertPanic AlertSeverity = "panic"

	// AlertError is used for error situations (red error icon in Slack).
	AlertError AlertSeverity = "error"

	// AlertWarning is used for warning situations (yellow warning icon in Slack).
	AlertWarning AlertSeverity = "warning"

	// AlertResolved is used when a previous panic/error/warning situation has been resolved (and IssueFollowUpEnabled is true).
	// The previous icon is replaced with a green OK icon.
	// Not to be confused with an info alert!
	AlertResolved AlertSeverity = "resolved"

	// AlertInfo is used for pure info situations (blue info icon in Slack).
	// Typically used for fire-and-forget status messages, where IssueFollowUpEnabled is false.
	// Not to be confused with a resolved alert!
	AlertInfo AlertSeverity = "info"
)

var (
	alertPriority   map[AlertSeverity]int
	validSeverities map[AlertSeverity]struct{}
)

func init() {
	validSeverities = make(map[AlertSeverity]struct{})
	alertPriority = make(map[AlertSeverity]int)

	validSeverities[AlertPanic] = struct{}{}
	alertPriority[AlertPanic] = 3

	validSeverities[AlertError] = struct{}{}
	alertPriority[AlertError] = 2

	validSeverities[AlertWarning] = struct{}{}
	alertPriority[AlertWarning] = 1

	validSeverities[AlertResolved] = struct{}{}
	alertPriority[AlertResolved] = 0

	validSeverities[AlertInfo] = struct{}{}
	alertPriority[AlertInfo] = 0
}

func SeverityIsValid(s AlertSeverity) bool {
	_, ok := validSeverities[s]
	return ok
}

func SeverityPriority(s AlertSeverity) int {
	val, ok := alertPriority[s]
	if ok {
		return val
	}
	return -1
}

func ValidSeverities() []string {
	r := make([]string, len(validSeverities))
	i := 0

	for s := range validSeverities {
		r[i] = string(s)
		i++
	}

	return r
}
