package config

import (
	"context"
	"fmt"
	"time"

	common "github.com/peteraglen/slack-manager-common"
)

type ChannelSettings struct {
	// GlobalAdmins is a list of Slack user IDs that have global Slack Manager admin rights,
	// including using emojis to handle issues, and invoking all alert webhooks.
	// Not to be confused with the Slack workspace's global admins!
	// This list should be kept as short as possible.
	GlobalAdmins []string `json:"globalAdmins" yaml:"globalAdmins"`

	// AlertChannels is an optional list of settings for individual alert channels.
	// Each alert channel can potentially have its own list of admins, and some settings that affect how alerts are handled.
	// If no settings are specified for a channel, the default settings are used.
	AlertChannels []*AlertChannelSettings `json:"alertChannels" yaml:"alertChannels"`

	// InfoChannels is a list of channels defined as "info channels".
	// These channels are used to display information about the Slack Manager system, but cannot be used to handle alerts.
	InfoChannels []*InfoChannelSettings `json:"infoChannels" yaml:"infoChannels"`

	// DefaultPostIconEmoji is the default emoji to use for Slack posts, on the format ":emoji:".
	// This value is used for alerts where no icon emoji is specified.
	// https://api.slack.com/methods/chat.postMessage#arg_icon_emoji
	DefaultPostIconEmoji string `json:"defaultPostIconEmoji" yaml:"defaultPostIconEmoji"`

	// DefaultPostUsername is the default username to use for Slack posts.
	// This value is used for alerts where no username is specified.
	// https://api.slack.com/methods/chat.postMessage#arg_username
	DefaultPostUsername string `json:"defaultPostUsername" yaml:"defaultPostUsername"`

	// DefaultAlertSeverity is the default severity to use for alerts.
	// This value is used for alerts where no severity is specified.
	DefaultAlertSeverity string `json:"defaultAlertSeverity" yaml:"defaultAlertSeverity"`

	// DefaultIssueArchivingDelay is the default delay before an alert is archived.
	// This value is used for alerts where no archiving delay is specified.
	DefaultIssueArchivingDelay time.Duration `json:"defaultIssueArchivingDelay" yaml:"defaultIssueArchivingDelay"`

	globalAdmins  map[string]struct{}
	alertChannels map[string]*AlertChannelSettings
	infoChannels  map[string]*InfoChannelSettings
	initialized   bool
}

type AlertChannelSettings struct {
	ID                    string   `json:"id"                    yaml:"id"`
	AdminUsers            []string `json:"adminUsers"            yaml:"adminUsers"`
	AdminGroups           []string `json:"adminGroups"           yaml:"adminGroups"`
	OrderIssuesBySeverity bool     `json:"orderIssuesBySeverity" yaml:"orderIssuesBySeverity"`

	adminUsers  map[string]struct{}
	adminGroups map[string]struct{}
}

type InfoChannelSettings struct {
	ID           string `json:"channelID"    yaml:"channelID"`
	TemplatePath string `json:"templatePath" yaml:"templatePath"`
}

func (c *ChannelSettings) InitAndValidate() error {
	if c.initialized {
		return nil
	}

	c.globalAdmins = make(map[string]struct{})
	c.alertChannels = make(map[string]*AlertChannelSettings)
	c.infoChannels = make(map[string]*InfoChannelSettings)

	for i, userID := range c.GlobalAdmins {
		if userID == "" {
			return fmt.Errorf("globalAdmins[%d] cannot be empty", i)
		}

		c.globalAdmins[userID] = struct{}{}
	}

	for i, a := range c.AlertChannels {
		if a.ID == "" {
			return fmt.Errorf("alertChannels[%d].id cannot be empty", i)
		}

		a.adminUsers = make(map[string]struct{})
		a.adminGroups = make(map[string]struct{})

		for j, userID := range a.AdminUsers {
			if userID == "" {
				return fmt.Errorf("alertChannels[%d].adminUsers[%d] cannot be empty", i, j)
			}

			a.adminUsers[userID] = struct{}{}
		}

		for j, groupID := range a.AdminGroups {
			if groupID == "" {
				return fmt.Errorf("alertChannels[%d].adminGroups[%d] cannot be empty", i, j)
			}

			a.adminGroups[groupID] = struct{}{}
		}

		c.alertChannels[a.ID] = a
	}

	for i, a := range c.InfoChannels {
		if a.ID == "" {
			return fmt.Errorf("infoChannels[%d].id cannot be empty", i)
		}

		if a.TemplatePath == "" {
			return fmt.Errorf("infoChannels[%d].templatePath cannot be empty", i)
		}

		c.infoChannels[a.ID] = a
	}

	if c.DefaultPostIconEmoji == "" {
		c.DefaultPostIconEmoji = ":female-detective:"
	} else if !common.IconRegex.MatchString(c.DefaultPostIconEmoji) {
		return fmt.Errorf("default icon emoji must be on the format \":emoji:\"")
	}

	if c.DefaultPostUsername == "" {
		c.DefaultPostUsername = "Slack Manager"
	}

	if c.DefaultAlertSeverity == "" {
		c.DefaultAlertSeverity = string(common.AlertError)
	} else if c.DefaultAlertSeverity != string(common.AlertPanic) && c.DefaultAlertSeverity != string(common.AlertError) && c.DefaultAlertSeverity != string(common.AlertWarning) {
		return fmt.Errorf("default alert severity must be one of [%s, %s, %s]", common.AlertPanic, common.AlertError, common.AlertWarning)
	}

	if c.DefaultIssueArchivingDelay <= 0 {
		c.DefaultIssueArchivingDelay = 12 * time.Hour
	} else if c.DefaultIssueArchivingDelay < time.Minute || c.DefaultIssueArchivingDelay > 30*24*time.Hour {
		return fmt.Errorf("default archiving delay must be between 1 minute and 30 days")
	}

	c.initialized = true

	return nil
}

func (c ChannelSettings) UserIsGlobalAdmin(userID string) bool {
	if _, ok := c.globalAdmins[userID]; ok {
		return true
	}

	return false
}

func (c ChannelSettings) UserIsChannelAdmin(ctx context.Context, channelID, userID string, userIsInGroup func(ctx context.Context, groupID, userID string) bool) bool {
	if userID == "" || channelID == "" {
		return false
	}

	if _, ok := c.globalAdmins[userID]; ok {
		return true
	}

	channelConfig, channelFound := c.alertChannels[channelID]

	if !channelFound {
		return false
	}

	if _, ok := channelConfig.adminUsers[userID]; ok {
		return true
	}

	if userIsInGroup != nil {
		for _, groupID := range channelConfig.AdminGroups {
			if userIsInGroup(ctx, groupID, userID) {
				return true
			}
		}
	}

	return false
}

func (c ChannelSettings) IsInfoChannel(channelID string) bool {
	if _, ok := c.infoChannels[channelID]; ok {
		return true
	}

	return false
}

func (c ChannelSettings) GetInfoChannelConfig(channelID string) (*InfoChannelSettings, bool) {
	if c, ok := c.infoChannels[channelID]; ok {
		return c, true
	}

	return nil, false
}

func (c ChannelSettings) OrderIssuesBySeverity(channelID string) bool {
	if channelID == "" {
		return true
	}

	channelConfig, channelFound := c.alertChannels[channelID]

	if !channelFound {
		return true
	}

	return channelConfig.OrderIssuesBySeverity
}
