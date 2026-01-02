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
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

const (
	httpRequestMetric = "http_server_request_duration_seconds"
)

type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error
}

type FifoQueueProducer interface {
	Send(ctx context.Context, slackChannelID, dedupID, body string) error
}

type Server struct {
	rawAlertConsumers []FifoQueueConsumer
	alertQueue        FifoQueueProducer
	limitersByChannel map[string]*rate.Limiter
	limitersLock      *sync.Mutex
	cacheStore        cachestore.StoreInterface
	slackAPI          *slackapi.Client
	channelInfoSyncer *channelInfoSyncer
	logger            common.Logger
	metrics           common.Metrics
	apiSettings       *config.APISettings
	cfg               *config.APIConfig
	defaultPretty     bool
}

func New(alertQueue FifoQueueProducer, cacheStore cachestore.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.APIConfig, settings *config.APISettings) *Server {
	if cacheStore == nil {
		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		cacheStore = gocache_store.NewGoCache(gocacheClient)
	}

	if metrics == nil {
		metrics = &common.NoopMetrics{}
	}

	if settings == nil {
		settings = &config.APISettings{}
	}

	return &Server{
		alertQueue:        alertQueue,
		limitersByChannel: make(map[string]*rate.Limiter),
		limitersLock:      &sync.Mutex{},
		cacheStore:        cacheStore,
		logger:            logger,
		metrics:           metrics,
		apiSettings:       settings,
		cfg:               cfg,
	}
}

// WithRawAlertConsumer defines an alternative alert consumer, which reads from a FIFO queue and processes the items similarly to the main rest API.
// The consumer is started by Run(ctx), and the queue is consumed in a separate goroutine.
//
// Multiple raw alert consumers can be added.
//
// The server can receive alerts from both the main rest API and the raw alert consumers simultaneously.
//
// The queue item body must be a single JSON-serialized common.Alert. Prometheus webhooks are not supported here.
func (s *Server) WithRawAlertConsumer(consumer FifoQueueConsumer) *Server {
	s.rawAlertConsumers = append(s.rawAlertConsumers, consumer)
	return s
}

// Run starts the HTTP server and handles incoming requests. It also initializes the Slack API client and the channel info syncer.
// If a raw alert consumer is set, it will start a dedicated consumer for that queue.
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

	if err := s.apiSettings.InitAndValidate(s.logger); err != nil {
		return fmt.Errorf("failed to initialize API settings: %w", err)
	}

	s.slackAPI = slackapi.New(s.cacheStore, s.cfg.CacheKeyPrefix, s.logger, s.metrics, s.cfg.SlackClient)

	if _, err := s.slackAPI.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect to Slack API: %w", err)
	}

	s.channelInfoSyncer = newChannelInfoSyncer(s.slackAPI, s.logger)

	if err := s.channelInfoSyncer.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize channel info manager: %w", err)
	}

	// Register prometheus histogram metric for HTTP request durations
	metricsLabels := []string{"path", "method", "status"}
	s.metrics.RegisterHistogram(httpRequestMetric, "The duration of incoming HTTP server requests in seconds",
		[]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}, metricsLabels...)

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
	// We add this *after* the metrcics and logging middleware, so that calls that trigger a panic are still logged and measured.
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

	// We support both /alert and /alerts endpoints for backwards compatibility.
	// Input is either a single alert (common.Alert), or an array of alerts.
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

	readHeaderTimeout := 5 * time.Second
	handlerTimeout := s.getHandlerTimeout()
	timeoutWiggleRoom := time.Second
	readTimeout := readHeaderTimeout + handlerTimeout + timeoutWiggleRoom
	writeTimeout := handlerTimeout + timeoutWiggleRoom

	// Apply timeout middleware globally
	engine.Use(timeout.New(
		timeout.WithTimeout(handlerTimeout),
		timeout.WithResponse(timeoutResponse),
	))

	srv := &http.Server{
		Addr:              ":" + s.cfg.RestPort,
		Handler:           engine,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}

	s.logger.
		WithField("read_header_timeout", fmt.Sprintf("%v", srv.ReadHeaderTimeout)).
		WithField("read_timeout", fmt.Sprintf("%v", srv.ReadTimeout)).
		WithField("handler_timeout", fmt.Sprintf("%v", handlerTimeout)).
		WithField("write_timeout", fmt.Sprintf("%v", srv.WriteTimeout)).
		WithField("idle_timeout", fmt.Sprintf("%v", srv.IdleTimeout)).
		WithField("port", s.cfg.RestPort).
		Info("Starting API listener")

	errg, ctx := errgroup.WithContext(ctx)

	// Start each alert consumer, if any.
	for _, consumer := range s.rawAlertConsumers {
		errg.Go(func() error {
			return s.runRawAlertConsumer(ctx, consumer)
		})
	}

	errg.Go(func() error {
		return s.channelInfoSyncer.Run(ctx)
	})

	errg.Go(func() error {
		return srv.ListenAndServe()
	})

	errg.Go(func() error {
		<-ctx.Done()

		if err := srv.Close(); err != nil {
			s.logger.Errorf("Failed to close http server: %s", err)
		}

		return ctx.Err()
	})

	if err := errg.Wait(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	}

	return nil
}

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

