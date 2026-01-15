package config_test

import (
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLogger implements common.Logger for testing.
type mockLogger struct{}

func (m *mockLogger) Debug(_ string)                            {}
func (m *mockLogger) Debugf(_ string, _ ...any)                 {}
func (m *mockLogger) Info(_ string)                             {}
func (m *mockLogger) Infof(_ string, _ ...any)                  {}
func (m *mockLogger) Error(_ string)                            {}
func (m *mockLogger) Errorf(_ string, _ ...any)                 {}
func (m *mockLogger) WithField(_ string, _ any) common.Logger   { return m } //nolint:ireturn
func (m *mockLogger) WithFields(_ map[string]any) common.Logger { return m } //nolint:ireturn

func TestAPISettings_InitAndValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		settings    *config.APISettings
		expectError string
	}{
		{
			name:        "empty settings is valid",
			settings:    &config.APISettings{},
			expectError: "",
		},
		{
			name: "valid rule with equals",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Equals:  []string{"key1", "key2"},
						Channel: "C1234567890",
					},
				},
			},
			expectError: "",
		},
		{
			name: "valid rule with prefix",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:      "test-rule",
						HasPrefix: []string{"prefix-"},
						Channel:   "C1234567890",
					},
				},
			},
			expectError: "",
		},
		{
			name: "valid rule with match all",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:     "catch-all",
						MatchAll: true,
						Channel:  "C1234567890",
					},
				},
			},
			expectError: "",
		},
		{
			name: "empty rule name",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "",
						Equals:  []string{"key1"},
						Channel: "C1234567890",
					},
				},
			},
			expectError: "rule[0].name cannot be empty",
		},
		{
			name: "whitespace only rule name",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "   ",
						Equals:  []string{"key1"},
						Channel: "C1234567890",
					},
				},
			},
			expectError: "rule[0].name cannot be empty",
		},
		{
			name: "duplicate rule names",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "rule-1",
						Equals:  []string{"key1"},
						Channel: "C1234567890",
					},
					{
						Name:    "rule-1",
						Equals:  []string{"key2"},
						Channel: "C1234567891",
					},
				},
			},
			expectError: "rule[1].name is not unique",
		},
		{
			name: "empty equals value",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Equals:  []string{"key1", ""},
						Channel: "C1234567890",
					},
				},
			},
			expectError: "rule[0].equals[1] cannot be empty",
		},
		{
			name: "empty prefix value",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:      "test-rule",
						HasPrefix: []string{""},
						Channel:   "C1234567890",
					},
				},
			},
			expectError: "rule[0].hasPrefix[0] cannot be empty",
		},
		{
			name: "empty regex value",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:         "test-rule",
						MatchesRegex: []string{""},
						Channel:      "C1234567890",
					},
				},
			},
			expectError: "rule[0].matchesRegex[0] cannot be empty",
		},
		{
			name: "invalid regex",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:         "test-rule",
						MatchesRegex: []string{"[invalid"},
						Channel:      "C1234567890",
					},
				},
			},
			expectError: "failed to compile regex for rule[0]: error parsing regexp: missing closing ]: `[invalid`",
		},
		{
			name: "valid regex rule",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:         "test-rule",
						MatchesRegex: []string{"^prod-.*"},
						Channel:      "C1234567890",
					},
				},
			},
			expectError: "",
		},
		{
			name: "rule matches nothing",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Channel: "C1234567890",
					},
				},
			},
			expectError: "rule[0] does not match anything",
		},
		{
			name: "empty channel",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Equals:  []string{"key1"},
						Channel: "",
					},
				},
			},
			expectError: "rule[0].channel cannot be empty",
		},
		{
			name: "invalid channel ID format",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Equals:  []string{"key1"},
						Channel: "invalid",
					},
				},
			},
			expectError: "rule[0].channel is not a valid Slack channel ID",
		},
		{
			name: "channel ID too short",
			settings: &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Equals:  []string{"key1"},
						Channel: "C12345",
					},
				},
			},
			expectError: "rule[0].channel is not a valid Slack channel ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.settings.InitAndValidate(&mockLogger{})

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestAPISettings_InitAndValidate_Idempotent(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{
				Name:    "test-rule",
				Equals:  []string{"key1"},
				Channel: "C1234567890",
			},
		},
	}

	logger := &mockLogger{}

	err := settings.InitAndValidate(logger)
	require.NoError(t, err)

	// Calling again should not error (idempotent)
	err = settings.InitAndValidate(logger)
	assert.NoError(t, err)
}

