package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	cachestore "github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	"github.com/gin-contrib/timeout"
	"github.com/gin-gonic/gin"
	gocache "github.com/patrickmn/go-cache"
	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

const (
	httpRequestMetric  = "http_server_request_duration_seconds"
	httpInFlightMetric = "http_server_requests_in_flight"
)

const (
	handlerTimeout     = 30 * time.Second
	readHeaderTimeout  = 5 * time.Second
	timeoutBuffer      = time.Second // wiggle room added to HTTP server timeouts
	idleTimeout        = 60 * time.Second
	maxRetryAfterDelay = 24 * time.Hour // cap for rate.InfDuration from ReserveN
)

// FifoQueueConsumer is the read side of a FIFO queue. It is used by the [Server] to
// consume raw alert items delivered outside of the HTTP API (e.g. via a secondary
// SQS or Redis Streams queue registered with [Server.WithRawAlertConsumer]).
type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *types.FifoQueueItem) error
}

// FifoQueueProducer is the write side of a FIFO queue. The [Server] uses it to
// dispatch processed alerts to the Manager for issue lifecycle handling.
type FifoQueueProducer interface {
	Send(ctx context.Context, slackChannelID, dedupID, body string) error
}

// SlackClient is the Slack API subset required by the [Server] for channel
// validation (membership checks, user counts) and for listing managed channels.
type SlackClient interface {
	GetChannelInfo(ctx context.Context, channelID string) (*slack.Channel, error)
	GetUserIDsInChannel(ctx context.Context, channelID string) (map[string]struct{}, error)
	BotIsInChannel(ctx context.Context, channelID string) (bool, error)
	ListBotChannels(ctx context.Context) ([]*internal.ChannelSummary, error)
}

// Server is the Slack Manager REST API server. It accepts alerts from external
// monitoring systems, applies per-channel token-bucket rate limiting, and enqueues
// them for the Manager to process into Slack issues.
//
// Create a Server with [New] and start it with [Server.Run]. API settings can be
// updated at runtime via [Server.UpdateSettings] without restarting the service.
type Server struct {
	rawAlertConsumers   []FifoQueueConsumer
	alertQueue          FifoQueueProducer
	limitersByChannel   map[string]*rate.Limiter
	limitersLock        *sync.Mutex
	cacheStore          cachestore.StoreInterface
	slackClient         SlackClient
	channelInfoProvider ChannelInfoProvider
	logger              types.Logger
	metrics             types.Metrics
	apiSettings         *config.APISettings
	cfg                 *config.APIConfig
	defaultPretty       bool
	hooks               Hooks
}

// New creates a [Server] with the three required dependencies.
//
// Optional dependencies can be configured via method chaining before calling [Server.Run]:
//   - [Server.WithCacheStore] — sets the cache store (defaults to in-process go-cache, unsuitable for multi-instance)
//   - [Server.WithMetrics] — sets the metrics implementation (defaults to no-op)
//   - [Server.WithSettings] — sets the initial API settings (defaults to zero-value)
func New(alertQueue FifoQueueProducer, logger types.Logger, cfg *config.APIConfig) *Server {
	return &Server{
		alertQueue:        alertQueue,
		limitersByChannel: make(map[string]*rate.Limiter),
		limitersLock:      &sync.Mutex{},
		logger:            logger,
		metrics:           &types.NoopMetrics{},
		apiSettings:       &config.APISettings{},
		cfg:               cfg,
	}
}

// WithRawAlertConsumer registers a [FifoQueueConsumer] that reads alerts outside of
// the HTTP API (e.g. from SQS or Redis Streams). The consumer is started by [Server.Run]
// in a dedicated goroutine.
//
// Multiple raw alert consumers can be added.
//
// The server can receive alerts from both the main rest API and the raw alert consumers simultaneously.
//
// The queue item body must be a single JSON-serialized types.Alert. Prometheus webhooks are not supported here.
func (s *Server) WithRawAlertConsumer(consumer FifoQueueConsumer) *Server {
	s.rawAlertConsumers = append(s.rawAlertConsumers, consumer)
	return s
}