// runRawAlertConsumer starts consuming alerts from the given FIFO queue consumer.
// Each alert is expected to be a JSON-serialized common.Alert.
//
// Retryable processing errors result in the message being nacked, thus allowing re-processing later.
// Non-retryable processing errors result in the message being acked, thus avoiding re-processing.
//
// This method blocks until the context is cancelled, i.e. the consumer.Receive() method exits.
func (s *Server) runRawAlertConsumer(ctx context.Context, consumer FifoQueueConsumer) error {
	s.logger.Info("Starting raw alert consumer")
	defer s.logger.Info("Raw alert consumer exited")

	queueCh := make(chan *common.FifoQueueItem, 100)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return consumer.Receive(ctx, queueCh)
	})

	errg.Go(func() error {
		for item := range queueCh {
			logger := s.logger.WithField("message_id", item.MessageID).WithField("channel_id", item.SlackChannelID)
			logger.Debug("Alert received")

			var alert *common.Alert

			// Unmarshal the alert from the queue item body.
			// If the alert is invalid, we *ack* the message to avoid re-processing it.
			if err := json.Unmarshal([]byte(item.Body), &alert); err != nil {
				logger.Errorf("Failed to json unmarshal queued alert: %s", err)

				if item.Ack != nil {
					item.Ack(ctx)
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
						item.Nack(ctx)
						logger.Debug("Alert nacked")
					}

					continue
				}
			}

			// At this point, the alert has been processed successfully OR it failed with a non-retryable error.
			// In both cases, we ack the message to avoid re-processing it.
			if item.Ack != nil {
				item.Ack(ctx)
				logger.Debug("Alert acked")
			}
		}

		return nil
	})

	return errg.Wait()
}

func (s *Server) writeErrorResponse(c *gin.Context, clientErr error, statusCode int, alert *common.Alert) {
	errText := clientErr.Error()

	if len(errText) > 1 {
		errText = strings.ToUpper(string(errText[0])) + errText[1:]
	}

	if statusCode < 500 {
		s.logger.Infof("Request failed: %s", errText)
	} else {
		s.logger.Errorf("Request failed: %s", errText)
	}

	c.Header("Content-Type", "text/plain")
	c.Status(statusCode)

	if _, err := c.Writer.Write([]byte(errText)); err != nil {
		s.logger.Errorf("Failed to write error response: %s", err)
	}

	if s.cfg.ErrorReportChannelID != "" {
		targetChannel := debugGetAlertChannelOrRouteKey(c, alert)
		debugFields := debugGetAlertFields(alert)

		alert := s.createClientErrorAlert(clientErr, statusCode, debugFields, targetChannel)

		if err := s.queueAlert(c.Request.Context(), alert); err != nil {
			s.logger.Errorf("Failed to queue client error alert: %s", err)
		}
	}
}

