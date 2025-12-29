package internal_test

import (
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhokEncryption(t *testing.T) {
	t.Parallel()

	w := &common.Webhook{
		Payload: map[string]any{
			"foo":       "bar",
			"val":       1,
			"something": true,
			"else":      []string{"a", "b"},
		},
	}

	err := internal.EncryptWebhookPayload(w, []byte("passphrasewhichneedstobe32bytes!"))
	require.NoError(t, err)

	data, ok := w.Payload["__encrypted_data"].(string)
	assert.True(t, ok)
	assert.NotEmpty(t, data)

	payload, err := internal.DecryptWebhookPayload(w, []byte("passphrasewhichneedstobe32bytes!"))
	require.NoError(t, err)

	assert.Equal(t, "bar", payload["foo"])
	assert.InDelta(t, float64(1), payload["val"], 0.0001)
	assert.Equal(t, true, payload["something"])
	assert.Equal(t, ([]any{"a", "b"}), payload["else"])
}