// WithCacheStore sets the cache store. If not called, [Server.Run] defaults to an
// in-process go-cache instance (unsuitable for multi-instance deployments that
// require shared channel-info caching). Passing nil is a no-op.
func (s *Server) WithCacheStore(cacheStore cachestore.StoreInterface) *Server {
	if cacheStore == nil {
		return s
	}
	s.cacheStore = cacheStore
	return s
}

// WithMetrics sets the metrics implementation. If not called, a no-op
// implementation is used. Passing nil is a no-op.
func (s *Server) WithMetrics(metrics types.Metrics) *Server {
	if metrics == nil {
		return s
	}
	s.metrics = metrics
	return s
}

// WithSettings sets the initial API settings. If not called, zero-value settings
// are used. For runtime updates after [Server.Run] is started, use
// [Server.UpdateSettings] instead. Passing nil is a no-op.
func (s *Server) WithSettings(settings *config.APISettings) *Server {
	if settings == nil {
		return s
	}
	s.apiSettings = settings
	return s
}

// WithHooks registers optional lifecycle callbacks for startup, readiness, and
// shutdown probe support. See [Hooks] for the available hooks and when
// they fire.
func (s *Server) WithHooks(hooks Hooks) *Server {
	s.hooks = hooks
	return s
}

// Run starts the HTTP server and handles incoming requests. It also initializes the Slack API client and the channel info syncer.
// If raw alert consumers are set, it will start dedicated consumers for those queues.
//
// This method blocks until the context is cancelled, or a server error occurs.
func (s *Server) Run(ctx context.Context) error {
	s.logger.Info("API server started")
	defer s.logger.Infof("API server exited")

	if s.alertQueue == nil {
		return errors.New("alert queue cannot be nil")
	}

	if s.cfg == nil {
		return errors.New("manager configuration cannot be nil")
	}

	if err := s.cfg.Validate(); err != nil {
		return fmt.Errorf("failed to validate manager configuration: %w", err)
	}

	if s.cfg.EncryptionKey == "" {
		s.logger.Error("No encryption key configured. The server will start, but alerts with webhook payloads will be rejected with HTTP 400.")
	}

	if err := s.apiSettings.InitAndValidate(s.logger); err != nil {
		return fmt.Errorf("failed to initialize API settings: %w", err)
	}

	if s.cacheStore == nil {
		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		s.cacheStore = gocache_store.NewGoCache(gocacheClient)
	}

	// Wrap the metrics implementation with the configured prefix so all internal metric
	// names are automatically namespaced (e.g. "slackmgr_http_server_request_duration_seconds").
	s.metrics = internal.NewPrefixedMetrics(s.metrics, s.cfg.MetricsPrefix)

	// Initialize Slack API client if not already set
	if s.slackClient == nil {
		slackAPI := internal.NewSlackAPIClient(s.cacheStore, s.cfg.CacheKeyPrefix, s.logger, s.metrics, s.cfg.SlackClient)

		if _, err := slackAPI.Connect(ctx); err != nil {
			return fmt.Errorf("failed to connect to Slack API: %w", err)
		}

		s.slackClient = slackAPI
	}

	// Initialize channel info provider if not already set
	if s.channelInfoProvider == nil {
		channelInfoSyncer := newChannelInfoSyncer(s.slackClient, s.logger)

		if err := channelInfoSyncer.Init(ctx); err != nil {
			return fmt.Errorf("failed to initialize channel info manager: %w", err)
		}

		s.channelInfoProvider = channelInfoSyncer
	}

	if s.hooks.OnStartup != nil {
		s.hooks.OnStartup()
	}

	if s.hooks.OnShutdown != nil {
		defer s.hooks.OnShutdown()
	}

	// Register HTTP server metrics.
	metricsLabels := []string{"path", "method", "status"}
	s.metrics.RegisterHistogram(httpRequestMetric, "Duration of incoming HTTP server requests in seconds",
		[]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}, metricsLabels...)
	s.metrics.RegisterGauge(httpInFlightMetric, "Number of HTTP requests currently being processed")
	s.metrics.GaugeSet(httpInFlightMetric, 0)

	// Set release mode to mute a few annoying startup logs.
	// The actual runtime mode is set below, depending on config.
	gin.SetMode(gin.ReleaseMode)

	// Create a blank Gin engine, without any middleware.
	engine := gin.New()

	// Add global metrics middleware.
	engine.Use(s.metricsMiddleware())

	// Add logging middleware. Custom JSON if configured, otherwise the default stdout logger.
	if s.cfg.LogJSON {
		engine.Use(s.jsonLogMiddleware()) // Custom JSON logger

		gin.DebugPrintFunc = func(format string, args ...any) {
			s.logger.Debugf("gin: "+format, args...)
		}

		gin.DebugPrintRouteFunc = func(httpMethod, absolutePath, handlerName string, nuHandlers int) {
			s.logger.Debugf("gin: %s %s --> %s (%d handlers)", httpMethod, absolutePath, handlerName, nuHandlers)
		}
	} else {
		engine.Use(gin.Logger()) // Default logger
	}

	// Add recovery middleware to recover from any panics and write a 500 if there was one.
	// We add this *after* the metrics and logging middleware, so that calls that trigger a panic are still logged and measured.
	// We just need to make sure that the metrics and logging middleware don't themselves panic...
	engine.Use(s.recoveryMiddleware())

	// Set runtime mode and default pretty printing based on verbose config.
	if s.cfg.Verbose {
		gin.SetMode(gin.DebugMode)
		s.defaultPretty = true
	} else {
		gin.SetMode(gin.ReleaseMode)
		s.defaultPretty = false
	}

	// Apply timeout middleware globally.
	// This MUST be added before routes are registered, otherwise it won't apply to them.
	engine.Use(timeout.New(
		timeout.WithTimeout(handlerTimeout),
		timeout.WithResponse(timeoutResponse),
	))

	// We support both /alert and /alerts endpoints for backwards compatibility.
	// Input is either a single alert (types.Alert), or an array of alerts.
	engine.POST("/alert", s.handleAlerts)
	engine.POST("/alert/:slackChannelId", s.handleAlerts)
	engine.POST("/alerts", s.handleAlerts)
	engine.POST("/alerts/:slackChannelId", s.handleAlerts)

	// Webhooks from Prometheus alert manager.
	// The alert format is defined in PrometheusWebhook.
	engine.POST("/prometheus-alert", s.handlePrometheusWebhook)
	engine.POST("/prometheus-alert/:slackChannelId", s.handlePrometheusWebhook)

	// Test endpoint for alerts, which writes the input body as an info log message.
	engine.POST("/alerts-test", s.handleAlertsTest)
	engine.POST("/alerts-test/:slackChannelId", s.handleAlertsTest)

	// Route mappings
	engine.GET("/mappings", s.handleMappings)

	// List channels managed by Slack Manager
	engine.GET("/channels", s.handleChannels)

	// Ping
	engine.GET("/ping", s.ping)

	// Calculate timeouts for the HTTP server and handler middleware.
	readTimeout := readHeaderTimeout + handlerTimeout + timeoutBuffer
	writeTimeout := handlerTimeout + timeoutBuffer

	srv := &http.Server{
		Addr:              ":" + s.cfg.RestPort,
		Handler:           engine,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	s.logger.
		WithField("read_header_timeout", fmt.Sprintf("%v", readHeaderTimeout)).
		WithField("read_timeout", fmt.Sprintf("%v", readTimeout)).
		WithField("handler_timeout", fmt.Sprintf("%v", handlerTimeout)).
		WithField("write_timeout", fmt.Sprintf("%v", writeTimeout)).
		WithField("idle_timeout", fmt.Sprintf("%v", idleTimeout)).
		WithField("port", s.cfg.RestPort).
		Info("Starting API listener")

	errg, ctx := errgroup.WithContext(ctx)

	// Start each alert consumer, if any.
	for _, consumer := range s.rawAlertConsumers {
		errg.Go(func() error {
			return s.runRawAlertConsumer(ctx, consumer)
		})
	}

	// Start the channel info syncer, if applicable.
	if syncer, ok := s.channelInfoProvider.(*channelInfoSyncer); ok {
		errg.Go(func() error {
			return syncer.Run(ctx)
		})
	}

	readyCh := make(chan struct{})

	errg.Go(func() error {
		ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
		if err != nil {
			return err
		}

		close(readyCh)

		// ErrServerClosed is the expected return value when the shutdown goroutine
		// calls srv.Shutdown() after ctx is cancelled. Treat it as a clean exit.
		if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	})

	errg.Go(func() error {
		select {
		case <-readyCh:
			if s.hooks.OnReady != nil {
				s.hooks.OnReady()
			}
		case <-ctx.Done():
			// Shutdown before port was bound; skip OnReady.
		}

		<-ctx.Done()

		if s.hooks.OnNotReady != nil {
			s.hooks.OnNotReady()
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(s.cfg.ShutdownTimeoutMs)*time.Millisecond)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // ctx is already cancelled here; a fresh context is required for the shutdown deadline.
			s.logger.Errorf("Failed to shut down http server: %s", err)
		}

		return ctx.Err()
	})

	return errg.Wait()
}

