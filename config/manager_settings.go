package config

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	common "github.com/peteraglen/slack-manager-common"
)

const (
	DefaultPostIconEmoji                  = ":female-detective:"
	DefaultPostUsername                   = "Slack Manager"
	DefaultAlertSeverity                  = common.AlertError
	DefaultIssueArchivingDelaySeconds     = 12 * 3600 // 12 hours
	MinIssueArchivingDelaySeconds         = 60
	MaxIssueArchivingDelaySeconds         = 30 * 24 * 3600 // 30 days
	DefaultIssueReorderingLimit           = 30
	MinIssueReorderingLimit               = 5
	MaxIssueReorderingLimit               = 100
	DefaultIssueProcessingIntervalSeconds = 10
	MinIssueProcessingIntervalSeconds     = 3
	MaxIssueProcessingIntervalSeconds     = 600
	DefaultIssueTerminateEmoji            = ":firecracker:"
	DefaultIssueResolveEmoji              = ":white_check_mark:"
	DefaultIssueInvestigateEmoji          = ":eyes:"
	DefaultIssueMuteEmoji                 = ":mask:"
	DefaultIssueShowOptionButtonsEmoji    = ":information_source:"
	DefaultMinIssueCountForThrottle       = 5
	DefaultMaxThrottleDurationSeconds     = 90 // 1.5 minutes
	DefaultAppFriendlyName                = "Slack Manager"
	DefaultPanicEmoji                     = ":scream:"
	DefaultErrorEmoji                     = ":x:"
	DefaultWarningEmoji                   = ":warning:"
	DefaultInfoEmoji                      = ":information_source:"
	DefaultMutePanicEmoji                 = ":no_bell:"
	DefaultMuteErrorEmoji                 = ":no_bell:"
	DefaultMuteWarningEmoji               = ":no_bell:"
	DefaultInconclusiveEmoji              = ":grey_question:"
	DefaultResolvedEmoji                  = ":white_check_mark:"
)

