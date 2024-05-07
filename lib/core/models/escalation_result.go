package models

type EscalationResult struct {
	Issue         *Issue
	Escalated     bool
	MoveToChannel string
}
