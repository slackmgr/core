package config_test

import (
	"context"
	"testing"
	"time"

	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerSettings_InitAndValidate_Defaults(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	assert.Equal(t, config.DefaultAppFriendlyName, settings.AppFriendlyName)
	assert.Equal(t, config.DefaultPostIconEmoji, settings.DefaultPostIconEmoji)
	assert.Equal(t, config.DefaultPostUsername, settings.DefaultPostUsername)
	assert.Equal(t, config.DefaultAlertSeverity, settings.DefaultAlertSeverity)
	assert.Equal(t, config.DefaultIssueArchivingDelaySeconds, settings.DefaultIssueArchivingDelaySeconds)
	assert.Equal(t, config.DefaultIssueReorderingLimit, settings.IssueReorderingLimit)
	assert.Equal(t, config.DefaultIssueProcessingIntervalSeconds, settings.IssueProcessingIntervalSeconds)
	assert.Equal(t, config.DefaultMinIssueCountForThrottle, settings.MinIssueCountForThrottle)
	assert.Equal(t, config.DefaultMaxThrottleDurationSeconds, settings.MaxThrottleDurationSeconds)

	require.NotNil(t, settings.IssueReactions)
	require.NotNil(t, settings.IssueStatus)
}

func TestManagerSettings_InitAndValidate_Idempotent(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{}

	err := settings.InitAndValidate()
	require.NoError(t, err)

	// Calling again should not error
	err = settings.InitAndValidate()
	assert.NoError(t, err)
}

func TestManagerSettings_InitAndValidate_GlobalAdmins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		admins      []string
		expectError string
	}{
		{
			name:        "valid admins",
			admins:      []string{"U123", "U456"},
			expectError: "",
		},
		{
			name:        "empty admin in list",
			admins:      []string{"U123", ""},
			expectError: "globalAdmins[1] cannot be empty",
		},
		{
			name:        "whitespace only admin",
			admins:      []string{"   "},
			expectError: "globalAdmins[0] cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				GlobalAdmins: tt.admins,
			}

			err := settings.InitAndValidate()

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_DefaultPostIconEmoji(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		emoji       string
		expected    string
		expectError bool
	}{
		{
			name:     "empty uses default",
			emoji:    "",
			expected: config.DefaultPostIconEmoji,
		},
		{
			name:     "valid emoji with colons",
			emoji:    ":rocket:",
			expected: ":rocket:",
		},
		{
			name:        "invalid emoji format",
			emoji:       "rocket",
			expected:    ":rocket:",
			expectError: false, // Gets wrapped with colons
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				DefaultPostIconEmoji: tt.emoji,
			}

			err := settings.InitAndValidate()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, settings.DefaultPostIconEmoji)
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_DefaultAlertSeverity(t *testing.T) {
	t.Parallel()

	t.Run("empty uses default", func(t *testing.T) {
		t.Parallel()
		settings := &config.ManagerSettings{}
		err := settings.InitAndValidate()
		require.NoError(t, err)
		assert.Equal(t, config.DefaultAlertSeverity, settings.DefaultAlertSeverity)
	})

	t.Run("invalid severity rejected", func(t *testing.T) {
		t.Parallel()
		settings := &config.ManagerSettings{
			DefaultAlertSeverity: "invalid",
		}
		err := settings.InitAndValidate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "default alert severity must be one of")
	})
}

