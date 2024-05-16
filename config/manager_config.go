package config

import (
	"context"
	"time"
)

type ChannelConfig struct {
	AlertChannelSize   int
	CommandChannelSize int
}

type ThrottleConfig struct {
	MinIssueCountForThrottle int
	UpperLimit               time.Duration
}

type ManagerConfig struct {
	SkipAlertsConsumer    bool
	ProcessInterval       time.Duration
	DefaultArchivingDelay time.Duration
	WebhookTimeout        time.Duration
	ReorderIssueLimit     int
	EncryptionKey         string
	CachePrefix           string
	IgnoreCacheReadErrors bool
	Location              *time.Location
	Slack                 SlackClientConfig
	ChannelManager        ChannelConfig
	Throttle              ThrottleConfig
	DocsURL               string
	StatusDashboardURL    string
	LogsDashboardURL      string

	adminSettings *AdminSettings
}

func NewDefaultConfig() *ManagerConfig {
	return &ManagerConfig{
		SkipAlertsConsumer:    false,
		ProcessInterval:       5 * time.Second,
		DefaultArchivingDelay: 12 * time.Hour,
		WebhookTimeout:        2 * time.Second,
		ReorderIssueLimit:     30,
		EncryptionKey:         "",
		CachePrefix:           "slack-manager",
		IgnoreCacheReadErrors: true,
		Location:              time.UTC,
		Slack:                 NewDefaultSlackClientConfig(),
		ChannelManager: ChannelConfig{
			AlertChannelSize:   100,
			CommandChannelSize: 100,
		},
		Throttle: ThrottleConfig{
			MinIssueCountForThrottle: 5,
			UpperLimit:               90 * time.Second,
		},
		DocsURL:            "",
		StatusDashboardURL: "",
		LogsDashboardURL:   "",
	}
}

func (c *ManagerConfig) UpdateAdminSettings(settings *AdminSettings) {
	c.adminSettings = settings
}

func (c *ManagerConfig) AdminSettings() *AdminSettings {
	return c.adminSettings
}

func (c *ManagerConfig) UserIsGlobalAdmin(userID string) bool {
	if _, ok := c.adminSettings.GlobalAdmins[userID]; ok {
		return true
	}

	return false
}

func (c *ManagerConfig) UserIsChannelAdmin(ctx context.Context, channelID, userID string, userIsInGroup func(ctx context.Context, groupID, userID string) bool) bool {
	if userID == "" || channelID == "" {
		return false
	}

	settings := c.adminSettings

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

func (c *ManagerConfig) IsInfoChannel(channelID string) bool {
	settings := c.adminSettings

	if _, ok := settings.InfoChannels[channelID]; ok {
		return true
	}

	return false
}

func (c *ManagerConfig) GetInfoChannelConfig(channelID string) (*InfoChannelConfig, bool) {
	settings := c.adminSettings

	if c, ok := settings.InfoChannels[channelID]; ok {
		return c, true
	}

	return nil, false
}

func (c *ManagerConfig) OrderIssuesBySeverity(channelID string) bool {
	if channelID == "" {
		return true
	}

	settings := c.adminSettings

	channelConfig, channelFound := settings.AlertChannels[channelID]

	if !channelFound {
		return true
	}

	return channelConfig.OrderIssuesBySeverity
}