func TestAPISettings_InitAndValidate_NormalizesValues(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{
				Name:      "  test-rule  ",
				AlertType: "  SECURITY  ",
				Equals:    []string{"  KEY1  ", "KEY2"},
				HasPrefix: []string{"  PREFIX-  "},
				Channel:   "  C1234567890  ",
			},
		},
	}

	err := settings.InitAndValidate(&mockLogger{})
	require.NoError(t, err)

	rule := settings.RoutingRules[0]
	assert.Equal(t, "test-rule", rule.Name)
	assert.Equal(t, "security", rule.AlertType)
	assert.Equal(t, []string{"key1", "key2"}, rule.Equals)
	assert.Equal(t, []string{"prefix-"}, rule.HasPrefix)
	assert.Equal(t, "C1234567890", rule.Channel)
}

func TestAPISettings_Match(t *testing.T) {
	t.Parallel()
	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{
				Name:      "exact-with-type",
				AlertType: "security",
				Equals:    []string{"exact-key"},
				Channel:   "C1111111111",
			},
			{
				Name:    "exact-no-type",
				Equals:  []string{"exact-key"},
				Channel: "C2222222222",
			},
			{
				Name:      "prefix-rule",
				HasPrefix: []string{"prod-"},
				Channel:   "C3333333333",
			},
			{
				Name:         "regex-rule",
				MatchesRegex: []string{"^test-.*-end$"},
				Channel:      "C4444444444",
			},
			{
				Name:     "catch-all",
				MatchAll: true,
				Channel:  "C5555555555",
			},
		},
	}

	logger := &mockLogger{}
	err := settings.InitAndValidate(logger)
	require.NoError(t, err)

	tests := []struct {
		name            string
		routeKey        string
		alertType       string
		expectedChannel string
		expectedMatch   bool
	}{
		{
			name:            "exact match with matching alert type",
			routeKey:        "exact-key",
			alertType:       "security",
			expectedChannel: "C1111111111",
			expectedMatch:   true,
		},
		{
			name:            "exact match with non-matching alert type falls back",
			routeKey:        "exact-key",
			alertType:       "other",
			expectedChannel: "C2222222222",
			expectedMatch:   true,
		},
		{
			name:            "exact match case insensitive",
			routeKey:        "EXACT-KEY",
			alertType:       "",
			expectedChannel: "C2222222222",
			expectedMatch:   true,
		},
		{
			name:            "prefix match",
			routeKey:        "prod-service-1",
			alertType:       "",
			expectedChannel: "C3333333333",
			expectedMatch:   true,
		},
		{
			name:            "regex match",
			routeKey:        "test-something-end",
			alertType:       "",
			expectedChannel: "C4444444444",
			expectedMatch:   true,
		},
		{
			name:            "regex match case insensitive",
			routeKey:        "TEST-SOMETHING-END",
			alertType:       "",
			expectedChannel: "C4444444444",
			expectedMatch:   true,
		},
		{
			name:            "catch-all match",
			routeKey:        "unknown-key",
			alertType:       "",
			expectedChannel: "C5555555555",
			expectedMatch:   true,
		},
		{
			name:            "empty route key with catch-all",
			routeKey:        "",
			alertType:       "",
			expectedChannel: "C5555555555",
			expectedMatch:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			channel, matched := settings.Match(tt.routeKey, tt.alertType, logger)

			assert.Equal(t, tt.expectedMatch, matched)
			assert.Equal(t, tt.expectedChannel, channel)
		})
	}
}

func TestAPISettings_Match_NoRules(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{}
	logger := &mockLogger{}

	err := settings.InitAndValidate(logger)
	require.NoError(t, err)

	channel, matched := settings.Match("any-key", "", logger)

	assert.False(t, matched)
	assert.Empty(t, channel)
}

