package models

import (
	"sync"

	"github.com/peteraglen/slack-manager/config"
)

// ManagerSettingsWrapper wraps the ManagerSettings configuration with thread-safe access.
// The wrapper allows hot-reloading of settings without passing them around directly.
// All access to the settings should go through GetSettings() and SetSettings() methods
// to ensure thread safety during hot-reloading.
type ManagerSettingsWrapper struct {
	mu       sync.RWMutex
	settings *config.ManagerSettings
}

// NewManagerSettingsWrapper creates a new ManagerSettingsWrapper with the given settings.
func NewManagerSettingsWrapper(settings *config.ManagerSettings) *ManagerSettingsWrapper {
	return &ManagerSettingsWrapper{
		settings: settings,
	}
}

// GetSettings returns the current settings in a thread-safe manner.
func (w *ManagerSettingsWrapper) GetSettings() *config.ManagerSettings {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.settings
}

// SetSettings updates the settings in a thread-safe manner.
func (w *ManagerSettingsWrapper) SetSettings(settings *config.ManagerSettings) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.settings = settings
}
