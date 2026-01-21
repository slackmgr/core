package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	common "github.com/peteraglen/slack-manager-common"
)

// Default values for ManagerSettings general fields.
// These defaults are applied during InitAndValidate() when fields are empty or zero.
const (
	// DefaultPostIconEmoji is the default emoji shown as the bot's icon in Slack posts.
	// This appears next to the bot's username in the message header. Must be in :emoji: format.
	// Can be overridden per-alert via the alert payload's iconEmoji field.
	DefaultPostIconEmoji = ":female-detective:"

	// DefaultPostUsername is the default display name for the bot in Slack posts.
	// This appears as the sender name in the message header. Can be overridden per-alert
	// via the alert payload's username field.
	DefaultPostUsername = "Slack Manager"

	// DefaultAlertSeverity is the severity assigned to alerts that don't specify one.
	// Valid severities are: panic (highest), error, warning, info (lowest).
	// Severity affects issue ordering when reordering is enabled - higher severity
	// issues appear at the bottom of the channel for visibility.
	DefaultAlertSeverity = common.AlertError

	// DefaultAppFriendlyName is the application name shown in Slack messages and modals.
	// Used in greeting messages, help text, and error dialogs to identify the bot.
	DefaultAppFriendlyName = "Slack Manager"
)

// Issue archiving delay constants control when resolved issues are automatically archived.
//
// When an issue is resolved (via emoji reaction or auto-resolve), it remains in an "open"
// state for the archiving delay period before being marked as archived. This gives team
// members time to re-open the issue if needed. Archived issues retain their Slack posts
// but are marked as archived in the database to prevent re-opening via new alerts.
const (
	// DefaultIssueArchivingDelaySeconds is the default delay before archiving resolved issues.
	// 12 hours provides a reasonable window for team members across time zones to re-open
	// issues if needed before they are permanently archived.
	DefaultIssueArchivingDelaySeconds = 12 * 3600 // 12 hours

	// MinIssueArchivingDelaySeconds is the minimum allowed archiving delay.
	// At least 1 minute is required to give users a chance to re-open issues via new
	// alerts before the issue is permanently archived.
	MinIssueArchivingDelaySeconds = 60 // 1 minute

	// MaxIssueArchivingDelaySeconds is the maximum allowed archiving delay.
	// 30 days prevents issues from remaining in a "resolved but re-openable" state
	// indefinitely. Longer delays provide diminishing value as the likelihood of
	// needing to re-open an old resolved issue decreases over time.
	MaxIssueArchivingDelaySeconds = 30 * 24 * 3600 // 30 days
)

// Issue reordering constants control automatic severity-based ordering of issues in channels.
//
// When reordering is enabled, issues are automatically arranged so higher-severity issues
// (panic, error) appear at the bottom of the channel where they are most visible. This is
// achieved by deleting and reposting Slack messages, which consumes API quota.
//
// Reordering is automatically disabled when the number of open issues exceeds the limit,
// preventing excessive API calls during incident storms. Once the issue count drops below
// the limit, reordering resumes automatically.
const (
	// DefaultIssueReorderingLimit is the default maximum number of open issues in a channel
	// before reordering is automatically disabled. With 30 issues, reordering operations
	// remain responsive and API usage stays reasonable.
	DefaultIssueReorderingLimit = 30

	// MinIssueReorderingLimit is the minimum allowed reordering limit.
	// Below 5 issues, reordering provides minimal visual benefit since users can easily
	// scan a small number of messages regardless of order.
	MinIssueReorderingLimit = 5

	// MaxIssueReorderingLimit is the maximum allowed reordering limit.
	// Above 100 issues, the API calls required for reordering become excessive and may
	// trigger Slack rate limits, causing delays in issue processing.
	MaxIssueReorderingLimit = 100
)

// Issue processing interval constants control the frequency of background issue maintenance.
//
// Each channel manager runs a periodic processing loop that handles:
//   - Reordering issues by severity (when enabled and issue count is below limit)
//   - Auto-resolving issues that have been quiet for their configured auto-resolve period
//   - Archiving resolved issues after their archiving delay has elapsed
//   - Escalating issues that have triggered escalation rules
//
// The interval can be configured globally or per-channel via AlertChannelSettings.
const (
	// DefaultIssueProcessingIntervalSeconds is the default interval between processing runs.
	// 10 seconds provides responsive issue management while limiting API usage.
	DefaultIssueProcessingIntervalSeconds = 10

	// MinIssueProcessingIntervalSeconds is the minimum allowed processing interval.
	// Intervals shorter than 3 seconds may cause excessive Slack API calls during
	// high-activity periods and risk hitting rate limits.
	MinIssueProcessingIntervalSeconds = 3

	// MaxIssueProcessingIntervalSeconds is the maximum allowed processing interval.
	// Intervals longer than 10 minutes cause unacceptable delays in auto-resolution,
	// archiving, and escalation, making the system feel unresponsive.
	MaxIssueProcessingIntervalSeconds = 600 // 10 minutes
)

