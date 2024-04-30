package client

// AlertSeverity represents the severity for a given alert
type WebhookAccessLevel string

const (
	// WebhookAccessLevelGlobalAdmins indicates that a webhook is available only to SUDO Slack Manager global admins.
	WebhookAccessLevelGlobalAdmins WebhookAccessLevel = "global_admins"

	// WebhookAccessLevelChannelAdmins indicates that a webhook is available to the SUDO Slack Manager channel admins (and global admins).
	WebhookAccessLevelChannelAdmins WebhookAccessLevel = "channel_admins"

	// WebhookAccessLevelChannelMembers indicates that a webhook is available to all members in a channel.
	WebhookAccessLevelChannelMembers WebhookAccessLevel = "channel_members"
)

var validWebhookAccessLevels map[WebhookAccessLevel]struct{}

func init() {
	validWebhookAccessLevels = map[WebhookAccessLevel]struct{}{
		WebhookAccessLevelGlobalAdmins:   {},
		WebhookAccessLevelChannelAdmins:  {},
		WebhookAccessLevelChannelMembers: {},
	}
}

func WebhookAccessLevelIsValid(s WebhookAccessLevel) bool {
	_, ok := validWebhookAccessLevels[s]
	return ok
}

func ValidWebhookAccessLevels() []string {
	r := make([]string, len(validWebhookAccessLevels))
	i := 0

	for s := range validWebhookAccessLevels {
		r[i] = string(s)
		i++
	}

	return r
}