func TestAPISettings_Match_Caching(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{
				Name:    "test-rule",
				Equals:  []string{"key1"},
				Channel: "C1234567890",
			},
		},
	}

	logger := &mockLogger{}
	err := settings.InitAndValidate(logger)
	require.NoError(t, err)

	// First call
	channel1, matched1 := settings.Match("key1", "", logger)
	require.True(t, matched1)
	require.Equal(t, "C1234567890", channel1)

	// Second call should use cache
	channel2, matched2 := settings.Match("key1", "", logger)
	assert.True(t, matched2)
	assert.Equal(t, "C1234567890", channel2)

	// Non-match should also be cached
	channel3, matched3 := settings.Match("unknown", "", logger)
	assert.False(t, matched3)
	assert.Empty(t, channel3)

	// Second call for non-match
	channel4, matched4 := settings.Match("unknown", "", logger)
	assert.False(t, matched4)
	assert.Empty(t, channel4)
}

func TestAPISettings_Match_Precedence(t *testing.T) {
	t.Parallel()

	// Test that exact match takes precedence over prefix, regex, and match-all
	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{
				Name:     "catch-all",
				MatchAll: true,
				Channel:  "C1111111111",
			},
			{
				Name:         "regex-rule",
				MatchesRegex: []string{"^dev-.*-test$"},
				Channel:      "C2222222222",
			},
			{
				Name:      "prefix-rule",
				HasPrefix: []string{"prod-"},
				Channel:   "C3333333333",
			},
			{
				Name:    "exact-rule",
				Equals:  []string{"prod-service"},
				Channel: "C4444444444",
			},
		},
	}

	logger := &mockLogger{}
	err := settings.InitAndValidate(logger)
	require.NoError(t, err)

	// Exact match should win over prefix, regex, and catch-all
	channel, matched := settings.Match("prod-service", "", logger)
	assert.True(t, matched)
	assert.Equal(t, "C4444444444", channel)

	// Prefix should win over regex and catch-all
	channel, matched = settings.Match("prod-other", "", logger)
	assert.True(t, matched)
	assert.Equal(t, "C3333333333", channel)

	// Regex should win over catch-all
	channel, matched = settings.Match("dev-foo-test", "", logger)
	assert.True(t, matched)
	assert.Equal(t, "C2222222222", channel)

	// Catch-all for non-matching keys
	channel, matched = settings.Match("unknown", "", logger)
	assert.True(t, matched)
	assert.Equal(t, "C1111111111", channel)
}

func TestAPISettings_Match_AlertTypePrecedence(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{
				Name:      "exact-with-type",
				AlertType: "security",
				Equals:    []string{"key1"},
				Channel:   "C1111111111",
			},
			{
				Name:    "exact-no-type",
				Equals:  []string{"key1"},
				Channel: "C2222222222",
			},
		},
	}

	logger := &mockLogger{}
	err := settings.InitAndValidate(logger)
	require.NoError(t, err)

	// Matching alert type should take precedence
	channel, matched := settings.Match("key1", "security", logger)
	assert.True(t, matched)
	assert.Equal(t, "C1111111111", channel)

	// Non-matching alert type falls back to rule without alert type
	channel, matched = settings.Match("key1", "other", logger)
	assert.True(t, matched)
	assert.Equal(t, "C2222222222", channel)
}

func TestRoutingRule_ValidChannelIDFormats(t *testing.T) {
	t.Parallel()

	validChannelIDs := []string{
		"C1234567890",     // 11 chars
		"C12345678901234", // 15 chars (max)
		"C123456789",      // 10 chars
		"CABC123DEF",      // mixed alphanumeric
	}

	for _, channelID := range validChannelIDs {
		t.Run(channelID, func(t *testing.T) {
			t.Parallel()

			settings := &config.APISettings{
				RoutingRules: []*config.RoutingRule{
					{
						Name:    "test-rule",
						Equals:  []string{"key1"},
						Channel: channelID,
					},
				},
			}

			err := settings.InitAndValidate(&mockLogger{})
			assert.NoError(t, err)
		})
	}
}
