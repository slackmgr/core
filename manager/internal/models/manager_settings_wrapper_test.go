package models //nolint:testpackage // Internal test to access unexported fields

import (
	"sync"
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManagerSettingsWrapper(t *testing.T) {
	t.Parallel()

	t.Run("creates wrapper with settings", func(t *testing.T) {
		t.Parallel()

		settings := &config.ManagerSettings{
			DefaultPostUsername:  "test-bot",
			DefaultPostIconEmoji: ":robot:",
		}

		wrapper := NewManagerSettingsWrapper(settings)

		require.NotNil(t, wrapper)
		assert.Same(t, settings, wrapper.GetSettings())
	})

	t.Run("creates wrapper with nil settings", func(t *testing.T) {
		t.Parallel()

		wrapper := NewManagerSettingsWrapper(nil)

		require.NotNil(t, wrapper)
		assert.Nil(t, wrapper.GetSettings())
	})
}

func TestManagerSettingsWrapper_GetSettings(t *testing.T) {
	t.Parallel()

	t.Run("returns current settings", func(t *testing.T) {
		t.Parallel()

		settings := &config.ManagerSettings{
			DefaultPostUsername:  "test-bot",
			DefaultAlertSeverity: common.AlertWarning,
		}

		wrapper := NewManagerSettingsWrapper(settings)

		result := wrapper.GetSettings()

		assert.Equal(t, "test-bot", result.DefaultPostUsername)
		assert.Equal(t, common.AlertWarning, result.DefaultAlertSeverity)
	})

	t.Run("returns nil when settings are nil", func(t *testing.T) {
		t.Parallel()

		wrapper := NewManagerSettingsWrapper(nil)

		result := wrapper.GetSettings()

		assert.Nil(t, result)
	})
}

func TestManagerSettingsWrapper_SetSettings(t *testing.T) {
	t.Parallel()

	t.Run("updates settings", func(t *testing.T) {
		t.Parallel()

		initialSettings := &config.ManagerSettings{
			DefaultPostUsername: "initial-bot",
		}

		newSettings := &config.ManagerSettings{
			DefaultPostUsername: "updated-bot",
		}

		wrapper := NewManagerSettingsWrapper(initialSettings)
		wrapper.SetSettings(newSettings)

		assert.Same(t, newSettings, wrapper.GetSettings())
		assert.Equal(t, "updated-bot", wrapper.GetSettings().DefaultPostUsername)
	})

	t.Run("can set nil settings", func(t *testing.T) {
		t.Parallel()

		settings := &config.ManagerSettings{
			DefaultPostUsername: "test-bot",
		}

		wrapper := NewManagerSettingsWrapper(settings)
		wrapper.SetSettings(nil)

		assert.Nil(t, wrapper.GetSettings())
	})

	t.Run("can update from nil to non-nil", func(t *testing.T) {
		t.Parallel()

		wrapper := NewManagerSettingsWrapper(nil)

		newSettings := &config.ManagerSettings{
			DefaultPostUsername: "new-bot",
		}
		wrapper.SetSettings(newSettings)

		assert.Same(t, newSettings, wrapper.GetSettings())
	})
}

func TestManagerSettingsWrapper_ThreadSafety(t *testing.T) {
	t.Parallel()

	t.Run("concurrent reads are safe", func(t *testing.T) {
		t.Parallel()

		settings := &config.ManagerSettings{
			DefaultPostUsername: "test-bot",
		}

		wrapper := NewManagerSettingsWrapper(settings)

		var wg sync.WaitGroup
		const numReaders = 100

		for i := 0; i < numReaders; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					result := wrapper.GetSettings()
					assert.NotNil(t, result)
				}
			}()
		}

		wg.Wait()
	})

	t.Run("concurrent reads and writes are safe", func(t *testing.T) {
		t.Parallel()

		settings1 := &config.ManagerSettings{DefaultPostUsername: "bot-1"}
		settings2 := &config.ManagerSettings{DefaultPostUsername: "bot-2"}

		wrapper := NewManagerSettingsWrapper(settings1)

		var wg sync.WaitGroup
		const numReaders = 50
		const numWriters = 10
		const iterations = 100

		// Start readers
		for i := 0; i < numReaders; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					result := wrapper.GetSettings()
					// Should always get a valid settings object (not corrupted)
					if result != nil {
						name := result.DefaultPostUsername
						assert.True(t, name == "bot-1" || name == "bot-2",
							"Expected 'bot-1' or 'bot-2', got: %s", name)
					}
				}
			}()
		}

		// Start writers
		for i := 0; i < numWriters; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					if id%2 == 0 {
						wrapper.SetSettings(settings1)
					} else {
						wrapper.SetSettings(settings2)
					}
				}
			}(i)
		}

		wg.Wait()
	})

	t.Run("no data race with rapid updates", func(t *testing.T) {
		t.Parallel()

		wrapper := NewManagerSettingsWrapper(&config.ManagerSettings{})

		var wg sync.WaitGroup
		const numGoroutines = 20
		const iterations = 1000

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					if j%2 == 0 {
						wrapper.SetSettings(&config.ManagerSettings{
							DefaultPostUsername: "user",
						})
					} else {
						_ = wrapper.GetSettings()
					}
				}
			}()
		}

		wg.Wait()
	})
}

func TestManagerSettingsWrapper_SettingsImmutability(t *testing.T) {
	t.Parallel()

	t.Run("modifying returned settings affects wrapper", func(t *testing.T) {
		t.Parallel()

		// Note: The wrapper returns the pointer directly, not a copy.
		// This test documents the current behavior - modifications to the
		// returned settings will affect what other callers see.
		// If copy-on-read semantics are needed, the wrapper should be modified
		// to return a deep copy.

		settings := &config.ManagerSettings{
			DefaultPostUsername: "original",
		}

		wrapper := NewManagerSettingsWrapper(settings)

		// Get settings and modify them
		retrieved := wrapper.GetSettings()
		retrieved.DefaultPostUsername = "modified"

		// The modification is visible through the wrapper
		assert.Equal(t, "modified", wrapper.GetSettings().DefaultPostUsername)
	})
}