// UpdateSettings hot-reloads API settings without restarting. The new settings are
// validated before being applied; if validation fails the existing settings remain
// active and an error is returned. Passing nil replaces the current settings with
// zero-value defaults.
func (s *Server) UpdateSettings(settings *config.APISettings) error {
	if settings == nil {
		settings = &config.APISettings{}
	}

	if err := settings.InitAndValidate(s.logger); err != nil {
		return fmt.Errorf("failed to initialize updated API settings (the existing settings will continue to be used): %w", err)
	}

	s.apiSettings = settings

	s.logger.Infof("API settings updated")

	return nil
}

// setChannelInfoProvider allows tests to inject a mock ChannelInfoProvider.
// This setter is intended for testing only and should not be used in production code.
func (s *Server) setChannelInfoProvider(provider ChannelInfoProvider) {
	s.channelInfoProvider = provider
}

// runRawAlertConsumer starts consuming alerts from the given FIFO queue consumer.
// Each alert is expected to be a JSON-serialized types.Alert.
//
// Retryable processing errors result in the message being nacked, thus allowing re-processing later.
// Non-retryable processing errors result in the message being acked, thus avoiding re-processing.
//
// This method blocks until the context is cancelled, i.e. the consumer.Receive() method exits.
func (s *Server) runRawAlertConsumer(ctx context.Context, consumer FifoQueueConsumer) error {
	s.logger.Info("Starting raw alert consumer")
	defer s.logger.Info("Raw alert consumer exited")

	queueCh := make(chan *types.FifoQueueItem, 100)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return consumer.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case item, ok := <-queueCh:
				if !ok {
					return nil
				}

				logger := s.logger.WithField("message_id", item.MessageID).WithField("channel_id", item.SlackChannelID)
				logger.Debug("Alert received")

				var alert *types.Alert

				// Unmarshal the alert from the queue item body.
				// If the alert is invalid, we *ack* the message to avoid re-processing it.
				if err := json.Unmarshal([]byte(item.Body), &alert); err != nil {
					logger.Errorf("Failed to json unmarshal queued alert: %s", err)

					if item.Ack != nil {
						item.Ack()
						logger.Debug("Alert acked")
					}

					continue
				}

				// Process the alert.
				// If processing fails with a retryable error, we *nack* the message, thus allowing it to be re-processed later.
				if err := s.processQueuedAlert(ctx, alert); err != nil {
					logger.Errorf("Failed to process queued alert: %s", err)

					var pErr *processingError

					if errors.As(err, &pErr) && pErr.IsRetryable() {
						if item.Nack != nil {
							item.Nack()
							logger.Debug("Alert nacked")
						}

						continue
					}
				}

				// At this point, the alert has been processed successfully OR it failed with a non-retryable error.
				// In both cases, we ack the message to avoid re-processing it.
				if item.Ack != nil {
					item.Ack()
					logger.Debug("Alert acked")
				}
			}
		}
	})

	return errg.Wait()
}

