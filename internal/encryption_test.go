package internal_test

import (
	"strings"
	"testing"

	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookEncryption(t *testing.T) {
	t.Parallel()

	validKey := []byte("passphrasewhichneedstobe32bytes!")

	t.Run("encrypts and decrypts payload successfully", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{
				"foo":       "bar",
				"val":       1,
				"something": true,
				"else":      []string{"a", "b"},
			},
		}

		err := internal.EncryptWebhookPayload(w, validKey)
		require.NoError(t, err)

		data, ok := w.Payload["__encrypted_data"].(string)
		assert.True(t, ok)
		assert.NotEmpty(t, data)

		payload, err := internal.DecryptWebhookPayload(w, validKey)
		require.NoError(t, err)

		assert.Equal(t, "bar", payload["foo"])
		assert.InDelta(t, float64(1), payload["val"], 0.0001)
		assert.Equal(t, true, payload["something"])
		assert.Equal(t, ([]any{"a", "b"}), payload["else"])
	})

	t.Run("empty payload is not encrypted", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{},
		}

		err := internal.EncryptWebhookPayload(w, validKey)
		require.NoError(t, err)

		// Payload should remain empty
		assert.Empty(t, w.Payload)
	})

	t.Run("nil payload is not encrypted", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: nil,
		}

		err := internal.EncryptWebhookPayload(w, validKey)
		require.NoError(t, err)
		assert.Nil(t, w.Payload)
	})

	t.Run("invalid key length returns error", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"key": "value"},
		}

		err := internal.EncryptWebhookPayload(w, []byte("short"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "32")
	})

	t.Run("payload too large returns error", func(t *testing.T) {
		t.Parallel()

		// Create a large payload > 2048 bytes
		largeValue := strings.Repeat("x", 3000)
		w := &types.Webhook{
			Payload: map[string]any{"large": largeValue},
		}

		err := internal.EncryptWebhookPayload(w, validKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "2048")
	})

	t.Run("decrypt with wrong key fails", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"key": "value"},
		}

		err := internal.EncryptWebhookPayload(w, validKey)
		require.NoError(t, err)

		wrongKey := []byte("differentkeywhichis32byteslong!")
		_, err = internal.DecryptWebhookPayload(w, wrongKey)
		require.Error(t, err)
	})

	t.Run("decrypt with invalid key length returns error", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"__encrypted_data": "somedata"},
		}

		_, err := internal.DecryptWebhookPayload(w, []byte("short"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "32")
	})

	t.Run("decrypt empty payload returns empty map", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{},
		}

		payload, err := internal.DecryptWebhookPayload(w, validKey)
		require.NoError(t, err)
		assert.Empty(t, payload)
	})

	t.Run("decrypt payload without __encrypted_data returns empty map", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"other": "data"},
		}

		payload, err := internal.DecryptWebhookPayload(w, validKey)
		require.NoError(t, err)
		assert.Empty(t, payload)
	})

	t.Run("decrypt with invalid base64 returns error", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"__encrypted_data": "not-valid-base64!!!"},
		}

		_, err := internal.DecryptWebhookPayload(w, validKey)
		require.Error(t, err)
	})

	t.Run("decrypt with wrong type for __encrypted_data returns error", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"__encrypted_data": 12345},
		}

		_, err := internal.DecryptWebhookPayload(w, validKey)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "string")
	})

	t.Run("decrypt with truncated data returns error", func(t *testing.T) {
		t.Parallel()

		w := &types.Webhook{
			Payload: map[string]any{"key": "value"},
		}

		err := internal.EncryptWebhookPayload(w, validKey)
		require.NoError(t, err)

		// Truncate the encrypted data
		encData, ok := w.Payload["__encrypted_data"].(string)
		require.True(t, ok, "expected encrypted data to be string")
		w.Payload["__encrypted_data"] = encData[:10]

		_, err = internal.DecryptWebhookPayload(w, validKey)
		require.Error(t, err)
	})
}
