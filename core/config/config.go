package config

import (
	"context"
	"time"
)

type SlackConfig struct {
	BotToken                     string
	AppToken                     string
	Debug                        bool
	Concurrency                  int
	DryRun                       bool
	MaxAttemtsForRateLimitError  int
	MaxAttemptsForTransientError int
	MaxAttemptsForFatalError     int
	MaxRateLimitErrorWaitTime    time.Duration
	MaxTransientErrorWaitTime    time.Duration
	MaxFatalErrorWaitTime        time.Duration
	HTTPTimeout                  time.Duration

	settings *SlackAdminConfig
}

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
	Slack                 SlackConfig
	ChannelManager        ChannelManagerConfig
	Throttle              ThrottleConfig
	DocsURL               string
	StatusDashboardURL    string
	LogsDashboardURL      string
}

func (c *Config) SetSettings(settings *SlackAdminConfig) error {
	if err := settings.Init(); err != nil {
		return err
	}

	c.Slack.settings = settings

	return nil
}

func (c *Config) GetSettings() *SlackAdminConfig {
	return c.Slack.settings
}

func (s *SlackConfig) UserIsGlobalAdmin(userID string) bool {
	if _, ok := s.settings.GlobalAdmins[userID]; ok {
		return true
	}

	return false
}

func (s *SlackConfig) UserIsChannelAdmin(ctx context.Context, channelID, userID string, userIsInGroup func(ctx context.Context, groupID, userID string) bool) bool {
	if userID == "" || channelID == "" {
		return false
	}

	settings := s.settings

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

func (s *SlackConfig) IsInfoChannel(channelID string) bool {
	settings := s.settings

	if _, ok := settings.InfoChannels[channelID]; ok {
		return true
	}

	return false
}

func (s *SlackConfig) GetInfoChannelConfig(channelID string) (*InfoChannelConfig, bool) {
	settings := s.settings

	if c, ok := settings.InfoChannels[channelID]; ok {
		return c, true
	}

	return nil, false
}

func (s *SlackConfig) OrderIssuesBySeverity(channelID string) bool {
	if channelID == "" {
		return true
	}

	settings := s.settings

	channelConfig, channelFound := settings.AlertChannels[channelID]

	if !channelFound {
		return true
	}

	return channelConfig.OrderIssuesBySeverity
}