func (s *Server) writeErrorResponse(c *gin.Context, err error, statusCode int, alert *types.Alert) {
	errorMsg := err.Error()

	if statusCode < 500 {
		s.logger.WithField("error", err.Error()).Info("Request failed")
	} else {
		s.logger.WithField("error", err.Error()).Error("Request failed")
		errorMsg = "Internal server error"
	}

	if len(errorMsg) > 1 {
		errorMsg = strings.ToUpper(string(errorMsg[0])) + errorMsg[1:]
	}

	c.JSON(statusCode, errorResponse{Error: errorMsg})

	if s.cfg.ErrorReportChannelID != "" {
		targetChannel := debugGetAlertChannelOrRouteKey(c, alert)
		debugFields := debugGetAlertFields(alert)

		alert := s.createClientErrorAlert(err, statusCode, debugFields, targetChannel)

		if err := s.queueAlert(c.Request.Context(), alert); err != nil {
			s.logger.Errorf("Failed to queue client error alert: %s", err)
		}
	}
}

// createClientErrorAlert creates an alert for reporting client errors to the configured Slack error report channel.
// This is an optional debug feature, used by Slack Manager admins to track client errors that may indicate misconfiguration or integration issues.
func (s *Server) createClientErrorAlert(err error, statusCode int, debugFields map[string]string, targetChannel string) *types.Alert {
	severity := types.AlertWarning

	if statusCode >= 500 {
		severity = types.AlertError
	}

	if targetChannel == "" {
		targetChannel = NA
	}

	alert := types.NewAlert(severity)

	alert.CorrelationID = fmt.Sprintf("__client_error_%s_%s", targetChannel, internal.Hash(err.Error()))
	alert.Header = fmt.Sprintf(":status: Client error %d", statusCode)
	alert.FallbackText = fmt.Sprintf("Client error %d", statusCode)
	alert.SlackChannelID = s.cfg.ErrorReportChannelID
	alert.IssueFollowUpEnabled = true
	alert.AutoResolveSeconds = 3600
	alert.ArchivingDelaySeconds = 24 * 3600

	alert.Text = fmt.Sprintf("*Target*: `%s`\n*Error*: `%s`", targetChannel, err.Error())

	for k, v := range debugFields {
		v = strings.ReplaceAll(v, "`", "")
		v = strings.ReplaceAll(v, "*", "")
		v = strings.ReplaceAll(v, "~", "")
		v = strings.ReplaceAll(v, "_", "")
		v = strings.ReplaceAll(v, ":status:", "")
		v = strings.ReplaceAll(v, "<", "")
		v = strings.ReplaceAll(v, ">", "")
		v = strings.ReplaceAll(v, "\n", " ")
		v = strings.TrimSpace(v)

		if len(v) > 100 {
			v = v[:97] + "..."
		}

		alert.Text += fmt.Sprintf("\n*%s*: `%s`", k, v)
	}

	return alert
}

