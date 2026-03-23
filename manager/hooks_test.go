package manager_test

import (
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
)

func newMinimalManager() *manager.Manager {
	cfg := config.NewDefaultManagerConfig()
	cfg.SkipDatabaseCache = true
	return manager.New(nil, nil, nil, &types.NoopLogger{}, cfg)
}

func TestWithHooks_Chaining(t *testing.T) {
	t.Parallel()

	m := newMinimalManager()
	got := m.WithHooks(manager.Hooks{})
	assert.Same(t, m, got, "WithHooks must return the same *Manager for chaining")
}

func TestWithHooks_NilFieldsAreAccepted(t *testing.T) {
	t.Parallel()

	// A zero-value ManagerHooks (all nil funcs) must be accepted without panic.
	m := newMinimalManager()
	assert.NotPanics(t, func() {
		m.WithHooks(manager.Hooks{})
	})
}

func TestManagerHooks_OnStartupAndOnShutdownAreSet(t *testing.T) {
	t.Parallel()

	var startupCalled, shutdownCalled bool

	hooks := manager.Hooks{
		OnStartup:  func() { startupCalled = true },
		OnShutdown: func() { shutdownCalled = true },
	}

	// Verify the struct fields are callable (compile-time check + runtime nil guard).
	assert.NotNil(t, hooks.OnStartup)
	assert.NotNil(t, hooks.OnShutdown)

	hooks.OnStartup()
	hooks.OnShutdown()

	assert.True(t, startupCalled)
	assert.True(t, shutdownCalled)
}
