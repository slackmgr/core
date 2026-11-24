package config_test

import (
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPISettings_InitAndValidate_AccessRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		defaultAccess string
		rules         []*config.AccessRule
		wantErr       string
		wantDefault   string
	}{
		{name: "no rules, default", wantDefault: config.ChannelAccessAllow},
		{name: "default allow", defaultAccess: "allow", wantDefault: config.ChannelAccessAllow},
		{name: "default deny, normalized", defaultAccess: " DENY ", wantDefault: config.ChannelAccessDeny},
		{name: "invalid default", defaultAccess: "block", wantErr: `defaultChannelAccess must be "allow" or "deny"`},
		{name: "valid rules", rules: []*config.AccessRule{
			{Name: "a", Principals: []string{"alertmanager", "team-a-*", "*"}, Channels: []string{"C1234567890", "*"}},
			{Name: "b", Principals: []string{"x"}, Channels: []string{"c1234567890"}},
		}, wantDefault: config.ChannelAccessAllow},
		{name: "nil rule", rules: []*config.AccessRule{nil}, wantErr: "accessRules[0] cannot be null"},
		{name: "empty name", rules: []*config.AccessRule{{Name: " ", Principals: []string{"a"}, Channels: []string{"*"}}}, wantErr: "accessRules[0] is invalid: name cannot be empty"},
		{name: "duplicate name", rules: []*config.AccessRule{
			{Name: "a", Principals: []string{"a"}, Channels: []string{"*"}},
			{Name: " a ", Principals: []string{"b"}, Channels: []string{"*"}},
		}, wantErr: "accessRules[1].name is not unique"},
		{name: "no principals", rules: []*config.AccessRule{{Name: "a", Channels: []string{"*"}}}, wantErr: "principals cannot be empty"},
		{name: "empty principal", rules: []*config.AccessRule{{Name: "a", Principals: []string{" "}, Channels: []string{"*"}}}, wantErr: "principals[0] cannot be empty"},
		{name: "wildcard in the middle", rules: []*config.AccessRule{{Name: "a", Principals: []string{"team-*-prod"}, Channels: []string{"*"}}}, wantErr: `principals[0] "team-*-prod" can only have '*' at the end`},
		{name: "double wildcard", rules: []*config.AccessRule{{Name: "a", Principals: []string{"**"}, Channels: []string{"*"}}}, wantErr: "can only have '*' at the end"},
		{name: "no channels", rules: []*config.AccessRule{{Name: "a", Principals: []string{"a"}}}, wantErr: "channels cannot be empty"},
		{name: "channel name", rules: []*config.AccessRule{{Name: "a", Principals: []string{"a"}, Channels: []string{"alerts-team-a"}}}, wantErr: `channels[0] "ALERTS-TEAM-A" is not a valid Slack channel ID or "*"`},
		{name: "empty channel", rules: []*config.AccessRule{{Name: "a", Principals: []string{"a"}, Channels: []string{""}}}, wantErr: "channels[0]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.APISettings{DefaultChannelAccess: tt.defaultAccess, AccessRules: tt.rules}
			err := settings.InitAndValidate(&mockLogger{})

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantDefault, settings.DefaultChannelAccess)
		})
	}
}

func TestAPISettings_InitAndValidate_NormalizesAccessRules(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{AccessRules: []*config.AccessRule{
		{Name: " a ", Description: " desc ", Principals: []string{" p "}, Channels: []string{" c1234567890 ", " * "}},
	}}

	require.NoError(t, settings.InitAndValidate(&mockLogger{}))

	r := settings.AccessRules[0]
	assert.Equal(t, "a", r.Name)
	assert.Equal(t, "desc", r.Description)
	assert.Equal(t, []string{"p"}, r.Principals)
	assert.Equal(t, []string{"C1234567890", "*"}, r.Channels)
}

