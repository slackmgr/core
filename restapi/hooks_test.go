package restapi_test

import (
	"context"
	"testing"

	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/restapi"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
)

type noopQueue struct{}

func (noopQueue) Send(_ context.Context, _, _, _ string) error { return nil }

func newHooksTestServer() *restapi.Server {
	cfg := &config.APIConfig{
		RestPort:               "0",
		MaxUsersInAlertChannel: 1000,
		ShutdownTimeoutMs:      config.DefaultShutdownTimeoutMs,
		RateLimitPerAlertChannel: &config.RateLimitConfig{
			AlertsPerSecond: 10,
			AllowedBurst:    100,
		},
	}

	return restapi.New(noopQueue{}, &types.NoopLogger{}, cfg)
}

func TestWithHooks_Chaining(t *testing.T) {
	t.Parallel()

	s := newHooksTestServer()
	got := s.WithHooks(restapi.ServerHooks{})
	assert.Same(t, s, got, "WithHooks must return the same *Server for chaining")
}

func TestWithHooks_NilFieldsAreAccepted(t *testing.T) {
	t.Parallel()

	s := newHooksTestServer()
	assert.NotPanics(t, func() {
		s.WithHooks(restapi.ServerHooks{})
	})
}

func TestServerHooks_AllFieldsAreCallable(t *testing.T) {
	t.Parallel()

	var startupCalled, readyCalled, notReadyCalled, shutdownCalled bool

	hooks := restapi.ServerHooks{
		OnStartup:  func() { startupCalled = true },
		OnReady:    func() { readyCalled = true },
		OnNotReady: func() { notReadyCalled = true },
		OnShutdown: func() { shutdownCalled = true },
	}

	assert.NotNil(t, hooks.OnStartup)
	assert.NotNil(t, hooks.OnReady)
	assert.NotNil(t, hooks.OnNotReady)
	assert.NotNil(t, hooks.OnShutdown)

	hooks.OnStartup()
	hooks.OnReady()
	hooks.OnNotReady()
	hooks.OnShutdown()

	assert.True(t, startupCalled)
	assert.True(t, readyCalled)
	assert.True(t, notReadyCalled)
	assert.True(t, shutdownCalled)
}
