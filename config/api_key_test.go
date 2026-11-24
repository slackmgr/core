package config_test

import (
	"strings"
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashAPIKey(t *testing.T) {
	t.Parallel()

	// printf '%s' test | sha256sum
	assert.Equal(t, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", config.HashAPIKey("test"))
	assert.Len(t, config.HashAPIKey(""), 64)
	assert.NotEqual(t, config.HashAPIKey("a"), config.HashAPIKey("b"))
}

func TestAPISettings_InitAndValidate_APIKeys(t *testing.T) {
	t.Parallel()

	hashA := config.HashAPIKey("key-a")
	hashB := config.HashAPIKey("key-b")

	tests := []struct {
		name    string
		keys    []*config.APIKey
		wantErr string
	}{
		{name: "no keys"},
		{name: "valid keys", keys: []*config.APIKey{
			{ID: "a", KeySHA256: hashA, Scopes: []string{"alerts:write"}},
			{ID: "b", KeySHA256: hashB, Scopes: []string{"alerts:test", "config:read", "*"}},
		}},
		{name: "nil key", keys: []*config.APIKey{nil}, wantErr: "apiKeys[0] cannot be null"},
		{name: "empty id", keys: []*config.APIKey{{ID: "  ", KeySHA256: hashA, Scopes: []string{"*"}}}, wantErr: "apiKeys[0] is invalid: id cannot be empty"},
		{name: "too long id", keys: []*config.APIKey{{ID: strings.Repeat("a", config.MaxAPIKeyIDLength+1), KeySHA256: hashA, Scopes: []string{"*"}}}, wantErr: "id cannot be longer than 100 characters"},
		{name: "max length id", keys: []*config.APIKey{{ID: strings.Repeat("a", config.MaxAPIKeyIDLength), KeySHA256: hashA, Scopes: []string{"*"}}}},
		{name: "max length id in characters", keys: []*config.APIKey{{ID: strings.Repeat("é", config.MaxAPIKeyIDLength), KeySHA256: hashA, Scopes: []string{"*"}}}},
		{name: "id with reserved colon", keys: []*config.APIKey{{ID: "oidc:client", KeySHA256: hashA, Scopes: []string{"*"}}}, wantErr: `id cannot contain ":"`},
		{name: "missing hash", keys: []*config.APIKey{{ID: "a", Scopes: []string{"*"}}}, wantErr: "keySha256 must be a hex encoded SHA-256 hash (64 hex characters)"},
		{name: "non-hex hash", keys: []*config.APIKey{{ID: "a", KeySHA256: strings.Repeat("z", 64), Scopes: []string{"*"}}}, wantErr: "keySha256 must be a hex encoded SHA-256 hash"},
		{name: "short hash", keys: []*config.APIKey{{ID: "a", KeySHA256: hashA[:62], Scopes: []string{"*"}}}, wantErr: "keySha256 must be a hex encoded SHA-256 hash"},
		{name: "plain text key instead of hash", keys: []*config.APIKey{{ID: "a", KeySHA256: "key-a", Scopes: []string{"*"}}}, wantErr: "keySha256 must be a hex encoded SHA-256 hash"},
		{name: "no scopes", keys: []*config.APIKey{{ID: "a", KeySHA256: hashA}}, wantErr: "apiKeys[0] is invalid: scopes cannot be empty"},
		{name: "unknown scope", keys: []*config.APIKey{{ID: "a", KeySHA256: hashA, Scopes: []string{"alerts:write", "alerts:read"}}}, wantErr: `scopes[1] "alerts:read" is not a valid scope (valid scopes: alerts:write, alerts:test, config:read, *)`},
		{name: "empty scope", keys: []*config.APIKey{{ID: "a", KeySHA256: hashA, Scopes: []string{" "}}}, wantErr: `scopes[0] "" is not a valid scope`},
		{name: "duplicate id", keys: []*config.APIKey{
			{ID: "a", KeySHA256: hashA, Scopes: []string{"*"}},
			{ID: " a ", KeySHA256: hashB, Scopes: []string{"*"}},
		}, wantErr: "apiKeys[1].id is not unique"},
		{name: "duplicate hash", keys: []*config.APIKey{
			{ID: "a", KeySHA256: hashA, Scopes: []string{"*"}},
			{ID: "b", KeySHA256: strings.ToUpper(hashA), Scopes: []string{"*"}},
		}, wantErr: "apiKeys[1].keySha256 is not unique"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &config.APISettings{APIKeys: tt.keys}
			err := settings.InitAndValidate(&mockLogger{})

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
		})
	}
}

func TestAPISettings_InitAndValidate_NormalizesAPIKeys(t *testing.T) {
	t.Parallel()

	hash := config.HashAPIKey("key-a")

	settings := &config.APISettings{APIKeys: []*config.APIKey{
		{ID: "  a  ", Description: "  desc  ", KeySHA256: "  " + strings.ToUpper(hash) + "  ", Scopes: []string{" alerts:write "}},
	}}

	require.NoError(t, settings.InitAndValidate(&mockLogger{}))

	k := settings.APIKeys[0]
	assert.Equal(t, "a", k.ID)
	assert.Equal(t, "desc", k.Description)
	assert.Equal(t, hash, k.KeySHA256)
	assert.Equal(t, []string{"alerts:write"}, k.Scopes)
}

func TestAPISettings_LookupAPIKey(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{APIKeys: []*config.APIKey{
		{ID: "a", KeySHA256: config.HashAPIKey("key-a"), Scopes: []string{"alerts:write"}},
		{ID: "b", KeySHA256: config.HashAPIKey("key-b"), Scopes: []string{"*"}},
	}}

	t.Run("uninitialized settings have no keys", func(t *testing.T) {
		t.Parallel()

		uninitialized := &config.APISettings{APIKeys: []*config.APIKey{
			{ID: "a", KeySHA256: config.HashAPIKey("key-a"), Scopes: []string{"*"}},
		}}

		assert.Nil(t, uninitialized.LookupAPIKey("key-a"))
	})

	require.NoError(t, settings.InitAndValidate(&mockLogger{}))

	tests := []struct {
		name   string
		key    string
		wantID string
	}{
		{name: "first key", key: "key-a", wantID: "a"},
		{name: "second key", key: "key-b", wantID: "b"},
		{name: "unknown key", key: "key-c"},
		{name: "empty key", key: ""},
		{name: "hash is not a key", key: config.HashAPIKey("key-a")},
		{name: "case sensitive", key: "KEY-A"},
		{name: "not trimmed", key: " key-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			k := settings.LookupAPIKey(tt.key)

			if tt.wantID == "" {
				assert.Nil(t, k)
				return
			}

			require.NotNil(t, k)
			assert.Equal(t, tt.wantID, k.ID)
		})
	}
}

func TestAPISettings_Clone_APIKeys(t *testing.T) {
	t.Parallel()

	settings := &config.APISettings{APIKeys: []*config.APIKey{
		{ID: "a", Description: "desc", KeySHA256: config.HashAPIKey("key-a"), Scopes: []string{"alerts:write"}},
	}}

	clone, err := settings.Clone()
	require.NoError(t, err)
	require.Len(t, clone.APIKeys, 1)
	assert.Equal(t, *settings.APIKeys[0], *clone.APIKeys[0])
	assert.NotSame(t, settings.APIKeys[0], clone.APIKeys[0])

	// The clone is not initialized, so keys are only matched after InitAndValidate.
	assert.Nil(t, clone.LookupAPIKey("key-a"))
	require.NoError(t, clone.InitAndValidate(&mockLogger{}))
	assert.NotNil(t, clone.LookupAPIKey("key-a"))
}