// createClientErrorAlert creates an alert for reporting client errors to the configured Slack error report channel.
// This is an optional debug feature, used by Slack Manager admins to track client errors that may indicate misconfiguration or integration issues.
func (s *Server) createClientErrorAlert(err error, statusCode int, debugFields map[string]string, targetChannel string) *common.Alert {
	severity := common.AlertWarning

	if statusCode >= 500 {
		severity = common.AlertError
	}

	if targetChannel == "" {
		targetChannel = NA
	}

	alert := common.NewAlert(severity)

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
func (s *Server) queueAlert(ctx context.Context, alert *common.Alert) error {
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

func (s *Server) waitForRateLimit(ctx context.Context, channel string, count int, failOnRateLimitError bool) (int, error) {
	limiter := s.getRateLimiter(channel)
	attempt := 1

	for {
		timeout := time.Duration(s.cfg.RateLimit.MaxWaitPerAttemptSeconds) * time.Second
		ctx, cancel := context.WithTimeout(ctx, timeout)

		defer cancel()

		err := limiter.WaitN(ctx, count)

		if err == nil {
			return count, nil
		}

		// Something other than rate limiting is going on
		if !strings.Contains(err.Error(), "rate") {
			return 0, fmt.Errorf("failed to wait for rate limit: %w", err)
		}

		// Stop trying after configured number of events
		if attempt >= s.cfg.RateLimit.MaxAttempts {
			return 0, fmt.Errorf("rate limit exceeded for %d alerts in channel %s", count, channel)
		}

		attempt++

		// Reduce the number of permits to wait for, IF failOnRateLimitError is false
		if count > 3 && !failOnRateLimitError {
			count /= 2
		}
	}
}

func (s *Server) getRateLimiter(channel string) *rate.Limiter {
	s.limitersLock.Lock()
	defer s.limitersLock.Unlock()

	limiter, ok := s.limitersByChannel[channel]

	if !ok {
		limiter = rate.NewLimiter(rate.Limit(s.cfg.RateLimit.AlertsPerSecond), s.cfg.RateLimit.AllowedBurst)
		s.limitersByChannel[channel] = limiter
	}

	return limiter
}

func (s *Server) debugLogRequest(c *gin.Context, body []byte) {
	s.logger.WithField("body", string(body)).Debugf("%s %s", c.Request.Method, c.Request.URL.Path)
}

// getHandlerTimeout calculates the timeout duration for request handlers based on rate limit settings.
// It ensures that the timeout is sufficient to handle the maximum wait time due to rate limiting, plus a small buffer.
// The timeout is never less than 30 seconds.
func (s *Server) getHandlerTimeout() time.Duration {
	apiRateLimitMaxTimeSeconds := (s.cfg.RateLimit.MaxWaitPerAttemptSeconds * s.cfg.RateLimit.MaxAttempts) + 3

	timeoutSeconds := max(
		apiRateLimitMaxTimeSeconds,
		30,
	)

	return time.Duration(timeoutSeconds) * time.Second
}

// jsonLogMiddleware is a Gin middleware that logs HTTP requests in JSON format.
// It captures details such as client IP, request duration, method, path, and status code.
// Responses with status codes 500 and above are logged as errors, all others as info.
func (s *Server) jsonLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.logger.Debug("LOG MIDDLEWARE")

		start := time.Now()

		// Process the request
		c.Next()

		duration := time.Since(start)

		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		if raw != "" {
			path = path + "?" + raw
		}

		msg := c.Errors.String()

		if msg == "" {
			msg = "Request"
		}

		status := c.Writer.Status()

		logger := s.logger.
			WithField("client_ip", c.ClientIP()).
			WithField("duration", duration).
			WithField("method", c.Request.Method).
			WithField("path", path).
			WithField("status", status)

		if status >= 500 {
			logger.Error(msg)
		} else {
			logger.Info(msg)
		}
	}
}

func (s *Server) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		s.logger.Debug("METRICS MIDDLEWARE")

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
					s.logger.WithField("error", rec).WithField("stack", stack(stackSkip)).Error("Panic recovered")
				}

				if brokenPipe {
					// If the connection is dead, we can't write a status to it.
					c.Abort()
				} else {
					// If panic occurred but connection is still alive, return HTTP 500
					c.AbortWithStatus(http.StatusInternalServerError)
				}
			}
		}()

		// Continue down the chain
		c.Next()
	}
}

// timeoutResponse is called when a request times out. It sends a 503 Service Unavailable response with a "timeout" message.
func timeoutResponse(c *gin.Context) {
	c.String(http.StatusServiceUnavailable, "timeout")
}

func debugGetAlertChannelOrRouteKey(c *gin.Context, alert *common.Alert) string {
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

func debugGetAlertFields(alert *common.Alert) map[string]string {
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
func stack(skip int) []byte {
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
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			lines = bytes.Split(data, []byte{'\n'})
			lastFile = file
		}
		fmt.Fprintf(buf, "\t%s: %s\n", function(pc), source(lines, line))
	}
	return buf.Bytes()
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
