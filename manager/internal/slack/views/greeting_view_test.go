package views_test

import (
	"encoding/json"
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager/internal/slack/views"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validGreetingSettings returns a *config.ManagerSettings with all fields required
// by GreetingView populated.
func validGreetingSettings() *config.ManagerSettings {
	return &config.ManagerSettings{
		AppFriendlyName: "Test App",
		IssueReactions: &config.IssueReactionSettings{
			TerminateEmojis:   []string{"firecracker"},
			ResolveEmojis:     []string{"white_check_mark"},
			MuteEmojis:        []string{"mask"},
			InvestigateEmojis: []string{"eyes"},
		},
	}
}

func TestGreetingView_ReturnsBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		isChannelAdmin bool
	}{
		{"channel admin", true},
		{"non-admin user", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			blocks, err := views.GreetingView("alice", tt.isChannelAdmin, validGreetingSettings())

			require.NoError(t, err)
			assert.NotEmpty(t, blocks)
		})
	}
}

func TestGreetingView_ContainsUsername(t *testing.T) {
	t.Parallel()

	blocks, err := views.GreetingView("bob", false, validGreetingSettings())

	require.NoError(t, err)
	data, marshalErr := json.Marshal(blocks)
	require.NoError(t, marshalErr)
	assert.Contains(t, string(data), "bob")
}

func TestGreetingView_ContainsAppName(t *testing.T) {
	t.Parallel()

	settings := validGreetingSettings()
	settings.AppFriendlyName = "My Custom Bot"

	blocks, err := views.GreetingView("alice", false, settings)

	require.NoError(t, err)
	data, marshalErr := json.Marshal(blocks)
	require.NoError(t, marshalErr)
	assert.Contains(t, string(data), "My Custom Bot")
}

func TestGreetingView_AdminVsNonAdmin(t *testing.T) {
	t.Parallel()

	settings := validGreetingSettings()

	adminBlocks, err := views.GreetingView("alice", true, settings)
	require.NoError(t, err)

	nonAdminBlocks, err := views.GreetingView("alice", false, settings)
	require.NoError(t, err)

	adminData, err := json.Marshal(adminBlocks)
	require.NoError(t, err)

	nonAdminData, err := json.Marshal(nonAdminBlocks)
	require.NoError(t, err)

	// Admin template mentions having admin status; non-admin mentions needing it.
	assert.Contains(t, string(adminData), "admin status")
	assert.NotContains(t, string(nonAdminData), "admin status")
}

func TestGreetingView_DocsURL(t *testing.T) {
	t.Parallel()

	t.Run("docs URL included when set", func(t *testing.T) {
		t.Parallel()
		settings := validGreetingSettings()
		settings.DocsURL = "https://docs.example.com"

		blocks, err := views.GreetingView("alice", false, settings)

		require.NoError(t, err)
		data, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(data), "https://docs.example.com")
	})

	t.Run("docs URL absent when empty", func(t *testing.T) {
		t.Parallel()
		settings := validGreetingSettings()
		settings.DocsURL = ""

		blocks, err := views.GreetingView("alice", false, settings)

		require.NoError(t, err)
		data, marshalErr := json.Marshal(blocks)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(data), "docs.example.com")
	})
}
