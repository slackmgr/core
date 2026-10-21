package models

type SlackAction string

const (
	ActionNone    = SlackAction("NONE")
	ActionAlert   = SlackAction("ALERT")
	ActionResolve = SlackAction("RESOLVE")
)