func TestAPISettings_ChannelAccessAllowed(t *testing.T) {
	t.Parallel()

	newSettings := func(t *testing.T, defaultAccess string) *config.APISettings {
		t.Helper()

		settings := &config.APISettings{DefaultChannelAccess: defaultAccess, AccessRules: []*config.AccessRule{
			{Name: "alertmanager", Principals: []string{"alertmanager"}, Channels: []string{"C1111111111", "C2222222222"}},
			{Name: "team-a", Principals: []string{"team-a-*"}, Channels: []string{"C3333333333"}},
			{Name: "team-a-extra", Principals: []string{"team-a-prod"}, Channels: []string{"C4444444444"}},
			{Name: "admin", Principals: []string{"admin"}, Channels: []string{"*"}},
		}}
		require.NoError(t, settings.InitAndValidate(&mockLogger{}))

		return settings
	}

	tests := []struct {
		name          string
		defaultAccess string
		principal     string
		channel       string
		want          bool
	}{
		{name: "listed channel", principal: "alertmanager", channel: "C1111111111", want: true},
		{name: "second listed channel", principal: "alertmanager", channel: "C2222222222", want: true},
		{name: "case-insensitive channel", principal: "alertmanager", channel: " c1111111111 ", want: true},
		{name: "unlisted channel", principal: "alertmanager", channel: "C3333333333", want: false},
		{name: "unlisted channel with default deny", defaultAccess: "deny", principal: "alertmanager", channel: "C3333333333", want: false},
		{name: "prefix match", principal: "team-a-staging", channel: "C3333333333", want: true},
		{name: "prefix match, unlisted channel", principal: "team-a-staging", channel: "C4444444444", want: false},
		{name: "channels of all matching rules", principal: "team-a-prod", channel: "C4444444444", want: true},
		{name: "channels of all matching rules, prefix rule", principal: "team-a-prod", channel: "C3333333333", want: true},
		{name: "principal match is case-sensitive", principal: "Alertmanager", channel: "C3333333333", want: true},
		{name: "wildcard channel", principal: "admin", channel: "C9999999999", want: true},
		{name: "no rule, default allow", principal: "other", channel: "C9999999999", want: true},
		{name: "no rule, default deny", defaultAccess: "deny", principal: "other", channel: "C9999999999", want: false},
		{name: "unmapped channel name", principal: "alertmanager", channel: "alerts", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, newSettings(t, tt.defaultAccess).ChannelAccessAllowed(tt.principal, tt.channel))
		})
	}

	t.Run("wildcard principal applies to everyone", func(t *testing.T) {
		t.Parallel()

		settings := &config.APISettings{AccessRules: []*config.AccessRule{
			{Name: "all", Principals: []string{"*"}, Channels: []string{"C1111111111"}},
		}}
		require.NoError(t, settings.InitAndValidate(&mockLogger{}))

		assert.True(t, settings.ChannelAccessAllowed("anyone", "C1111111111"))
		assert.False(t, settings.ChannelAccessAllowed("anyone", "C2222222222"))
	})

	t.Run("uninitialized settings allow nothing", func(t *testing.T) {
		t.Parallel()

		assert.False(t, (&config.APISettings{}).ChannelAccessAllowed("anyone", "C1111111111"))
		assert.False(t, (&config.APISettings{DefaultChannelAccess: config.ChannelAccessAllow}).ChannelAccessAllowed("anyone", "C1111111111"))
	})
}

func TestAuthConfig(t *testing.T) {
	t.Parallel()

	t.Run("effective client binding", func(t *testing.T) {
		t.Parallel()

		var nilCfg *config.AuthConfig
		assert.Equal(t, config.ClientBindingFill, nilCfg.EffectiveClientBinding())
		assert.Equal(t, config.ClientBindingFill, (&config.AuthConfig{}).EffectiveClientBinding())
		assert.Equal(t, config.ClientBindingOverride, (&config.AuthConfig{ClientBinding: config.ClientBindingOverride}).EffectiveClientBinding())
		assert.Equal(t, config.ClientBindingOff, (&config.AuthConfig{ClientBinding: config.ClientBindingOff}).EffectiveClientBinding())
	})

	t.Run("validate", func(t *testing.T) {
		t.Parallel()

		for _, binding := range []string{"", "fill", "override", "off"} {
			require.NoError(t, (&config.AuthConfig{ClientBinding: binding}).Validate(), binding)
		}

		err := (&config.AuthConfig{ClientBinding: "Fill"}).Validate()
		require.EqualError(t, err, `client binding must be "fill", "override" or "off"`)
	})

	t.Run("api config validates auth", func(t *testing.T) {
		t.Parallel()

		cfg := validAPIConfig()
		cfg.Auth = &config.AuthConfig{ClientBinding: "always"}
		require.ErrorContains(t, cfg.Validate(), "auth config is invalid: client binding must be")

		cfg.Auth.ClientBinding = config.ClientBindingOverride
		require.NoError(t, cfg.Validate())
	})
}