func TestManagerSettings_InitAndValidate_ArchivingDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		delay       int
		expected    int
		expectError bool
	}{
		{
			name:     "zero uses default",
			delay:    0,
			expected: config.DefaultIssueArchivingDelaySeconds,
		},
		{
			name:     "negative uses default",
			delay:    -1,
			expected: config.DefaultIssueArchivingDelaySeconds,
		},
		{
			name:     "valid at minimum",
			delay:    config.MinIssueArchivingDelaySeconds,
			expected: config.MinIssueArchivingDelaySeconds,
		},
		{
			name:     "valid at maximum",
			delay:    config.MaxIssueArchivingDelaySeconds,
			expected: config.MaxIssueArchivingDelaySeconds,
		},
		{
			name:        "below minimum rejected",
			delay:       config.MinIssueArchivingDelaySeconds - 1,
			expectError: true,
		},
		{
			name:        "above maximum rejected",
			delay:       config.MaxIssueArchivingDelaySeconds + 1,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				DefaultIssueArchivingDelaySeconds: tt.delay,
			}

			err := settings.InitAndValidate()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, settings.DefaultIssueArchivingDelaySeconds)
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_IssueReorderingLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		limit       int
		expected    int
		expectError bool
	}{
		{
			name:     "zero uses default",
			limit:    0,
			expected: config.DefaultIssueReorderingLimit,
		},
		{
			name:     "valid at minimum",
			limit:    config.MinIssueReorderingLimit,
			expected: config.MinIssueReorderingLimit,
		},
		{
			name:     "valid at maximum",
			limit:    config.MaxIssueReorderingLimit,
			expected: config.MaxIssueReorderingLimit,
		},
		{
			name:        "below minimum rejected",
			limit:       config.MinIssueReorderingLimit - 1,
			expectError: true,
		},
		{
			name:        "above maximum rejected",
			limit:       config.MaxIssueReorderingLimit + 1,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				IssueReorderingLimit: tt.limit,
			}

			err := settings.InitAndValidate()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, settings.IssueReorderingLimit)
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_IssueProcessingInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		interval    int
		expected    int
		expectError bool
	}{
		{
			name:     "zero uses default",
			interval: 0,
			expected: config.DefaultIssueProcessingIntervalSeconds,
		},
		{
			name:     "valid at minimum",
			interval: config.MinIssueProcessingIntervalSeconds,
			expected: config.MinIssueProcessingIntervalSeconds,
		},
		{
			name:     "valid at maximum",
			interval: config.MaxIssueProcessingIntervalSeconds,
			expected: config.MaxIssueProcessingIntervalSeconds,
		},
		{
			name:        "below minimum rejected",
			interval:    config.MinIssueProcessingIntervalSeconds - 1,
			expectError: true,
		},
		{
			name:        "above maximum rejected",
			interval:    config.MaxIssueProcessingIntervalSeconds + 1,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				IssueProcessingIntervalSeconds: tt.interval,
			}

			err := settings.InitAndValidate()

			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, settings.IssueProcessingIntervalSeconds)
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_AlertChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		channels    []*config.AlertChannelSettings
		expectError string
	}{
		{
			name: "valid channel",
			channels: []*config.AlertChannelSettings{
				{ID: "C123456789", AdminUsers: []string{"U123"}},
			},
			expectError: "",
		},
		{
			name: "empty channel ID",
			channels: []*config.AlertChannelSettings{
				{ID: ""},
			},
			expectError: "alertChannels[0].id cannot be empty",
		},
		{
			name: "empty admin user in list",
			channels: []*config.AlertChannelSettings{
				{ID: "C123456789", AdminUsers: []string{"U123", ""}},
			},
			expectError: "alertChannels[0].adminUsers[1] cannot be empty",
		},
		{
			name: "empty admin group in list",
			channels: []*config.AlertChannelSettings{
				{ID: "C123456789", AdminGroups: []string{""}},
			},
			expectError: "alertChannels[0].adminGroups[0] cannot be empty",
		},
		{
			name: "channel reordering limit below minimum",
			channels: []*config.AlertChannelSettings{
				{ID: "C123456789", IssueReorderingLimit: config.MinIssueReorderingLimit - 1},
			},
			expectError: "alertChannels[0].issueReorderingLimit must be between 5 and 100 (use DisableIssueReordering to turn off reordering)",
		},
		{
			name: "channel processing interval below minimum",
			channels: []*config.AlertChannelSettings{
				{ID: "C123456789", IssueProcessingIntervalSeconds: config.MinIssueProcessingIntervalSeconds - 1},
			},
			expectError: "alertChannels[0].issueProcessingIntervalSeconds must be between 3 and 600 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				AlertChannels: tt.channels,
			}

			err := settings.InitAndValidate()

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_InfoChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		channels    []*config.InfoChannelSettings
		expectError string
	}{
		{
			name: "valid channel",
			channels: []*config.InfoChannelSettings{
				{ID: "C123456789", TemplatePath: "/path/to/template"},
			},
			expectError: "",
		},
		{
			name: "empty channel ID",
			channels: []*config.InfoChannelSettings{
				{ID: "", TemplatePath: "/path/to/template"},
			},
			expectError: "infoChannels[0].id cannot be empty",
		},
		{
			name: "empty template path",
			channels: []*config.InfoChannelSettings{
				{ID: "C123456789", TemplatePath: ""},
			},
			expectError: "infoChannels[0].templatePath cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				InfoChannels: tt.channels,
			}

			err := settings.InitAndValidate()

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestManagerSettings_UserIsGlobalAdmin(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		GlobalAdmins: []string{"U123", "U456"},
	}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	assert.True(t, settings.UserIsGlobalAdmin("U123"))
	assert.True(t, settings.UserIsGlobalAdmin("U456"))
	assert.False(t, settings.UserIsGlobalAdmin("U789"))
	assert.False(t, settings.UserIsGlobalAdmin(""))
}