// Throttle constants control rate limiting of Slack post updates during high-activity periods.
//
// Throttling prevents excessive API calls when many issues are being updated simultaneously,
// which can occur during incident storms or when a monitoring system sends many alerts at once.
// The throttle algorithm delays updates to individual issue posts based on:
//   - The number of open issues in the channel (throttling only activates above a threshold)
//   - The number of alerts already received for each issue (more alerts = longer delays)
//   - Whether the alert text has changed (text changes reduce the delay)
//
// This allows critical first alerts to post immediately while batching rapid updates to
// the same issue, reducing visual noise and API usage.
const (
	// DefaultMinIssueCountForThrottle is the minimum number of open issues in a channel
	// before throttling is considered. With fewer than 5 issues, API usage is manageable
	// and immediate updates provide better user experience.
	DefaultMinIssueCountForThrottle = 5

	// MinMinIssueCountForThrottle is the minimum allowed threshold for throttling.
	// Setting this to 1 means throttling can activate with just one open issue, which
	// may be useful for channels with very strict API limits.
	MinMinIssueCountForThrottle = 1

	// MaxMinIssueCountForThrottle is the maximum allowed threshold for throttling.
	// Setting this above 100 effectively disables throttling in most scenarios, which
	// may be appropriate for low-volume channels with generous API limits.
	MaxMinIssueCountForThrottle = 100

	// DefaultMaxThrottleDurationSeconds is the maximum delay between updates to a single issue.
	// 90 seconds provides meaningful batching during storms while ensuring updates are
	// eventually delivered within a reasonable timeframe.
	DefaultMaxThrottleDurationSeconds = 90

	// MinMaxThrottleDurationSeconds is the minimum allowed maximum throttle duration.
	// At least 1 second is required for throttling to have any effect.
	MinMaxThrottleDurationSeconds = 1

	// MaxMaxThrottleDurationSeconds is the maximum allowed throttle duration.
	// Delays longer than 10 minutes make the system feel unresponsive and may cause
	// users to miss important updates about ongoing issues.
	MaxMaxThrottleDurationSeconds = 600 // 10 minutes
)

// Default emoji constants for issue reaction commands.
//
// Users interact with issues by adding emoji reactions to Slack posts. Each reaction type
// triggers a specific action. Multiple emojis can be configured for each action via
// IssueReactionSettings, allowing teams to use their preferred emoji while maintaining
// consistent behavior.
//
// Actions requiring channel admin permissions: terminate, resolve, mute.
// Actions available to all users: investigate, show option buttons.
const (
	// DefaultIssueTerminateEmoji triggers immediate issue termination without resolution.
	// Use for false positives or issues that should be dismissed without tracking.
	DefaultIssueTerminateEmoji = ":firecracker:"

	// DefaultIssueResolveEmoji marks an issue as resolved. The issue remains visible
	// for the configured archiving delay before being archived.
	DefaultIssueResolveEmoji = ":white_check_mark:"

	// DefaultIssueInvestigateEmoji marks the user as investigating the issue.
	// The investigator's name appears in the issue post, signaling to others that
	// someone is actively working on it.
	DefaultIssueInvestigateEmoji = ":eyes:"

	// DefaultIssueMuteEmoji mutes an issue, preventing escalation notifications while
	// keeping the issue visible. Useful for known issues being monitored.
	DefaultIssueMuteEmoji = ":mask:"

	// DefaultIssueShowOptionButtonsEmoji reveals the interactive option buttons on the
	// issue post, providing quick access to actions without using emoji reactions.
	DefaultIssueShowOptionButtonsEmoji = ":information_source:"
)

// Default emoji constants for issue status indicators.
//
// Status emojis appear in issue posts to visually indicate the current severity and state.
// They help users quickly scan a channel to identify high-priority issues. Muted emojis
// indicate issues that are being tracked but won't trigger escalations.
const (
	// DefaultPanicEmoji indicates a panic-level issue (highest severity).
	DefaultPanicEmoji = ":scream:"

	// DefaultErrorEmoji indicates an error-level issue.
	DefaultErrorEmoji = ":x:"

	// DefaultWarningEmoji indicates a warning-level issue.
	DefaultWarningEmoji = ":warning:"

	// DefaultInfoEmoji indicates an info-level issue (lowest severity).
	DefaultInfoEmoji = ":information_source:"

	// DefaultMutePanicEmoji indicates a muted panic-level issue.
	DefaultMutePanicEmoji = ":no_bell:"

	// DefaultMuteErrorEmoji indicates a muted error-level issue.
	DefaultMuteErrorEmoji = ":no_bell:"

	// DefaultMuteWarningEmoji indicates a muted warning-level issue.
	DefaultMuteWarningEmoji = ":no_bell:"

	// DefaultInconclusiveEmoji indicates an issue resolved as inconclusive
	// (neither confirmed fixed nor confirmed false positive).
	DefaultInconclusiveEmoji = ":grey_question:"

	// DefaultResolvedEmoji indicates a successfully resolved issue.
	DefaultResolvedEmoji = ":white_check_mark:"
)

