package config

import (
	"context"
	"time"

	"github.com/peteraglen/slack-manager/lib/common"
)

type ChannelManagerConfig struct {
	AlertChannelSize   int
	CommandChannelSize int
}

type ThrottleConfig struct {
	MinIssueCountForThrottle int
	UpperLimit               time.Duration
}

type Config struct {
	SkipAlertsConsumer    bool
	ProcessInterval       time.Duration
	DefaultArchivingDelay time.Duration
	WebhookTimeout        time.Duration
	ReorderIssueLimit     int
	EncryptionKey         string
	CachePrefix           string
	IgnoreCacheReadErrors bool
	Location              *time.Location
	Slack                 common.SlackOptions
	ChannelManager        ChannelManagerConfig
	Throttle              ThrottleConfig
	DocsURL               string
	StatusDashboardURL    string
	LogsDashboardURL      string

	settings *SlackAdminConfig
}

func (c *Config) SetSettings(settings *SlackAdminConfig) error {
	if err := settings.Init(); err != nil {
		return err
	}

	c.settings = settings

	return nil
}

func (c *Config) GetSettings() *SlackAdminConfig {
	return c.settings
}

func (c *Config) UserIsGlobalAdmin(userID string) bool {
	if _, ok := c.settings.GlobalAdmins[userID]; ok {
		return true
	}

	return false
}

func (c *Config) UserIsChannelAdmin(ctx context.Context, channelID, userID string, userIsInGroup func(ctx context.Context, groupID, userID string) bool) bool {
	if userID == "" || channelID == "" {
		return false
	}

	settings := c.settings

	if _, ok := settings.GlobalAdmins[userID]; ok {
		return true
	}

	channelConfig, channelFound := settings.AlertChannels[channelID]

	if !channelFound {
		return false
	}

	if _, ok := channelConfig.Admins[userID]; ok {
		return true
	}

	if userIsInGroup != nil {
		for group := range channelConfig.AdminGroups {
			if userIsInGroup(ctx, group, userID) {
				return true
			}
		}
	}

	return false
}

func (c *Config) IsInfoChannel(channelID string) bool {
	settings := c.settings

	if _, ok := settings.InfoChannels[channelID]; ok {
		return true
	}

	return false
}

func (c *Config) GetInfoChannelConfig(channelID string) (*InfoChannelConfig, bool) {
	settings := c.settings

	if c, ok := settings.InfoChannels[channelID]; ok {
		return c, true
	}

	return nil, false
}

func (c *Config) OrderIssuesBySeverity(channelID string) bool {
	if channelID == "" {
		return true
	}

	settings := c.settings

	channelConfig, channelFound := settings.AlertChannels[channelID]

	if !channelFound {
		return true
	}

	return channelConfig.OrderIssuesBySeverity
}
