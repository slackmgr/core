package models

// IssueState represents the current state of an issue.
type IssueState string

const (
	// StateOpen indicates that the issue is currently open and active.
	StateOpen = IssueState("OPEN")

	// StateResolved indicates that the issue has been resolved, but not yet archived. It can be reopened.
	StateResolved = IssueState("RESOLVED")

	// StateArchived indicates that the issue has been archived, and cannot be modified.
	StateArchived = IssueState("ARCHIVED")
)