// ManagerSettings contains runtime configuration for the Slack Manager service.
//
// These settings control how the Manager processes alerts, displays issues in Slack,
// handles user interactions, and manages issue lifecycle. Unlike ManagerConfig (which
// requires a restart to change), ManagerSettings can be updated at runtime.
//
// # Structure
//
// Settings are organized into three levels:
//   - Global settings: Apply to all channels unless overridden
//   - AlertChannels: Per-channel overrides for processing behavior and admin permissions
//   - InfoChannels: Channels designated for system information (cannot receive alerts)
//
// # Defaults
//
// All fields are optional. Missing or zero values are replaced with sensible defaults
// during InitAndValidate(). See the Default* constants for specific default values.
//
// # Immutability
//
// Settings passed to the Manager are cloned internally to prevent external modifications
// from affecting the running system. Once settings are passed to the Manager, any subsequent
// changes to the original struct will have no effect. To update settings, create a new
// ManagerSettings instance and pass it to the Manager's update method.
//
// # Admin Permissions
//
// Admin permissions are checked for destructive actions (resolve, terminate, mute).
// A user is considered an admin if they are:
//   - Listed in GlobalAdmins (has admin rights in all channels)
//   - Listed in a channel's AdminUsers (has admin rights in that channel only)
//   - A member of a group listed in a channel's AdminGroups
type ManagerSettings struct {
	// AppFriendlyName is the application name displayed in Slack messages, modals, and
	// greeting messages. Used to identify the bot in user-facing text.
	// Default: "Slack Manager"
	AppFriendlyName string `json:"appFriendlyName" yaml:"appFriendlyName"`

	// GlobalAdmins is a list of Slack user IDs (e.g., "U1234567890") with admin permissions
	// in all channels. Global admins can resolve, terminate, and mute issues in any channel.
	//
	// Use sparingly - prefer channel-specific admins via AlertChannelSettings.AdminUsers
	// for better access control. Global admins are typically reserved for on-call leads
	// or platform administrators who need cross-channel access.
	GlobalAdmins []string `json:"globalAdmins" yaml:"globalAdmins"`

	// DefaultPostIconEmoji is the emoji displayed as the bot's avatar in Slack posts
	// when no per-alert icon is specified. Must be in :emoji: format (colons are added
	// automatically if missing during validation).
	// Default: ":female-detective:"
	DefaultPostIconEmoji string `json:"defaultPostIconEmoji" yaml:"defaultPostIconEmoji"`

	// DefaultPostUsername is the display name shown for the bot in Slack posts when no
	// per-alert username is specified. This appears as the sender name in message headers.
	// Default: "Slack Manager"
	DefaultPostUsername string `json:"defaultPostUsername" yaml:"defaultPostUsername"`

	// DefaultAlertSeverity is the severity level assigned to alerts that don't specify one.
	// Severity determines issue ordering (when reordering is enabled) and visual indicators.
	// Valid values: "panic" (highest), "error", "warning". Info-level alerts are not allowed
	// as the default since they typically don't require immediate attention.
	// Default: "error"
	DefaultAlertSeverity common.AlertSeverity `json:"defaultAlertSeverity" yaml:"defaultAlertSeverity"`

	// DefaultIssueArchivingDelaySeconds is the time (in seconds) after resolution before
	// an issue is marked as archived. During this period, new alerts can still re-open the
	// issue. Once archived, the Slack post remains but the issue cannot be re-opened.
	// Can be overridden per-alert via the alert payload.
	// Default: 43200 (12 hours). Must be between 60 seconds and 30 days.
	DefaultIssueArchivingDelaySeconds int `json:"defaultIssueArchivingDelaySeconds" yaml:"defaultIssueArchivingDelaySeconds"`

	// DisableIssueReordering globally disables automatic severity-based ordering of issues.
	// When false (default), higher-severity issues are moved to the bottom of the channel
	// for visibility. When true, issues remain in creation order regardless of severity.
	// Can be overridden per-channel via AlertChannelSettings.DisableIssueReordering.
	DisableIssueReordering bool `json:"disableIssueReordering" yaml:"disableIssueReordering"`

	// IssueReorderingLimit is the maximum number of open issues in a channel before
	// automatic reordering is temporarily disabled. This prevents excessive API calls
	// during incident storms. When the issue count drops below this limit, reordering
	// resumes automatically. Can be overridden per-channel.
	// Default: 30. Must be between 5 and 100.
	IssueReorderingLimit int `json:"issueReorderingLimit" yaml:"issueReorderingLimit"`

	// IssueProcessingIntervalSeconds is the frequency (in seconds) of background issue
	// maintenance tasks: reordering, auto-resolution, archiving, and escalation.
	// Lower values provide more responsive behavior but increase API usage.
	// Can be overridden per-channel via AlertChannelSettings.IssueProcessingIntervalSeconds.
	// Default: 10. Must be between 3 and 600 seconds.
	IssueProcessingIntervalSeconds int `json:"issueProcessingIntervalSeconds" yaml:"issueProcessingIntervalSeconds"`

	// IssueReactions configures which emoji reactions trigger issue actions. Each action
	// can have multiple emojis configured, allowing teams to use their preferred symbols.
	// If nil, a default IssueReactionSettings is created during InitAndValidate().
	IssueReactions *IssueReactionSettings `json:"issueReactions" yaml:"issueReactions"`

	// IssueStatus configures the emoji indicators displayed in issue posts to show
	// severity and state. These appear in the issue header for quick visual scanning.
	// If nil, a default IssueStatusSettings is created during InitAndValidate().
	IssueStatus *IssueStatusSettings `json:"issueStatus" yaml:"issueStatus"`

	// MinIssueCountForThrottle is the minimum number of open issues in a channel before
	// update throttling is considered. Below this threshold, all updates are immediate.
	// Above it, rapid updates to the same issue may be delayed to reduce API usage.
	// Default: 5. Must be between 1 and 100.
	MinIssueCountForThrottle int `json:"minIssueCountForThrottle" yaml:"minIssueCountForThrottle"`

	// MaxThrottleDurationSeconds is the maximum delay (in seconds) between updates to
	// a single issue when throttling is active. The actual delay scales with alert count
	// but never exceeds this value. After this duration, updates are always allowed.
	// Default: 90. Must be between 1 and 600 seconds.
	MaxThrottleDurationSeconds int `json:"maxThrottleDurationSeconds" yaml:"maxThrottleDurationSeconds"`

	// AlwaysShowOptionButtons controls whether interactive action buttons are permanently
	// visible on issue posts. When false (default), buttons are hidden until a user reacts
	// with the ShowOptionButtonsEmoji, reducing visual clutter. When true, buttons are
	// always visible for immediate access to actions.
	AlwaysShowOptionButtons bool `json:"alwaysShowOptionButtons" yaml:"alwaysShowOptionButtons"`

	// ShowIssueCorrelationIDInSlackPost controls whether the issue's correlation ID
	// (used for grouping related alerts) is displayed in the post footer. When false
	// (default), the ID is hidden but accessible via the Issue Details modal.
	// Enable for debugging or when correlation IDs are meaningful to users.
	ShowIssueCorrelationIDInSlackPost bool `json:"showIssueCorrelationIdInSlackPost" yaml:"showIssueCorrelationIdInSlackPost"`

	// DocsURL is an optional URL to documentation about your Slack Manager deployment.
	// When set, this URL is included in help messages and error dialogs to direct users
	// to relevant documentation. Leave empty if no documentation is available.
	DocsURL string `json:"docsUrl" yaml:"docsUrl"`

	// AlertChannels contains per-channel configuration overrides. Use this to customize
	// admin permissions, reordering behavior, and processing intervals for specific channels.
	// Channels not listed here use the global settings. Channel IDs must be unique and
	// cannot overlap with InfoChannels.
	AlertChannels []*AlertChannelSettings `json:"alertChannels" yaml:"alertChannels"`

	// InfoChannels defines channels designated for system information display. These
	// channels cannot receive alerts - the API returns an error if an alert targets an
	// info channel. Use for dashboards, status pages, or documentation channels.
	// Channel IDs must be unique and cannot overlap with AlertChannels.
	InfoChannels []*InfoChannelSettings `json:"infoChannels" yaml:"infoChannels"`

	globalAdmins       map[string]struct{}
	alertChannels      map[string]*AlertChannelSettings
	infoChannels       map[string]*InfoChannelSettings
	issueReactionMap   map[string]IssueReaction
	issueReactionMutex sync.RWMutex
	initialized        bool
}

