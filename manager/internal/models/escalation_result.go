package models

// EscalationResult represents the outcome of an escalation attempt for an issue.
type EscalationResult struct {
	Issue         *Issue
	Escalated     bool
	MoveToChannel string
}
