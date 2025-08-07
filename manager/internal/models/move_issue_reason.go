package models

type MoveIssueReason string

const (
	MoveIssueReasonEscalation  = MoveIssueReason("ESCALATION")
	MoveIssueReasonUserCommand = MoveIssueReason("USER_COMMAND")
)