// IssueReactionSettings configures which emoji reactions trigger issue actions.
//
// Users interact with issues by adding emoji reactions to Slack posts. Each action type
// can have multiple emojis configured, allowing teams to use their preferred symbols while
// maintaining consistent behavior. Emojis can be specified with or without colons - they
// are normalized during validation.
//
// # Permissions
//
// Some actions require channel admin permissions:
//   - Terminate: Requires admin (destructive action)
//   - Resolve: Requires admin (changes issue state)
//   - Mute: Requires admin (affects escalation)
//
// Other actions are available to all channel members:
//   - Investigate: Any user can mark themselves as investigating
//   - ShowOptionButtons: Any user can reveal the action buttons
type IssueReactionSettings struct {
	// TerminateEmojis lists emojis that trigger immediate issue termination. Terminated
	// issues are removed without being marked as resolved - use for false positives or
	// issues that should be dismissed without tracking. Requires admin permissions.
	// Default: [":firecracker:"]
	TerminateEmojis []string `json:"terminateEmojis" yaml:"terminateEmojis"`

	// ResolveEmojis lists emojis that mark an issue as resolved. Resolved issues can be
	// re-opened by new alerts until the archiving delay elapses. Requires admin.
	// Default: [":white_check_mark:"]
	ResolveEmojis []string `json:"resolveEmojis" yaml:"resolveEmojis"`

	// InvestigateEmojis lists emojis that mark the reacting user as investigating the
	// issue. The user's name appears in the issue post to signal that someone is working
	// on it. Available to all users. Default: [":eyes:"]
	InvestigateEmojis []string `json:"investigateEmojis" yaml:"investigateEmojis"`

	// MuteEmojis lists emojis that mute an issue, suppressing escalation notifications
	// while keeping the issue visible. Useful for known issues being monitored.
	// Requires admin permissions. Default: [":mask:"]
	MuteEmojis []string `json:"muteEmojis" yaml:"muteEmojis"`

	// ShowOptionButtonsEmojis lists emojis that reveal the interactive action buttons
	// on the issue post (when AlwaysShowOptionButtons is false). Provides quick access
	// to actions without memorizing emoji shortcuts. Available to all users.
	// Default: [":information_source:"]
	ShowOptionButtonsEmojis []string `json:"showOptionButtonsEmojis" yaml:"showOptionButtonsEmojis"`
}