func TestManagerSettings_UserIsChannelAdmin(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		GlobalAdmins: []string{"UGLOBAL"},
		AlertChannels: []*config.AlertChannelSettings{
			{
				ID:          "C123",
				AdminUsers:  []string{"UCHANNEL"},
				AdminGroups: []string{"GGROUP"},
			},
		},
	}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	ctx := context.Background()
	mockGroupChecker := func(_ context.Context, groupID, userID string) bool {
		return groupID == "GGROUP" && userID == "UGROUPMEMBER"
	}

	tests := []struct {
		name      string
		channelID string
		userID    string
		expected  bool
	}{
		{
			name:      "global admin has access",
			channelID: "C123",
			userID:    "UGLOBAL",
			expected:  true,
		},
		{
			name:      "channel admin has access",
			channelID: "C123",
			userID:    "UCHANNEL",
			expected:  true,
		},
		{
			name:      "group member has access",
			channelID: "C123",
			userID:    "UGROUPMEMBER",
			expected:  true,
		},
		{
			name:      "non-admin no access",
			channelID: "C123",
			userID:    "URANDOM",
			expected:  false,
		},
		{
			name:      "unknown channel no access",
			channelID: "CUNKNOWN",
			userID:    "UCHANNEL",
			expected:  false,
		},
		{
			name:      "empty user ID",
			channelID: "C123",
			userID:    "",
			expected:  false,
		},
		{
			name:      "empty channel ID",
			channelID: "",
			userID:    "UCHANNEL",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := settings.UserIsChannelAdmin(ctx, tt.channelID, tt.userID, mockGroupChecker)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManagerSettings_IsInfoChannel(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		InfoChannels: []*config.InfoChannelSettings{
			{ID: "CINFO1", TemplatePath: "/path1"},
			{ID: "CINFO2", TemplatePath: "/path2"},
		},
	}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	assert.True(t, settings.IsInfoChannel("CINFO1"))
	assert.True(t, settings.IsInfoChannel("CINFO2"))
	assert.False(t, settings.IsInfoChannel("COTHER"))
	assert.False(t, settings.IsInfoChannel(""))
}

func TestManagerSettings_GetInfoChannelConfig(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		InfoChannels: []*config.InfoChannelSettings{
			{ID: "CINFO1", TemplatePath: "/path1"},
		},
	}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	cfg, found := settings.GetInfoChannelConfig("CINFO1")
	assert.True(t, found)
	require.NotNil(t, cfg)
	assert.Equal(t, "/path1", cfg.TemplatePath)

	cfg, found = settings.GetInfoChannelConfig("CUNKNOWN")
	assert.False(t, found)
	assert.Nil(t, cfg)
}

func TestManagerSettings_OrderIssuesBySeverity(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		IssueReorderingLimit:   20,
		DisableIssueReordering: false,
		AlertChannels: []*config.AlertChannelSettings{
			{
				ID:                     "CDISABLED",
				DisableIssueReordering: true,
			},
			{
				ID:                   "CCUSTOM",
				IssueReorderingLimit: 10,
			},
		},
	}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	// Channel with reordering disabled
	assert.False(t, settings.OrderIssuesBySeverity("CDISABLED", 5))

	// Channel with custom limit
	assert.True(t, settings.OrderIssuesBySeverity("CCUSTOM", 5))
	assert.True(t, settings.OrderIssuesBySeverity("CCUSTOM", 10))
	assert.False(t, settings.OrderIssuesBySeverity("CCUSTOM", 11))

	// Unknown channel uses global settings
	assert.True(t, settings.OrderIssuesBySeverity("CUNKNOWN", 15))
	assert.True(t, settings.OrderIssuesBySeverity("CUNKNOWN", 20))
	assert.False(t, settings.OrderIssuesBySeverity("CUNKNOWN", 21))
}

