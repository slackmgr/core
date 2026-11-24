package config

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/slackmgr/types"
)

// MaxAPIKeyIDLength is the maximum length of [APIKey.ID].
const MaxAPIKeyIDLength = 100

// APIKey is an API key accepted by the built-in API key authentication of the REST API (see
// [APISettings.APIKeys]). Clients send the key as a bearer token (`Authorization: Bearer <key>`) or as the
// password of HTTP basic authentication (the username is ignored).
//
// Only the SHA-256 hash of the key is stored. Generate keys with a secure random generator, with at least 32
// bytes of entropy, e.g. `openssl rand -hex 32`, and hash them with [HashAPIKey] or
// `printf '%s' "$KEY" | sha256sum`.
//
// # Example Configuration
//
//	apiKeys:
//	  - id: "alertmanager-prod"
//	    description: "Prometheus Alertmanager in the prod cluster"
//	    keySha256: "<64 hex characters>" # printf '%s' "$KEY" | sha256sum
//	    scopes: ["alerts:write"]
type APIKey struct {
	// ID identifies the client using the key, e.g. "alertmanager-prod". It is the ID of the authenticated
	// principal, and is logged with each request. Required, unique, and at most [MaxAPIKeyIDLength] characters.
	ID string `json:"id" mapstructure:"id" yaml:"id"`

	// Description provides human-readable documentation for the key. Optional.
	Description string `json:"description" mapstructure:"description" yaml:"description"`

	// KeySHA256 is the hex encoded SHA-256 hash of the key (64 hex characters, case-insensitive). Required and
	// unique.
	KeySHA256 string `json:"keySha256" mapstructure:"keySha256" yaml:"keySha256"`

	// Scopes are the scopes granted to the key, e.g. "alerts:write". At least one is required. Valid scopes
	// are "alerts:write", "alerts:test", "config:read" and "*" (all scopes); see types.KnownScopes.
	Scopes []string `json:"scopes" mapstructure:"scopes" yaml:"scopes"`

	hash []byte
}

// HashAPIKey returns the hex encoded SHA-256 hash of an API key, for use as [APIKey.KeySHA256].
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// LookupAPIKey returns the API key matching the given plain text key, or nil if there is none. It compares the
// hash of key with all configured keys in constant time. The settings must be initialized with
// [APISettings.InitAndValidate]; uninitialized settings have no API keys.
func (s *APISettings) LookupAPIKey(key string) *APIKey {
	if key == "" || !s.initialized {
		return nil
	}

	sum := sha256.Sum256([]byte(key))

	var match *APIKey

	for _, k := range s.APIKeys {
		if subtle.ConstantTimeCompare(sum[:], k.hash) == 1 {
			match = k
		}
	}

	return match
}

// initAPIKeys normalizes and validates the API keys, and decodes their hashes.
func (s *APISettings) initAPIKeys() error {
	ids := make(map[string]struct{}, len(s.APIKeys))
	hashes := make(map[string]struct{}, len(s.APIKeys))

	for i, k := range s.APIKeys {
		if k == nil {
			return fmt.Errorf("apiKeys[%d] cannot be null", i)
		}

		if err := k.init(); err != nil {
			return fmt.Errorf("apiKeys[%d] is invalid: %w", i, err)
		}

		if _, ok := ids[k.ID]; ok {
			return fmt.Errorf("apiKeys[%d].id is not unique", i)
		}

		if _, ok := hashes[k.KeySHA256]; ok {
			return fmt.Errorf("apiKeys[%d].keySha256 is not unique", i)
		}

		ids[k.ID] = struct{}{}
		hashes[k.KeySHA256] = struct{}{}
	}

	return nil
}

func (k *APIKey) init() error {
	k.ID = strings.TrimSpace(k.ID)

	if k.ID == "" {
		return errors.New("id cannot be empty")
	}

	if len(k.ID) > MaxAPIKeyIDLength {
		return fmt.Errorf("id cannot be longer than %d characters", MaxAPIKeyIDLength)
	}

	k.Description = strings.TrimSpace(k.Description)
	k.KeySHA256 = strings.ToLower(strings.TrimSpace(k.KeySHA256))

	hash, err := hex.DecodeString(k.KeySHA256)
	if err != nil || len(hash) != sha256.Size {
		return fmt.Errorf("keySha256 must be a hex encoded SHA-256 hash (%d hex characters)", 2*sha256.Size)
	}

	k.hash = hash

	if len(k.Scopes) == 0 {
		return errors.New("scopes cannot be empty")
	}

	for j, scope := range k.Scopes {
		scope = strings.TrimSpace(scope)

		if !types.IsKnownScope(scope) {
			return fmt.Errorf("scopes[%d] %q is not a valid scope (valid scopes: %s)", j, scope, strings.Join(types.KnownScopes(), ", "))
		}

		k.Scopes[j] = scope
	}

	return nil
}