// IssueStatusSettings configures the visual indicators displayed in issue post headers.
//
// Status emojis provide at-a-glance severity and state information, helping users quickly
// scan a channel to identify high-priority issues. Each issue displays one status emoji
// based on its current severity and mute state.
//
// # Severity Hierarchy (highest to lowest)
//
//   - Panic: Critical issues requiring immediate attention
//   - Error: Significant issues that need prompt resolution
//   - Warning: Potential issues that should be monitored
//   - Info: Informational alerts (lowest priority)
//
// # Muted States
//
// Muted issues display different emojis to indicate they won't trigger escalations
// while remaining visible for monitoring. Muting is useful for known issues or issues
// being actively worked on.
type IssueStatusSettings struct {
	// PanicEmoji indicates a panic-level issue (highest severity, unmuted).
	// Default: ":scream:"
	PanicEmoji string `json:"panicEmoji" yaml:"panicEmoji"`

	// ErrorEmoji indicates an error-level issue (unmuted).
	// Default: ":x:"
	ErrorEmoji string `json:"errorEmoji" yaml:"errorEmoji"`

	// WarningEmoji indicates a warning-level issue (unmuted).
	// Default: ":warning:"
	WarningEmoji string `json:"warningEmoji" yaml:"warningEmoji"`

	// InfoEmoji indicates an info-level issue (lowest severity, unmuted).
	// Default: ":information_source:"
	InfoEmoji string `json:"infoEmoji" yaml:"infoEmoji"`

	// MutePanicEmoji indicates a muted panic-level issue. Escalations are suppressed
	// but the issue remains visible and can still receive alerts.
	// Default: ":no_bell:"
	MutePanicEmoji string `json:"mutePanicEmoji" yaml:"mutePanicEmoji"`

	// MuteErrorEmoji indicates a muted error-level issue.
	// Default: ":no_bell:"
	MuteErrorEmoji string `json:"muteErrorEmoji" yaml:"muteErrorEmoji"`

	// MuteWarningEmoji indicates a muted warning-level issue.
	// Default: ":no_bell:"
	MuteWarningEmoji string `json:"muteWarningEmoji" yaml:"muteWarningEmoji"`

	// InconclusiveEmoji indicates an issue resolved as inconclusive - neither confirmed
	// as fixed nor identified as a false positive. Used when the root cause is unclear.
	// Default: ":grey_question:"
	InconclusiveEmoji string `json:"unresolvedEmoji" yaml:"unresolvedEmoji"`

	// ResolvedEmoji indicates a successfully resolved (and possibly archived) issue.
	// Default: ":white_check_mark:"
	ResolvedEmoji string `json:"resolvedEmoji" yaml:"resolvedEmoji"`
}

// AlertChannelSettings provides per-channel configuration overrides for alert channels.
//
// Use this to customize admin permissions and processing behavior for specific channels
// without affecting the global defaults. Channels not configured here use the global
// ManagerSettings values.
//
// # Admin Hierarchy
//
// A user is considered an admin for this channel if any of the following are true:
//  1. The user is listed in ManagerSettings.GlobalAdmins (global admin)
//  2. The user is listed in this channel's AdminUsers
//  3. The user is a member of a group listed in this channel's AdminGroups
//
// Channel-specific admins cannot perform actions in other channels unless they are
// also global admins or admins of those specific channels.
type AlertChannelSettings struct {
	// ID is the Slack channel ID (e.g., "C1234567890"). This must be the channel's
	// internal ID, not its display name. Required and must be unique across all
	// AlertChannels and InfoChannels.
	ID string `json:"id" yaml:"id"`

	// AdminUsers lists Slack user IDs (e.g., "U1234567890") with admin permissions
	// in this channel. These users can resolve, terminate, and mute issues in this
	// channel only. For cross-channel admin access, use ManagerSettings.GlobalAdmins.
	AdminUsers []string `json:"adminUsers" yaml:"adminUsers"`

	// AdminGroups lists Slack user group IDs whose members have admin permissions in
	// this channel. Group membership is checked dynamically via the Slack API. This
	// allows permission management through Slack groups rather than individual user IDs.
	AdminGroups []string `json:"adminGroups" yaml:"adminGroups"`

	// DisableIssueReordering overrides the global DisableIssueReordering setting for
	// this channel. When true, issues in this channel are displayed in creation order
	// regardless of severity, even if global reordering is enabled.
	DisableIssueReordering bool `json:"disableIssueReordering" yaml:"disableIssueReordering"`

	// IssueReorderingLimit overrides the global IssueReorderingLimit for this channel.
	// When the number of open issues exceeds this limit, reordering is temporarily
	// disabled for this channel. Set to 0 to use the global default.
	// Must be between 5 and 100 if specified.
	IssueReorderingLimit int `json:"issueReorderingLimit" yaml:"issueReorderingLimit"`

	// IssueProcessingIntervalSeconds overrides the global IssueProcessingIntervalSeconds
	// for this channel. Controls how frequently background maintenance runs for this
	// channel's issues. Set to 0 to use the global default.
	// Must be between 3 and 600 seconds if specified.
	IssueProcessingIntervalSeconds int `json:"issueProcessingIntervalSeconds" yaml:"issueProcessingIntervalSeconds"`

	adminUsers  map[string]struct{}
	adminGroups map[string]struct{}
}

