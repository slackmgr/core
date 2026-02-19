package internal_test

import (
	"testing"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/internal"
	"github.com/stretchr/testify/assert"
)

func TestNewChannelSummary(t *testing.T) {
	t.Parallel()

	t.Run("extracts ID and Name from slack.Channel", func(t *testing.T) {
		t.Parallel()

		channel := slack.Channel{
			GroupConversation: slack.GroupConversation{
				Conversation: slack.Conversation{
					ID: "C123ABC",
				},
				Name: "general",
			},
		}

		summary := internal.NewChannelSummary(channel)

		assert.Equal(t, "C123ABC", summary.ID)
		assert.Equal(t, "general", summary.Name)
	})

	t.Run("handles empty channel", func(t *testing.T) {
		t.Parallel()

		channel := slack.Channel{}

		summary := internal.NewChannelSummary(channel)

		assert.Empty(t, summary.ID)
		assert.Empty(t, summary.Name)
	})
}
