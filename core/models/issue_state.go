package models

type IssueState string

const (
	StateOpen     = IssueState("OPEN")
	StateResolved = IssueState("RESOLVED")
	StateArchived = IssueState("ARCHIVED")
)