// ManagerSettings contains the settings for the Slack Manager system,
// both global settings and settings for individual channels.
// Nothing in this struct is mandatory, but some settings have default values (see constants above).
type ManagerSettings struct {
	// AppFriendlyName is the friendly name of the Slack Manager app.
	// It is used in messages and alerts. If not set, DefaultAppFriendlyName is used.
	AppFriendlyName string `json:"appFriendlyName" yaml:"appFriendlyName"`

	// GlobalAdmins is a list of Slack user IDs that have global Slack Manager admin rights,
	// including using emojis to handle issues, and invoking all alert webhooks.
	// Not to be confused with the Slack workspace's global admins!
	// This list should be kept as short as possible.
	GlobalAdmins []string `json:"globalAdmins" yaml:"globalAdmins"`

	// DefaultPostIconEmoji is the Slack post emoji to use for alerts where no icon emoji is specified.
	// The value must be on the format ":emoji:".
	// https://api.slack.com/methods/chat.postMessage#arg_icon_emoji
	DefaultPostIconEmoji string `json:"defaultPostIconEmoji" yaml:"defaultPostIconEmoji"`

	// DefaultPostUsername is the Slack post username to use for alerts where no username is specified.
	// https://api.slack.com/methods/chat.postMessage#arg_username
	DefaultPostUsername string `json:"defaultPostUsername" yaml:"defaultPostUsername"`

	// DefaultAlertSeverity is the severity to use for alerts where no severity is specified.
	// The value must be one of allowedDefaultAlertSeverities.
	DefaultAlertSeverity common.AlertSeverity `json:"defaultAlertSeverity" yaml:"defaultAlertSeverity"`

	// DefaultIssueArchivingDelaySeconds is the archiving delay (in seconds) to use for alerts where no archiving delay is specified.
	// The value must be between MinIssueArchivingDelaySeconds and MaxIssueArchivingDelaySeconds.
	DefaultIssueArchivingDelaySeconds int `json:"defaultIssueArchivingDelaySeconds" yaml:"defaultIssueArchivingDelaySeconds"`

	// DisableIssueReordering can be used to turn off the standard reordering of issues by severity (in each alert channel).
	// When true, issues are displayed in the order they were created, regardless of severity.
	// When false, issues are automatically reordered and displayed in order of severity.
	// This setting can be overridden for individual alert channels.
	DisableIssueReordering bool `json:"disableIssueReordering" yaml:"disableIssueReordering"`

	// IssueReorderingLimit is the maximum number of open issues (in a single channel) that can be reordered by severity.
	// This setting is used to prevent reordering from becoming too slow when there are many open issues, and to avoid hitting Slack rate limits.
	// If the number of open issues exceeds this limit, issues are displayed in the order they were created, regardless of severity.
	// When the number of open issues drops below this limit, issues are again reordered by severity.
	// The value must be between MinIssueReorderingLimit and MaxIssueReorderingLimit, and may be overridden for individual alert channels.
	IssueReorderingLimit int `json:"issueReorderingLimit" yaml:"issueReorderingLimit"`

	// IssueProcessingIntervalSeconds is the interval (in seconds) between issue processing runs in each channel.
	// An issue processing run includes reordering issues by severity, escalating issues, and archiving resolved issues.
	// The value must be between MinIssueProcessingIntervalSeconds and MaxIssueProcessingIntervalSeconds, and may be overridden for individual alert channels.
	IssueProcessingIntervalSeconds int `json:"issueProcessingIntervalSeconds" yaml:"issueProcessingIntervalSeconds"`

	// IssueReactions contains the settings for Slack post reactions.
	// These settings define which specific emojis are used to handle issues.
	IssueReactions *IssueReactionSettings `json:"issueReactions" yaml:"issueReactions"`

	// IssueStatus contains the settings for Slack post status emojis.
	// These settings define which specific emojis are used to indicate the status of an individual issue.
	IssueStatus *IssueStatusSettings `json:"issueStatus" yaml:"issueStatus"`

	// MinIssueCountForThrottle is the minimum number of open issues that must be present in a channel, before throttling is considered.
	// Throttling is a mechanism to prevent too many Slack post updates, in a short period of time.
	MinIssueCountForThrottle int `json:"minIssueCountForThrottle" yaml:"minIssueCountForThrottle"`

	// MaxThrottleDurationSeconds is the maximum duration (in seconds) that a particular Slack post can be throttled.
	// After this duration, the Slack post is again allowed to be updated.
	MaxThrottleDurationSeconds int `json:"maxThrottleDurationSeconds" yaml:"maxThrottleDurationSeconds"`

	// AlwaysShowOptionButtons is a flag that determines whether the option buttons should always be shown in the Slack post.
	// If set to true, the option buttons are always shown.
	// If set to false, the option buttons are only shown when the user enabled them by reacting with the appropriate emoji.
	AlwaysShowOptionButtons bool `json:"alwaysShowOptionButtons" yaml:"alwaysShowOptionButtons"`

	// ShowIssueCorrelationIDInSlackPost is a flag that determines whether the issue correlation ID should be shown in the Slack post.
	// If set to true, the issue correlation ID is shown in the post footer.
	// If set to false, the issue correlation ID is not shown (but can be found under Issue Details).
	ShowIssueCorrelationIDInSlackPost bool `json:"showIssueCorrelationIDInSlackPost" yaml:"showIssueCorrelationIDInSlackPost"`

	// DocsURL is the URL to the Slack Manager documentation (if any).
	DocsURL string `json:"docsUrl" yaml:"docsUrl"`

	// AlertChannels is an optional list of settings for individual alert channels.
	// Each alert channel can potentially have its own list of admins, and some settings that affect how alerts are handled.
	// If no settings are specified for a channel, the default settings are used.
	AlertChannels []*AlertChannelSettings `json:"alertChannels" yaml:"alertChannels"`

	// InfoChannels is a list of channels defined as "info channels".
	// These channels are used to display information about the Slack Manager system, but cannot be used to handle alerts.
	InfoChannels []*InfoChannelSettings `json:"infoChannels" yaml:"infoChannels"`

	globalAdmins     map[string]struct{}
	alertChannels    map[string]*AlertChannelSettings
	infoChannels     map[string]*InfoChannelSettings
	issueReactionMap map[string]IssueReaction
	initialized      bool
}