func TestManagerSettings_IssueProcessingInterval(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		IssueProcessingIntervalSeconds: 30,
		AlertChannels: []*config.AlertChannelSettings{
			{
				ID:                             "CCUSTOM",
				IssueProcessingIntervalSeconds: 60,
			},
		},
	}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	assert.Equal(t, 60*time.Second, settings.IssueProcessingInterval("CCUSTOM"))
	assert.Equal(t, 30*time.Second, settings.IssueProcessingInterval("CUNKNOWN"))
}

func TestManagerSettings_MapSlackPostReaction(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{}
	err := settings.InitAndValidate()
	require.NoError(t, err)

	// Test default emojis (colons are stripped during init)
	assert.Equal(t, config.IssueReactionTerminate, settings.MapSlackPostReaction("firecracker"))
	assert.Equal(t, config.IssueReactionResolve, settings.MapSlackPostReaction("white_check_mark"))
	assert.Equal(t, config.IssueReactionInvestigate, settings.MapSlackPostReaction("eyes"))
	assert.Equal(t, config.IssueReactionMute, settings.MapSlackPostReaction("mask"))
	assert.Equal(t, config.IssueReactionShowOptionButtons, settings.MapSlackPostReaction("information_source"))

	// Unknown emoji
	assert.Equal(t, config.IssueReaction(""), settings.MapSlackPostReaction("unknown"))

	// Empty string
	assert.Equal(t, config.IssueReaction(""), settings.MapSlackPostReaction(""))
}

func TestManagerSettings_InitAndValidate_DefaultPostIconEmoji_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		emoji    string
		expected string
	}{
		{
			name:     "missing leading colon",
			emoji:    "rocket:",
			expected: ":rocket:",
		},
		{
			name:     "missing trailing colon",
			emoji:    ":rocket",
			expected: ":rocket:",
		},
		{
			name:     "missing both colons",
			emoji:    "rocket",
			expected: ":rocket:",
		},
		{
			name:     "valid format unchanged",
			emoji:    ":rocket:",
			expected: ":rocket:",
		},
		{
			name:     "whitespace trimmed",
			emoji:    "  :rocket:  ",
			expected: ":rocket:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				DefaultPostIconEmoji: tt.emoji,
			}

			err := settings.InitAndValidate()

			require.NoError(t, err)
			assert.Equal(t, tt.expected, settings.DefaultPostIconEmoji)
		})
	}
}

func TestManagerSettings_InitAndValidate_DuplicateAlertChannelIDs(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		AlertChannels: []*config.AlertChannelSettings{
			{ID: "C123456789"},
			{ID: "C123456789"},
		},
	}

	err := settings.InitAndValidate()

	require.Error(t, err)
	assert.Equal(t, `alertChannels[1].id "C123456789" is a duplicate`, err.Error())
}

func TestManagerSettings_InitAndValidate_DuplicateInfoChannelIDs(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		InfoChannels: []*config.InfoChannelSettings{
			{ID: "C123456789", TemplatePath: "/path1"},
			{ID: "C123456789", TemplatePath: "/path2"},
		},
	}

	err := settings.InitAndValidate()

	require.Error(t, err)
	assert.Equal(t, `infoChannels[1].id "C123456789" is a duplicate`, err.Error())
}

func TestManagerSettings_InitAndValidate_OverlappingAlertAndInfoChannelIDs(t *testing.T) {
	t.Parallel()

	settings := &config.ManagerSettings{
		AlertChannels: []*config.AlertChannelSettings{
			{ID: "C123456789"},
		},
		InfoChannels: []*config.InfoChannelSettings{
			{ID: "C123456789", TemplatePath: "/path"},
		},
	}

	err := settings.InitAndValidate()

	require.Error(t, err)
	assert.Equal(t, `infoChannels[0].id "C123456789" is already configured as an alert channel`, err.Error())
}

