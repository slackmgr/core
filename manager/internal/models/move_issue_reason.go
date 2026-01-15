package models

// MoveIssueReason represents the reason for moving an issue between Slack channels.
type MoveIssueReason string

const (
	// MoveIssueReasonEscalation indicates the issue was moved due to escalation rules.
	MoveIssueReasonEscalation = MoveIssueReason("ESCALATION")

	// MoveIssueReasonAutoResolve indicates the issue was moved manually by a Slack user.
	MoveIssueReasonUserCommand = MoveIssueReason("USER_COMMAND")
)