// InfoChannelSettings configures a channel designated for system information display.
//
// Info channels cannot receive alerts - the API returns an error if an alert targets
// an info channel. Use info channels for:
//   - System status dashboards
//   - Documentation or help content
//   - Operational announcements
//
// The Manager can post templated content to info channels based on the configured
// template path, which is rendered using the Go template engine with access to
// system status information.
type InfoChannelSettings struct {
	// ID is the Slack channel ID (e.g., "C1234567890"). Required and must be unique
	// across all AlertChannels and InfoChannels.
	ID string `json:"channelId" yaml:"channelId"`

	// TemplatePath is the file path to a Go template file used to render content
	// for this info channel. The template has access to system status data and is
	// re-rendered periodically. Required.
	TemplatePath string `json:"templatePath" yaml:"templatePath"`
}

// Clone creates a deep copy of the ManagerSettings by marshaling to JSON and back.
// The returned clone is NOT initialized - InitAndValidate() must be called before use.
//
// This method is used internally by the Manager to ensure that external modifications
// to the original settings struct do not affect the running system.
//
// Only exported fields are copied. Unexported fields (internal maps, mutex, initialized flag)
// are intentionally excluded since InitAndValidate() rebuilds them.
func (s *ManagerSettings) Clone() (*ManagerSettings, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal settings: %w", err)
	}

	clone := &ManagerSettings{}
	if err := json.Unmarshal(data, clone); err != nil {
		return nil, fmt.Errorf("failed to unmarshal settings: %w", err)
	}

	return clone, nil
}

