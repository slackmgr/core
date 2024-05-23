package config

import (
	"context"
	"fmt"
)

type ChannelSettings struct {
	GlobalAdmins  []string                `json:"globalAdmins"  yaml:"globalAdmins"`
	AlertChannels []*AlertChannelSettings `json:"alertChannels" yaml:"alertChannels"`
	InfoChannels  []*InfoChannelSettings  `json:"infoChannels"  yaml:"infoChannels"`

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
