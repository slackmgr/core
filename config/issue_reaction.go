package config

// IssueReaction identifies the action triggered by a Slack emoji reaction on an issue
// message. Each constant maps a semantic name to the string value stored in the
// database and matched against user-configured reaction settings.
type IssueReaction string

const (
	// IssueReactionTerminate is the reaction used to indicate that an issue should be terminated.
	IssueReactionTerminate IssueReaction = "terminate"

	// IssueReactionResolve is the reaction used to indicate that an issue has been resolved.
	IssueReactionResolve IssueReaction = "resolve"

	// IssueReactionInvestigate is the reaction used to indicate that an issue is being investigated.
	IssueReactionInvestigate IssueReaction = "investigate"

	// IssueReactionMute is the reaction used to indicate that an issue has been muted.
	IssueReactionMute IssueReaction = "mute"

	// IssueReactionShowOptionButtons is the reaction used to indicate that option buttons should be shown for an issue.
	IssueReactionShowOptionButtons IssueReaction = "show_option_buttons"
)