// IssueReactionSettings contains the settings for Slack post reactions.
type IssueReactionSettings struct {
	// TerminateEmojis is a list of emojis used to indicate that an issue should be terminated.
	TerminateEmojis []string `json:"terminateEmojis" yaml:"terminateEmojis"`

	// ResolveEmojis is a list of emojis used to indicate that an issue should be resolved.
	ResolveEmojis []string `json:"resolveEmojis" yaml:"resolveEmojis"`

	// InvestigateEmojis is a list of emojis used to indicate that an issue is being investigated by the current user.
	InvestigateEmojis []string `json:"investigateEmojis" yaml:"investigateEmojis"`

	// MuteEmojis is a list of emojis used to indicate that an issue should be muted.
	MuteEmojis []string `json:"muteEmojis" yaml:"muteEmojis"`

	// MoveIssueEmojis is a list of emojis used to indicate that an issue should be moved to another channel.
	ShowOptionButtonsEmojis []string `json:"showOptionButtonsEmojis" yaml:"showOptionButtonsEmojis"`
}

// IssueStatusSettings contains the settings for Slack post status emojis.
type IssueStatusSettings struct {
	// PanicEmoji is the emoji used to indicate a panic status.
	PanicEmoji string `json:"panicEmoji" yaml:"panicEmoji"`

	// ErrorEmoji is the emoji used to indicate an error status.
	ErrorEmoji string `json:"errorEmoji" yaml:"errorEmoji"`

	// WarningEmoji is the emoji used to indicate a warning status.
	WarningEmoji string `json:"warningEmoji" yaml:"warningEmoji"`

	// InfoEmoji is the emoji used to indicate an info status.
	InfoEmoji string `json:"infoEmoji" yaml:"infoEmoji"`

	// MutePanicEmoji is the emoji used to indicate a muted panic status.
	MutePanicEmoji string `json:"mutePanicEmoji" yaml:"mutePanicEmoji"`

	// MuteErrorEmoji is the emoji used to indicate a muted error status.
	MuteErrorEmoji string `json:"muteErrorEmoji" yaml:"muteErrorEmoji"`

	// MuteWarningEmoji is the emoji used to indicate a muted warning status.
	MuteWarningEmoji string `json:"muteWarningEmoji" yaml:"muteWarningEmoji"`

	// InconclusiveEmoji is the emoji used to indicate an inconclusive status, i.e. an issue that
	// has been resolved as inconclusive.
	InconclusiveEmoji string `json:"unresolvedEmoji" yaml:"unresolvedEmoji"`

	// ResolvedEmoji is the emoji used to indicate a resolved status, i.e. an issue that has been resolved as OK.
	ResolvedEmoji string `json:"resolvedEmoji" yaml:"resolvedEmoji"`
}

// AlertChannelSettings contains the settings for an individual alert channel.
type AlertChannelSettings struct {
	ID                             string   `json:"id"                             yaml:"id"`
	AdminUsers                     []string `json:"adminUsers"                     yaml:"adminUsers"`
	AdminGroups                    []string `json:"adminGroups"                    yaml:"adminGroups"`
	DisableIssueReordering         bool     `json:"disableIssueReordering"         yaml:"disableIssueReordering"`
	IssueReorderingLimit           int      `json:"issueReorderingLimit"           yaml:"issueReorderingLimit"`
	IssueProcessingIntervalSeconds int      `json:"issueProcessingIntervalSeconds" yaml:"issueProcessingIntervalSeconds"`

	adminUsers  map[string]struct{}
	adminGroups map[string]struct{}
}

// InfoChannelSettings contains the settings for an individual info channel.
type InfoChannelSettings struct {
	ID           string `json:"channelId"    yaml:"channelId"`
	TemplatePath string `json:"templatePath" yaml:"templatePath"`
}

