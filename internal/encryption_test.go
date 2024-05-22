package internal

import (
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/stretchr/testify/assert"
)

func TestEncryption(t *testing.T) {
	key := []byte("passphrasewhichneedstobe32bytes!")
	data := []byte("Hello, World!")

	encrypted, err := encrypt(key, data)
	assert.NoError(t, err)

	decrypted, err := decrypt(key, encrypted)
	assert.NoError(t, err)
	assert.Equal(t, "Hello, World!", string(decrypted))
}

func TestWebhokEncryption(t *testing.T) {
	w := &common.Webhook{
		Payload: map[string]interface{}{
			"foo":       "bar",
			"val":       1,
			"something": true,
			"else":      []string{"a", "b"},
		},
	}

	err := EncryptWebhookPayload(w, []byte("passphrasewhichneedstobe32bytes!"))
	assert.NoError(t, err)

	data := w.Payload["__encrypted_data"].(string)
	assert.NotEmpty(t, data)

	payload, err := DecryptWebhookPayload(w, []byte("passphrasewhichneedstobe32bytes!"))
	assert.NoError(t, err)

	assert.Equal(t, "bar", payload["foo"])
	assert.Equal(t, float64(1), payload["val"])
	assert.Equal(t, true, payload["something"])
	assert.Equal(t, ([]interface{}{"a", "b"}), payload["else"])
}
