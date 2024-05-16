package models

type MoveRequest struct {
	UserRealName  string
	Issue         *Issue
	SourceChannel string
	TargetChannel string
}
