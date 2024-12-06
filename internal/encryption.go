package internal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	common "github.com/peteraglen/slack-manager-common"
)

// EncryptWebhookPayload encrypts the existing webhook payload (if any) and replaces it with an encrypted version.
// This function modifies the state of the webhook.
func EncryptWebhookPayload(w *common.Webhook, key []byte) error {
	if len(w.Payload) == 0 {
		return nil
	}

	if len(key) != 32 {
		return errors.New("encryption key length must be 32")
	}

	data, err := json.Marshal(w.Payload)
	if err != nil {
		return err
	}

	if len(data) > 2048 {
		return fmt.Errorf("length of JSON serialized webhook payload is %d, expected <= 2048", len(data))
	}

	encryptedData, err := encrypt(key, data)
	if err != nil {
		return err
	}

	w.Payload = map[string]interface{}{
		"__encrypted_data": base64.StdEncoding.EncodeToString(encryptedData),
	}

	return nil
}

func encrypt(key, data []byte) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())

	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	return gcm.Seal(nonce, nonce, data, nil), nil
}

// DecryptWebhookPayload decrypts the encrypted webhook payload (if any) and returns it, or nil if payload is empty.
// This function does not modify the state of the webhook.
func DecryptWebhookPayload(w *common.Webhook, key []byte) (map[string]interface{}, error) {
	if len(w.Payload) == 0 {
		return map[string]interface{}{}, nil
	}

	if len(key) != 32 {
		return nil, errors.New("encryption key length must be 32")
	}

	encryptedDataBase64, dataFound := w.Payload["__encrypted_data"]

	if !dataFound {
		return map[string]interface{}{}, nil
	}

	encryptedData, err := base64.StdEncoding.DecodeString(encryptedDataBase64.(string))
	if err != nil {
		return nil, err
	}

	data, err := decrypt(key, encryptedData)
	if err != nil {
		return nil, err
	}

	originalPayload := make(map[string]interface{})

	if err := json.Unmarshal(data, &originalPayload); err != nil {
		return nil, err
	}

	return originalPayload, nil
}

func decrypt(key, encryptedData []byte) ([]byte, error) {
	c, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(c)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()

	if len(encryptedData) < nonceSize {
		return nil, errors.New("encrypted data length is too short")
	}

	nonce, ciphertext := encryptedData[:nonceSize], encryptedData[nonceSize:]

	return gcm.Open(nil, nonce, ciphertext, nil) // #nosec G407
}