// InitAndValidate initializes internal data structures and validates all settings.
//
// This method performs the following operations:
//  1. Sets default values for any missing or zero-value fields
//  2. Normalizes emoji formats (adds colons if missing)
//  3. Builds internal lookup maps for efficient access (global admins, channels, reactions)
//  4. Validates all fields are within acceptable ranges
//  5. Checks for duplicate or overlapping channel IDs
//
// This method is idempotent - calling it multiple times on an already-initialized
// ManagerSettings has no effect. The Manager calls this internally when settings
// are updated, so external callers typically don't need to call it directly.
//
// Returns an error describing the first validation failure encountered, or nil if valid.
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

	if s.DefaultPostIconEmoji == "" {
		s.DefaultPostIconEmoji = DefaultPostIconEmoji
	} else {
		// Normalize emoji format by ensuring it has colons on both ends
		s.DefaultPostIconEmoji = strings.TrimSpace(s.DefaultPostIconEmoji)

		if !strings.HasPrefix(s.DefaultPostIconEmoji, ":") {
			s.DefaultPostIconEmoji = ":" + s.DefaultPostIconEmoji
		}

		if !strings.HasSuffix(s.DefaultPostIconEmoji, ":") {
			s.DefaultPostIconEmoji += ":"
		}

		if !common.IconRegex.MatchString(s.DefaultPostIconEmoji) {
			return errors.New("default icon emoji must be on the format \":emoji:\"")
		}
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
		return fmt.Errorf("default archiving delay must be between %d and %d seconds", MinIssueArchivingDelaySeconds, MaxIssueArchivingDelaySeconds)
	}

	if s.IssueReorderingLimit <= 0 {
		s.IssueReorderingLimit = DefaultIssueReorderingLimit
	} else if s.IssueReorderingLimit < MinIssueReorderingLimit || s.IssueReorderingLimit > MaxIssueReorderingLimit {
		return fmt.Errorf("issue reordering limit must be between %d and %d (use DisableIssueReordering to turn off reordering)", MinIssueReorderingLimit, MaxIssueReorderingLimit)
	}

	if s.IssueProcessingIntervalSeconds <= 0 {
		s.IssueProcessingIntervalSeconds = DefaultIssueProcessingIntervalSeconds
	} else if s.IssueProcessingIntervalSeconds < MinIssueProcessingIntervalSeconds || s.IssueProcessingIntervalSeconds > MaxIssueProcessingIntervalSeconds {
		return fmt.Errorf("issue processing interval must be between %d and %d seconds", MinIssueProcessingIntervalSeconds, MaxIssueProcessingIntervalSeconds)
	}

	if s.IssueReactions == nil {
		s.IssueReactions = &IssueReactionSettings{}
	}

	var err error
	s.IssueReactions.TerminateEmojis, err = initReactionEmojiSlice(s.IssueReactions.TerminateEmojis, DefaultIssueTerminateEmoji, "issueReactions.terminateEmojis")
	if err != nil {
		return err
	}

	s.IssueReactions.ResolveEmojis, err = initReactionEmojiSlice(s.IssueReactions.ResolveEmojis, DefaultIssueResolveEmoji, "issueReactions.resolveEmojis")
	if err != nil {
		return err
	}

	s.IssueReactions.InvestigateEmojis, err = initReactionEmojiSlice(s.IssueReactions.InvestigateEmojis, DefaultIssueInvestigateEmoji, "issueReactions.investigateEmojis")
	if err != nil {
		return err
	}

	s.IssueReactions.MuteEmojis, err = initReactionEmojiSlice(s.IssueReactions.MuteEmojis, DefaultIssueMuteEmoji, "issueReactions.muteEmojis")
	if err != nil {
		return err
	}

	s.IssueReactions.ShowOptionButtonsEmojis, err = initReactionEmojiSlice(s.IssueReactions.ShowOptionButtonsEmojis, DefaultIssueShowOptionButtonsEmoji, "issueReactions.showOptionButtonsEmojis")
	if err != nil {
		return err
	}

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
	} else if s.MinIssueCountForThrottle < MinMinIssueCountForThrottle || s.MinIssueCountForThrottle > MaxMinIssueCountForThrottle {
		return fmt.Errorf("min issue count for throttle must be between %d and %d", MinMinIssueCountForThrottle, MaxMinIssueCountForThrottle)
	}

	if s.MaxThrottleDurationSeconds <= 0 {
		s.MaxThrottleDurationSeconds = DefaultMaxThrottleDurationSeconds
	} else if s.MaxThrottleDurationSeconds < MinMaxThrottleDurationSeconds || s.MaxThrottleDurationSeconds > MaxMaxThrottleDurationSeconds {
		return fmt.Errorf("max throttle duration must be between %d and %d seconds", MinMaxThrottleDurationSeconds, MaxMaxThrottleDurationSeconds)
	}

	for i, a := range s.AlertChannels {
		a.ID = strings.TrimSpace(a.ID)

		if a.ID == "" {
			return fmt.Errorf("alertChannels[%d].id cannot be empty", i)
		}

		if _, exists := s.alertChannels[a.ID]; exists {
			return fmt.Errorf("alertChannels[%d].id %q is a duplicate", i, a.ID)
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
			return fmt.Errorf("alertChannels[%d].issueReorderingLimit must be between %d and %d (use DisableIssueReordering to turn off reordering)", i, MinIssueReorderingLimit, MaxIssueReorderingLimit)
		}

		if a.IssueProcessingIntervalSeconds <= 0 {
			a.IssueProcessingIntervalSeconds = s.IssueProcessingIntervalSeconds
		} else if a.IssueProcessingIntervalSeconds < MinIssueProcessingIntervalSeconds || a.IssueProcessingIntervalSeconds > MaxIssueProcessingIntervalSeconds {
			return fmt.Errorf("alertChannels[%d].issueProcessingIntervalSeconds must be between %d and %d seconds", i, MinIssueProcessingIntervalSeconds, MaxIssueProcessingIntervalSeconds)
		}

		s.alertChannels[a.ID] = a
	}

	for i, a := range s.InfoChannels {
		a.ID = strings.TrimSpace(a.ID)
		a.TemplatePath = strings.TrimSpace(a.TemplatePath)

		if a.ID == "" {
			return fmt.Errorf("infoChannels[%d].id cannot be empty", i)
		}

		if _, exists := s.infoChannels[a.ID]; exists {
			return fmt.Errorf("infoChannels[%d].id %q is a duplicate", i, a.ID)
		}

		if _, exists := s.alertChannels[a.ID]; exists {
			return fmt.Errorf("infoChannels[%d].id %q is already configured as an alert channel", i, a.ID)
		}

		if a.TemplatePath == "" {
			return fmt.Errorf("infoChannels[%d].templatePath cannot be empty", i)
		}

		s.infoChannels[a.ID] = a
	}

	s.initialized = true

	return nil
}

// UserIsGlobalAdmin reports whether the given user ID is listed in GlobalAdmins.
// Global admins have admin permissions in all channels and can perform any action.
//
// Returns false if the settings have not been initialized or if userID is not found.
func (s *ManagerSettings) UserIsGlobalAdmin(userID string) bool {
	if !s.initialized {
		return false
	}

	_, ok := s.globalAdmins[userID]
	return ok
}

