package models

import "github.com/slackmgr/types"

// EscalationResult represents the outcome of an escalation attempt for an issue.
type EscalationResult struct {
	Issue         *Issue
	Escalated     bool
	MoveToChannel string

	// Changed is true if the escalation became active for the first time, or raised the issue severity,
	// in this attempt. Escalated stays true on every attempt while a rule is active; Changed does not.
	Changed bool

	// PrevSeverity is the issue severity before the escalation was applied (only set when Escalated is true).
	PrevSeverity types.AlertSeverity
}
