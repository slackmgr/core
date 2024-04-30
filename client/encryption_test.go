package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEncryption(t *testing.T) {
	key := []byte("passphrasewhichneedstobe32bytes!")
	data := []byte("Heisann!")

	encrypted, err := Encrypt(key, data)
	assert.NoError(t, err)

	decrypted, err := Decrypt(key, encrypted)
	assert.NoError(t, err)
	assert.Equal(t, "Heisann!", string(decrypted))
}