// UserIsChannelAdmin reports whether the given user has admin permissions for the channel.
//
// A user is considered a channel admin if any of the following are true:
//  1. The user is a global admin (listed in GlobalAdmins)
//  2. The user is listed in the channel's AdminUsers
//  3. The user is a member of a group listed in the channel's AdminGroups
//
// The userIsInGroup callback is used to check group membership via the Slack API.
// If userIsInGroup is nil, group-based permissions are not checked.
//
// Returns false if the settings have not been initialized, if the channel has no
// specific configuration, or if the user doesn't match any admin criteria.
func (s *ManagerSettings) UserIsChannelAdmin(ctx context.Context, channelID, userID string, userIsInGroup func(ctx context.Context, groupID, userID string) bool) bool {
	if !s.initialized || userID == "" || channelID == "" {
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

// IsInfoChannel reports whether the channel is configured as an info channel.
// Info channels cannot receive alerts - they are designated for system information
// display only. The API returns an error if an alert targets an info channel.
//
// Returns false if the settings have not been initialized or if the channel is not
// configured as an info channel.
func (s *ManagerSettings) IsInfoChannel(channelID string) bool {
	if !s.initialized {
		return false
	}

	_, ok := s.infoChannels[channelID]
	return ok
}

// GetInfoChannelConfig returns the InfoChannelSettings for the given channel ID.
// The second return value indicates whether the channel was found in the info channel
// configuration. Returns (nil, false) if the settings have not been initialized or
// if the channel is not configured as an info channel.
func (s *ManagerSettings) GetInfoChannelConfig(channelID string) (*InfoChannelSettings, bool) {
	if !s.initialized {
		return nil, false
	}

	c, ok := s.infoChannels[channelID]
	return c, ok
}

// OrderIssuesBySeverity reports whether automatic severity-based issue ordering should
// be applied for the given channel with the current number of open issues.
//
// Reordering is enabled when all of the following are true:
//  1. DisableIssueReordering is false (globally or for this channel)
//  2. The number of open issues does not exceed IssueReorderingLimit
//
// Channel-specific settings override global settings when configured. Returns false
// if the settings have not been initialized.
func (s *ManagerSettings) OrderIssuesBySeverity(channelID string, openIssueCount int) bool {
	if !s.initialized {
		return false
	}

	if a, ok := s.alertChannels[channelID]; ok {
		return !a.DisableIssueReordering && openIssueCount <= a.IssueReorderingLimit
	}

	return !s.DisableIssueReordering && openIssueCount <= s.IssueReorderingLimit
}

// IssueProcessingInterval returns the configured interval between background processing
// runs for the given channel. This controls how frequently the channel manager performs
// maintenance tasks like reordering, auto-resolution, and archiving.
//
// Returns the channel-specific interval if configured in AlertChannels, otherwise
// returns the global IssueProcessingIntervalSeconds value. If the settings have not
// been initialized, returns the default interval (10 seconds).
func (s *ManagerSettings) IssueProcessingInterval(channelID string) time.Duration {
	if !s.initialized {
		return time.Duration(DefaultIssueProcessingIntervalSeconds) * time.Second
	}

	if a, ok := s.alertChannels[channelID]; ok {
		return time.Duration(a.IssueProcessingIntervalSeconds) * time.Second
	}

	return time.Duration(s.IssueProcessingIntervalSeconds) * time.Second
}

// MapSlackPostReaction maps a Slack reaction emoji name to an IssueReaction action type.
//
// The reaction parameter is the emoji name without colons (e.g., "white_check_mark"),
// as provided by Slack's reaction_added events. The method checks against all configured
// reaction emojis in IssueReactionSettings and returns the corresponding action type.
//
// Results are cached in a thread-safe map for performance, as reaction lookups occur
// frequently during event processing. Returns an empty string if the reaction doesn't
// match any configured action emoji or if the settings have not been initialized.
func (s *ManagerSettings) MapSlackPostReaction(reaction string) IssueReaction {
	if reaction == "" || !s.initialized {
		return ""
	}

	// Check cache with read lock first
	s.issueReactionMutex.RLock()
	if val, ok := s.issueReactionMap[reaction]; ok {
		s.issueReactionMutex.RUnlock()
		return val
	}
	s.issueReactionMutex.RUnlock()

	// Compute the reaction type
	var r IssueReaction

	switch {
	case slices.Contains(s.IssueReactions.TerminateEmojis, reaction):
		r = IssueReactionTerminate
	case slices.Contains(s.IssueReactions.ResolveEmojis, reaction):
		r = IssueReactionResolve
	case slices.Contains(s.IssueReactions.InvestigateEmojis, reaction):
		r = IssueReactionInvestigate
	case slices.Contains(s.IssueReactions.MuteEmojis, reaction):
		r = IssueReactionMute
	case slices.Contains(s.IssueReactions.ShowOptionButtonsEmojis, reaction):
		r = IssueReactionShowOptionButtons
	default:
		r = ""
	}

	// Store in cache with write lock
	s.issueReactionMutex.Lock()
	s.issueReactionMap[reaction] = r
	s.issueReactionMutex.Unlock()

	return r
}

func initReactionEmojiSlice(emojis []string, defaultEmoji string, fieldName string) ([]string, error) {
	result := []string{}

	for i, emoji := range emojis {
		emoji = strings.TrimSpace(emoji)

		if emoji == "" {
			return nil, fmt.Errorf("%s[%d] cannot be empty", fieldName, i)
		}

		// Normalize by adding colons if missing
		if !strings.HasPrefix(emoji, ":") {
			emoji = ":" + emoji
		}

		if !strings.HasSuffix(emoji, ":") {
			emoji += ":"
		}

		if !common.IconRegex.MatchString(emoji) {
			return nil, fmt.Errorf("%s[%d] %q is not a valid emoji format (expected :emoji:)", fieldName, i, emojis[i])
		}

		result = append(result, emoji)
	}

	if len(result) == 0 {
		result = []string{defaultEmoji}
	}

	// When comparing with reaction events, we need to remove the colons from the emojis.
	for i, emoji := range result {
		result[i] = strings.Trim(emoji, ":")
	}

	return result, nil
}

func initStatusEmoji(emoji, defaultEmoji string) string {
	emoji = strings.TrimSpace(emoji)

	if !common.IconRegex.MatchString(emoji) {
		return defaultEmoji
	}

	return emoji
}