// queueAlert serializes the alert and sends it to the alert queue.
// Any returned errors are considered retryable, as they typically indicate transient queuing issues.
func (s *Server) queueAlert(ctx context.Context, alert *types.Alert) error {
	// Serialize the alert to JSON
	// This really can't fail, but if it does, we log the error and return nil.
	// Errors from this method are supposed to indicate retryable queuing errors, not fatal serialization errors.
	body, err := json.Marshal(alert)
	if err != nil {
		s.logger.Errorf("Failed to marshal alert: %w", err)
		return nil
	}

	// Send the alert to the queue. Any errors here are retryable and thus returned.
	if err := s.alertQueue.Send(ctx, alert.SlackChannelID, alert.UniqueID(), string(body)); err != nil {
		return fmt.Errorf("failed to send message to queue: %w", err)
	}

	return nil
}

func (s *Server) checkRateLimit(channel string, count int) (bool, time.Duration) {
	r := s.getRateLimiter(channel).ReserveN(time.Now(), count)

	delay := r.Delay()

	if delay == rate.InfDuration || delay > 0 {
		r.Cancel()
		if delay == rate.InfDuration {
			delay = maxRetryAfterDelay
		}
		return false, delay
	}

	return true, 0
}

// rateLimitInfo holds a snapshot of the rate limiter state for RateLimit-* headers.
type rateLimitInfo struct {
	limit     int
	remaining int
	resetSecs int
}