// InitAndValidate initializes the settings and validates them.
// Missing or empty settings are set to their default values (using the constants above).
// An error is returned if any settings are invalid.
// This function is called from inside the manager, so it is not necessary to call it from the outside.
func (s *ManagerSettings) InitAndValidate() error {
	if s.initialized {
		return nil
	}

	s.globalAdmins = make(map[string]struct{})
	s.alertChannels = make(map[string]*AlertChannelSettings)
	s.infoChannels = make(map[string]*InfoChannelSettings)
	s.issueReactionMap = make(map[string]IssueReaction)

	if s.AppFriendlyName == "" {
		s.AppFriendlyName = DefaultAppFriendlyName
	}

	for i, userID := range s.GlobalAdmins {
		userID = strings.TrimSpace(userID)

		if userID == "" {
			return fmt.Errorf("globalAdmins[%d] cannot be empty", i)
		}

		s.globalAdmins[userID] = struct{}{}
	}

	if s.DefaultPostIconEmoji != "" && !strings.HasPrefix(s.DefaultPostIconEmoji, ":") && !strings.HasSuffix(s.DefaultPostIconEmoji, ":") {
		s.DefaultPostIconEmoji = ":" + s.DefaultPostIconEmoji + ":"
	}

	if s.DefaultPostIconEmoji == "" {
		s.DefaultPostIconEmoji = DefaultPostIconEmoji
	} else if !common.IconRegex.MatchString(s.DefaultPostIconEmoji) {
		return errors.New("default icon emoji must be on the format \":emoji:\"")
	}

	if s.DefaultPostUsername == "" {
		s.DefaultPostUsername = DefaultPostUsername
	}

	allowedDefaultAlertSeverities := map[common.AlertSeverity]struct{}{
		common.AlertPanic:   {},
		common.AlertError:   {},
		common.AlertWarning: {},
	}

	if s.DefaultAlertSeverity == "" {
		s.DefaultAlertSeverity = DefaultAlertSeverity
	} else if _, ok := allowedDefaultAlertSeverities[s.DefaultAlertSeverity]; !ok {
		return fmt.Errorf("default alert severity must be one of %v", allowedDefaultAlertSeverities)
	}

	if s.DefaultIssueArchivingDelaySeconds <= 0 {
		s.DefaultIssueArchivingDelaySeconds = DefaultIssueArchivingDelaySeconds
	} else if s.DefaultIssueArchivingDelaySeconds < MinIssueArchivingDelaySeconds || s.DefaultIssueArchivingDelaySeconds > MaxIssueArchivingDelaySeconds {
		return errors.New("default archiving delay must be between MinIssueArchivingDelaySeconds and MaxIssueArchivingDelaySeconds")
	}

	if s.IssueReorderingLimit <= 0 {
		s.IssueReorderingLimit = DefaultIssueReorderingLimit
	} else if s.IssueReorderingLimit < MinIssueReorderingLimit || s.IssueReorderingLimit > MaxIssueReorderingLimit {
		return errors.New("issue reordering limit must be between MinIssueReorderingLimit and MaxIssueReorderingLimit (use DisableIssueReordering to turn off reordering)")
	}

	if s.IssueProcessingIntervalSeconds <= 0 {
		s.IssueProcessingIntervalSeconds = DefaultIssueProcessingIntervalSeconds
	} else if s.IssueProcessingIntervalSeconds < MinIssueProcessingIntervalSeconds || s.IssueProcessingIntervalSeconds > MaxIssueProcessingIntervalSeconds {
		return errors.New("issue processing interval must be between MinIssueProcessingIntervalSeconds and MaxIssueProcessingIntervalSeconds")
	}

	if s.IssueReactions == nil {
		s.IssueReactions = &IssueReactionSettings{}
	}

	s.IssueReactions.TerminateEmojis = initReactionEmojiSlice(s.IssueReactions.TerminateEmojis, DefaultIssueTerminateEmoji)
	s.IssueReactions.ResolveEmojis = initReactionEmojiSlice(s.IssueReactions.ResolveEmojis, DefaultIssueResolveEmoji)
	s.IssueReactions.InvestigateEmojis = initReactionEmojiSlice(s.IssueReactions.InvestigateEmojis, DefaultIssueInvestigateEmoji)
	s.IssueReactions.MuteEmojis = initReactionEmojiSlice(s.IssueReactions.MuteEmojis, DefaultIssueMuteEmoji)
	s.IssueReactions.ShowOptionButtonsEmojis = initReactionEmojiSlice(s.IssueReactions.ShowOptionButtonsEmojis, DefaultIssueShowOptionButtonsEmoji)

	if s.IssueStatus == nil {
		s.IssueStatus = &IssueStatusSettings{}
	}

	s.IssueStatus.PanicEmoji = initStatusEmoji(s.IssueStatus.PanicEmoji, DefaultPanicEmoji)
	s.IssueStatus.ErrorEmoji = initStatusEmoji(s.IssueStatus.ErrorEmoji, DefaultErrorEmoji)
	s.IssueStatus.WarningEmoji = initStatusEmoji(s.IssueStatus.WarningEmoji, DefaultWarningEmoji)
	s.IssueStatus.InfoEmoji = initStatusEmoji(s.IssueStatus.InfoEmoji, DefaultInfoEmoji)
	s.IssueStatus.MutePanicEmoji = initStatusEmoji(s.IssueStatus.MutePanicEmoji, DefaultMutePanicEmoji)
	s.IssueStatus.MuteErrorEmoji = initStatusEmoji(s.IssueStatus.MuteErrorEmoji, DefaultMuteErrorEmoji)
	s.IssueStatus.MuteWarningEmoji = initStatusEmoji(s.IssueStatus.MuteWarningEmoji, DefaultMuteWarningEmoji)
	s.IssueStatus.InconclusiveEmoji = initStatusEmoji(s.IssueStatus.InconclusiveEmoji, DefaultInconclusiveEmoji)
	s.IssueStatus.ResolvedEmoji = initStatusEmoji(s.IssueStatus.ResolvedEmoji, DefaultResolvedEmoji)

	if s.MinIssueCountForThrottle <= 0 {
		s.MinIssueCountForThrottle = DefaultMinIssueCountForThrottle
	}

	if s.MaxThrottleDurationSeconds <= 0 {
		s.MaxThrottleDurationSeconds = DefaultMaxThrottleDurationSeconds
	}

	for i, a := range s.AlertChannels {
		a.ID = strings.TrimSpace(a.ID)

		if a.ID == "" {
			return fmt.Errorf("alertChannels[%d].id cannot be empty", i)
		}

		a.adminUsers = make(map[string]struct{})
		a.adminGroups = make(map[string]struct{})

		for j, userID := range a.AdminUsers {
			userID = strings.TrimSpace(userID)

			if userID == "" {
				return fmt.Errorf("alertChannels[%d].adminUsers[%d] cannot be empty", i, j)
			}

			a.adminUsers[userID] = struct{}{}
		}

		for j, groupID := range a.AdminGroups {
			groupID = strings.TrimSpace(groupID)

			if groupID == "" {
				return fmt.Errorf("alertChannels[%d].adminGroups[%d] cannot be empty", i, j)
			}

			a.adminGroups[groupID] = struct{}{}
		}

		if a.IssueReorderingLimit <= 0 {
			a.IssueReorderingLimit = s.IssueReorderingLimit
		} else if a.IssueReorderingLimit < MinIssueReorderingLimit || a.IssueReorderingLimit > MaxIssueReorderingLimit {
			return fmt.Errorf("alertChannels[%d].issueReorderingLimit must be between MinIssueReorderingLimit and MaxIssueReorderingLimit (use DisableIssueReordering to turn off reordering)", i)
		}

		if a.IssueProcessingIntervalSeconds <= 0 {
			a.IssueProcessingIntervalSeconds = s.IssueProcessingIntervalSeconds
		} else if a.IssueProcessingIntervalSeconds < MinIssueProcessingIntervalSeconds || a.IssueProcessingIntervalSeconds > MaxIssueProcessingIntervalSeconds {
			return fmt.Errorf("alertChannels[%d].issueProcessingIntervalSeconds must be between MinIssueProcessingIntervalSeconds and MaxIssueProcessingIntervalSeconds", i)
		}

		s.alertChannels[a.ID] = a
	}

	for i, a := range s.InfoChannels {
		a.ID = strings.TrimSpace(a.ID)
		a.TemplatePath = strings.TrimSpace(a.TemplatePath)

		if a.ID == "" {
			return fmt.Errorf("infoChannels[%d].id cannot be empty", i)
		}

		if a.TemplatePath == "" {
			return fmt.Errorf("infoChannels[%d].templatePath cannot be empty", i)
		}

		s.infoChannels[a.ID] = a
	}

	s.initialized = true

	return nil
}

