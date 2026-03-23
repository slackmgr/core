package restapi

// ServerHooks contains optional lifecycle callbacks for use with Kubernetes
// probe endpoints. All fields are optional; nil functions are silently skipped.
// Wire these to your own probe HTTP server — the library does not serve probes.
//
// Typical Kubernetes probe mapping:
//
//	startupProbe:   set a flag to true in [ServerHooks.OnStartup]
//	readinessProbe: set a flag to true in [ServerHooks.OnReady], false in [ServerHooks.OnNotReady]
//	livenessProbe:  set a flag to true in [ServerHooks.OnReady], false in [ServerHooks.OnShutdown]
//
// Register hooks via [Server.WithHooks] before calling [Server.Run].
type ServerHooks struct {
	// OnStartup is called once after Slack connects and channel info is
	// initialized, but before the TCP listener opens. Signals the end of the
	// startup phase — the process is past initialization but not yet accepting
	// traffic. Use it to pass a startup probe.
	OnStartup func()

	// OnReady is called once after net.Listen succeeds and the TCP port is
	// bound. At this point the server is accepting HTTP connections. Use it to
	// pass a readiness probe.
	OnReady func()

	// OnNotReady is called when shutdown begins (context cancelled), before
	// srv.Close() drains in-flight connections. Flip your readiness flag here
	// so the load balancer stops routing traffic before connections are torn
	// down.
	OnNotReady func()

	// OnShutdown is called once when [Server.Run] returns, regardless of
	// whether shutdown was caused by context cancellation or a fatal error.
	// Use it to flip your liveness flag to false.
	OnShutdown func()
}