// getRateLimitInfo returns a post-reservation snapshot of the limiter for channel.
// Call after a successful checkRateLimit so remaining reflects consumed tokens.
func (s *Server) getRateLimitInfo(channel string) rateLimitInfo {
	l := s.getRateLimiter(channel)
	tokens := l.Tokens()
	burst := l.Burst()

	remaining := max(int(tokens), 0)

	var resetSecs int
	if lim := float64(l.Limit()); lim > 0 && tokens < float64(burst) {
		deficit := float64(burst) - tokens
		deficitDur := time.Duration(deficit / lim * float64(time.Second))
		resetSecs = int((deficitDur + time.Second - 1) / time.Second)
	}

	return rateLimitInfo{limit: burst, remaining: remaining, resetSecs: resetSecs}
}

func (s *Server) setRateLimitHeaders(c *gin.Context, info rateLimitInfo) {
	c.Header("Ratelimit-Limit", strconv.Itoa(info.limit))
	c.Header("Ratelimit-Remaining", strconv.Itoa(info.remaining))
	c.Header("Ratelimit-Reset", strconv.Itoa(info.resetSecs))
}

func (s *Server) getRateLimiter(channel string) *rate.Limiter {
	s.limitersLock.Lock()
	defer s.limitersLock.Unlock()

	limiter, ok := s.limitersByChannel[channel]

	if !ok {
		limiter = rate.NewLimiter(rate.Limit(s.cfg.RateLimitPerAlertChannel.AlertsPerSecond), s.cfg.RateLimitPerAlertChannel.AllowedBurst)
		s.limitersByChannel[channel] = limiter
	}

	return limiter
}

func (s *Server) debugLogRequest(c *gin.Context, body []byte) {
	s.logger.WithField("body", string(body)).Debugf("%s %s", c.Request.Method, c.Request.URL.Path)
}

// jsonLogMiddleware is a Gin middleware that logs HTTP requests in JSON format.
// It captures details such as client IP, request duration, method, path, and status code.
// Responses with status codes 500 and above are logged as errors, all others as info.
func (s *Server) jsonLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		// Process the request
		c.Next()

		duration := time.Since(start)

		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		if raw != "" {
			path = path + "?" + raw
		}

		errors := c.Errors.String()

		status := c.Writer.Status()

		logger := s.logger.
			WithField("client_ip", c.ClientIP()).
			WithField("duration", duration).
			WithField("method", c.Request.Method).
			WithField("path", path).
			WithField("status", status)

		if errors != "" {
			logger = logger.WithField("errors", errors)
		}

		if status >= 500 {
			logger.Error("Request")
		} else {
			logger.Info("Request")
		}
	}
}

// metricsMiddleware is a Gin middleware that records HTTP request durations, status
// codes, and the number of in-flight requests.
func (s *Server) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.metrics.GaugeAdd(httpInFlightMetric, 1)
		defer s.metrics.GaugeAdd(httpInFlightMetric, -1)

		start := time.Now()

		c.Next() // process request

		method := c.Request.Method
		path := c.FullPath() // uses route patterns like "/users/:id"

		// Ensure that the path is never empty (happens with 404s)
		if path == "" {
			path = "unmatched"
		}

		s.metrics.Observe(httpRequestMetric, time.Since(start).Seconds(), path, method, strconv.Itoa(c.Writer.Status()))
	}
}

