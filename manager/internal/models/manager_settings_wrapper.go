package models

import "github.com/peteraglen/slack-manager/config"

// ManagerSettingsWrapper wraps the ManagerSettings configuration.
// The wrapper allows hot-reloading of settings without passing them around directly.
type ManagerSettingsWrapper struct {
	Settings *config.ManagerSettings
}