func TestManagerSettings_InitAndValidate_ThrottleSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                       string
		minIssueCountForThrottle   int
		maxThrottleDurationSeconds int
		expectError                string
	}{
		{
			name:                       "valid at lower bounds",
			minIssueCountForThrottle:   config.MinMinIssueCountForThrottle,
			maxThrottleDurationSeconds: config.MinMaxThrottleDurationSeconds,
			expectError:                "",
		},
		{
			name:                       "valid at upper bounds",
			minIssueCountForThrottle:   config.MaxMinIssueCountForThrottle,
			maxThrottleDurationSeconds: config.MaxMaxThrottleDurationSeconds,
			expectError:                "",
		},
		{
			name:                     "min issue count above maximum",
			minIssueCountForThrottle: config.MaxMinIssueCountForThrottle + 1,
			expectError:              "min issue count for throttle must be between 1 and 100",
		},
		{
			name:                       "max throttle duration above maximum",
			maxThrottleDurationSeconds: config.MaxMaxThrottleDurationSeconds + 1,
			expectError:                "max throttle duration must be between 1 and 600 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				MinIssueCountForThrottle:   tt.minIssueCountForThrottle,
				MaxThrottleDurationSeconds: tt.maxThrottleDurationSeconds,
			}

			err := settings.InitAndValidate()

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestManagerSettings_InitAndValidate_ThrottleSettingsDefaults(t *testing.T) {
	t.Parallel()

	// Zero and negative values should use defaults instead of erroring
	tests := []struct {
		name                       string
		minIssueCountForThrottle   int
		maxThrottleDurationSeconds int
	}{
		{
			name:                       "zero values use defaults",
			minIssueCountForThrottle:   0,
			maxThrottleDurationSeconds: 0,
		},
		{
			name:                       "negative values use defaults",
			minIssueCountForThrottle:   -5,
			maxThrottleDurationSeconds: -10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				MinIssueCountForThrottle:   tt.minIssueCountForThrottle,
				MaxThrottleDurationSeconds: tt.maxThrottleDurationSeconds,
			}

			err := settings.InitAndValidate()

			require.NoError(t, err)
			assert.Equal(t, config.DefaultMinIssueCountForThrottle, settings.MinIssueCountForThrottle)
			assert.Equal(t, config.DefaultMaxThrottleDurationSeconds, settings.MaxThrottleDurationSeconds)
		})
	}
}

func TestManagerSettings_InitAndValidate_InvalidReactionEmojis(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		reactions   *config.IssueReactionSettings
		expectError string
	}{
		{
			name: "empty emoji in terminate list",
			reactions: &config.IssueReactionSettings{
				TerminateEmojis: []string{":valid:", ""},
			},
			expectError: "issueReactions.terminateEmojis[1] cannot be empty",
		},
		{
			name: "whitespace only emoji in mute list",
			reactions: &config.IssueReactionSettings{
				MuteEmojis: []string{"   "},
			},
			expectError: "issueReactions.muteEmojis[0] cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.ManagerSettings{
				IssueReactions: tt.reactions,
			}

			err := settings.InitAndValidate()

			require.Error(t, err)
			assert.Equal(t, tt.expectError, err.Error())
		})
	}
}

func TestManagerSettings_MethodsBeforeInitialization(t *testing.T) {
	t.Parallel()

	// Test that methods return safe defaults when called before initialization
	settings := &config.ManagerSettings{}

	// These should return false/nil instead of panicking
	assert.False(t, settings.UserIsGlobalAdmin("U123"))
	assert.False(t, settings.UserIsChannelAdmin(context.Background(), "C123", "U123", nil))
	assert.False(t, settings.IsInfoChannel("C123"))
	assert.False(t, settings.OrderIssuesBySeverity("C123", 5))
	assert.Equal(t, config.IssueReaction(""), settings.MapSlackPostReaction("firecracker"))

	cfg, found := settings.GetInfoChannelConfig("C123")
	assert.Nil(t, cfg)
	assert.False(t, found)

	// IssueProcessingInterval returns a default value
	assert.Equal(t, time.Duration(config.DefaultIssueProcessingIntervalSeconds)*time.Second, settings.IssueProcessingInterval("C123"))
}
