package manager

import (
	"context"
	"testing"
	"time"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPWebhookHandler_DefaultTimeout(t *testing.T) {
	t.Parallel()

	handler := NewHTTPWebhookHandler(&mockLogger{})

	assert.Equal(t, config.DefaultWebhookTimeoutSeconds*time.Second, handler.client.GetClient().Timeout)
}

func TestHTTPWebhookHandler_WithRequestTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "within range", timeout: 10 * time.Second, want: 10 * time.Second},
		{name: "at minimum", timeout: time.Second, want: time.Second},
		{name: "at maximum", timeout: 15 * time.Second, want: 15 * time.Second},
		{name: "below minimum", timeout: 500 * time.Millisecond, want: time.Second},
		{name: "zero", timeout: 0, want: time.Second},
		{name: "negative", timeout: -time.Second, want: time.Second},
		{name: "above maximum", timeout: time.Minute, want: 15 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := NewHTTPWebhookHandler(&mockLogger{}).WithRequestTimeout(tt.timeout)

			assert.Equal(t, tt.want, handler.client.GetClient().Timeout)
		})
	}
}

// claimAllWebhookHandler is a WebhookHandler that claims every target.
type claimAllWebhookHandler struct{}

func (h *claimAllWebhookHandler) ShouldHandleWebhook(_ context.Context, _ string) bool { return true }

func (h *claimAllWebhookHandler) HandleWebhook(_ context.Context, _ string, _ *types.WebhookCallback, _ types.Logger) error {
	return nil
}

func TestManager_AllWebhookHandlers(t *testing.T) {
	t.Parallel()

	t.Run("fallback only", func(t *testing.T) {
		t.Parallel()

		m := &Manager{logger: &mockLogger{}, cfg: &config.ManagerConfig{WebhookTimeoutSeconds: 12}}

		handlers := m.allWebhookHandlers()

		require.Len(t, handlers, 1)
		fallback, ok := handlers[0].(*HTTPWebhookHandler)
		require.True(t, ok)
		assert.Equal(t, 12*time.Second, fallback.client.GetClient().Timeout)
	})

	t.Run("fallback after registered handlers", func(t *testing.T) {
		t.Parallel()

		registered := &claimAllWebhookHandler{}
		m := (&Manager{logger: &mockLogger{}, cfg: &config.ManagerConfig{WebhookTimeoutSeconds: 5}}).RegisterWebhookHandler(registered)

		handlers := m.allWebhookHandlers()

		require.Len(t, handlers, 2)
		assert.Same(t, registered, handlers[0])
		assert.IsType(t, &HTTPWebhookHandler{}, handlers[1])
		assert.Len(t, m.webhookHandlers, 1, "registered handlers must not be modified")
	})
}
