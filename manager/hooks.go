package manager

// Hooks contains optional lifecycle callbacks for use with Kubernetes probe
// endpoints. All fields are optional; nil functions are silently skipped.
// Wire these to your own probe HTTP server — the library does not serve probes.
//
// Typical Kubernetes probe mapping:
//
//	startupProbe:  set a flag to true in [Hooks.OnStartup]
//	livenessProbe: flag set true in [Hooks.OnStartup], false in [Hooks.OnShutdown]
//
// Register hooks via [Manager.WithHooks] before calling [Manager.Run].
type Hooks struct {
	// OnStartup is called once, after all goroutines are launched and the
	// manager is fully operational (Slack connected, coordinator initialized,
	// queue consumers running). Use it to signal that the startup probe has
	// passed and the process is live.
	OnStartup func()

	// OnShutdown is called once when [Manager.Run] returns, regardless of
	// whether shutdown was caused by context cancellation or a fatal error.
	// Use it to flip your liveness flag to false.
	OnShutdown func()
}