func initReactionEmojiSlice(emojis []string, defaultEmoji string) []string {
	result := []string{}

	for _, emoji := range emojis {
		emoji = strings.TrimSpace(emoji)

		if !common.IconRegex.MatchString(emoji) {
			continue
		}

		if emoji != "" {
			result = append(result, emoji)
		}
	}

	if len(result) == 0 {
		result = []string{defaultEmoji}
	}

	// When comparing with reaction events, we need to remove the colons from the emojis.
	for i, emoji := range result {
		result[i] = strings.Trim(emoji, ":")
	}

	return result
}

func initStatusEmoji(emoji, defaultEmoji string) string {
	emoji = strings.TrimSpace(emoji)

	if !common.IconRegex.MatchString(emoji) {
		return defaultEmoji
	}

	return emoji
}

func (s *ManagerSettings) UserIsGlobalAdmin(userID string) bool {
	if _, ok := s.globalAdmins[userID]; ok {
		return true
	}

	return false
}

func (s *ManagerSettings) UserIsChannelAdmin(ctx context.Context, channelID, userID string, userIsInGroup func(ctx context.Context, groupID, userID string) bool) bool {
	if userID == "" || channelID == "" {
		return false
	}

	if _, ok := s.globalAdmins[userID]; ok {
		return true
	}

	channelConfig, channelFound := s.alertChannels[channelID]

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

func (s *ManagerSettings) IsInfoChannel(channelID string) bool {
	if _, ok := s.infoChannels[channelID]; ok {
		return true
	}

	return false
}

func (s *ManagerSettings) GetInfoChannelConfig(channelID string) (*InfoChannelSettings, bool) {
	if c, ok := s.infoChannels[channelID]; ok {
		return c, true
	}

	return nil, false
}

func (s *ManagerSettings) OrderIssuesBySeverity(channelID string, openIssueCount int) bool {
	if a, ok := s.alertChannels[channelID]; ok {
		return !a.DisableIssueReordering && openIssueCount <= a.IssueReorderingLimit
	}

	return !s.DisableIssueReordering && openIssueCount <= s.IssueReorderingLimit
}

func (s *ManagerSettings) IssueProcessingInterval(channelID string) time.Duration {
	if a, ok := s.alertChannels[channelID]; ok {
		return time.Duration(a.IssueProcessingIntervalSeconds) * time.Second
	}

	return time.Duration(s.IssueProcessingIntervalSeconds) * time.Second
}

func (s *ManagerSettings) MapSlackPostReaction(reaction string) IssueReaction {
	if reaction == "" {
		return ""
	}

	if val, ok := s.issueReactionMap[reaction]; ok {
		return val
	}

	var r IssueReaction

	switch {
	case containsString(s.IssueReactions.TerminateEmojis, reaction):
		r = IssueReactionTerminate
	case containsString(s.IssueReactions.ResolveEmojis, reaction):
		r = IssueReactionResolve
	case containsString(s.IssueReactions.InvestigateEmojis, reaction):
		r = IssueReactionInvestigate
	case containsString(s.IssueReactions.MuteEmojis, reaction):
		r = IssueReactionMute
	case containsString(s.IssueReactions.ShowOptionButtonsEmojis, reaction):
		r = IssueReactionShowOptionButtons
	default:
		r = ""
	}

	s.issueReactionMap[reaction] = r

	return r
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}

	return false
}