// recoveryMiddleware is a Gin middleware that recovers from any panics and writes a 500 if there was one.
// Partially adapted from the Gin framework recovery middleware.
func (s *Server) recoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				// Check for a broken connection, as it is not really a
				// condition that warrants a panic stack trace.
				var brokenPipe bool

				if ne, ok := rec.(*net.OpError); ok {
					var se *os.SyscallError
					if errors.As(ne, &se) {
						seStr := strings.ToLower(se.Error())
						if strings.Contains(seStr, "broken pipe") || strings.Contains(seStr, "connection reset by peer") {
							brokenPipe = true
						}
					}
				}

				const stackSkip = 3

				if brokenPipe {
					s.logger.WithField("error", rec).Error("Connection error")
				} else {
					if s.cfg.LogJSON {
						s.logger.WithField("error", rec).WithField("stack", stack(stackSkip)).Error("Panic recovered")
					} else {
						s.logger.Errorf("Panic recovered:\n%s\n%s", rec, stack(stackSkip))
					}
				}

				if brokenPipe {
					// If the connection is dead, we can't write a status to it.
					c.Abort()
				} else {
					// If panic occurred but connection is still alive, return HTTP 500
					c.AbortWithStatusJSON(http.StatusInternalServerError, errorResponse{Error: "Internal server error"})
				}
			}
		}()

		c.Next()
	}
}

// timeoutResponse is called when a request times out. It sends a 503 Service Unavailable response with a "timeout" message.
func timeoutResponse(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, errorResponse{Error: "Request timeout"})
}

func debugGetAlertChannelOrRouteKey(c *gin.Context, alert *types.Alert) string {
	if alert.SlackChannelID != "" {
		return fmt.Sprintf("%s (channel ID from alert body)", alert.SlackChannelID)
	}

	channelIDFromParam := c.Param("slackChannelId")

	if channelIDFromParam != "" {
		return fmt.Sprintf("%s (channel ID from URL param)", channelIDFromParam)
	}

	if alert.RouteKey != "" {
		return fmt.Sprintf("%s (route key)", alert.RouteKey)
	}

	return "[no channel ID or route key found]"
}

func debugGetAlertFields(alert *types.Alert) map[string]string {
	if alert == nil {
		return nil
	}

	header := alert.Header

	if len(header) > 200 {
		header = header[:200] + "..."
	}

	body := alert.Text

	if len(body) > 1000 {
		body = body[:1000] + "..."
	}

	return map[string]string{
		"CorrelationId": alert.CorrelationID,
		"Header":        header,
		"Body":          body,
	}
}

// stack returns a nicely formatted stack frame, skipping skip frames.
// Borrowed from the gin framework recovery.go file.
func stack(skip int) string {
	buf := new(bytes.Buffer) // the returned data
	// As we loop, we open files and read them. These variables record the currently
	// loaded file.
	var lines [][]byte
	var lastFile string
	for i := skip; ; i++ { // Skip the expected number of frames
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		// Print this much at least.  If we can't find the source, it won't show.
		fmt.Fprintf(buf, "%s:%d (0x%x)\n", file, line, pc)
		if file != lastFile {
			data, err := os.ReadFile(file) // #nosec G304 -- file path comes from runtime.Caller(), not user input
			if err != nil {
				continue
			}
			lines = bytes.Split(data, []byte{'\n'})
			lastFile = file
		}
		fmt.Fprintf(buf, "\t%s: %s\n", function(pc), source(lines, line))
	}
	return buf.String()
}

// source returns a space-trimmed slice of the n'th line.
// Borrowed from the gin framework recovery.go file.
func source(lines [][]byte, n int) []byte {
	n-- // in stack trace, lines are 1-indexed but our array is 0-indexed
	if n < 0 || n >= len(lines) {
		return []byte("n/a")
	}
	return bytes.TrimSpace(lines[n])
}

// function returns, if possible, the name of the function containing the PC.
// Borrowed from the gin framework recovery.go file.
func function(pc uintptr) string {
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "n/a"
	}
	name := fn.Name()
	// The name includes the path name to the package, which is unnecessary
	// since the file name is already included.  Plus, it has center dots.
	// That is, we see
	//	runtime/debug.*T·ptrmethod
	// and want
	//	*T.ptrmethod
	// Also the package path might contain dot (e.g. code.google.com/...),
	// so first eliminate the path prefix
	if lastSlash := strings.LastIndexByte(name, '/'); lastSlash >= 0 {
		name = name[lastSlash+1:]
	}
	if period := strings.IndexByte(name, '.'); period >= 0 {
		name = name[period+1:]
	}
	name = strings.ReplaceAll(name, "·", ".")
	return name
}
