package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	cachestore "github.com/eko/gocache/lib/v4/store"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

type FifoQueueProducer interface {
	Send(ctx context.Context, groupID, dedupID, body string) error
}

type Server struct {
	alertQueue         FifoQueueProducer
	limitersByChannel  map[string]*rate.Limiter
	limitersLock       *sync.Mutex
	cfg                *config.APIConfig
	cache              *internal.Cache[string]
	channelInfoManager *channelInfoManager
	metrics            common.Metrics
	logger             common.Logger
	alertMapping       *config.AlertMapping
}

func New(alertQueue FifoQueueProducer, cacheStore cachestore.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.APIConfig, alertMapping *config.AlertMapping) *Server {
	if metrics == nil {
		metrics = &internal.NoopMetrics{}
	}

	if alertMapping == nil {
		alertMapping = &config.AlertMapping{}
	}

	slackAPI := slackapi.New(cacheStore, cfg.CachePrefix, logger, metrics, cfg.SlackClient)
	channelInfoManager := newChannelInfoManager(slackAPI, logger)
	cache := internal.NewCache(cache.New[string](cacheStore), logger)

	return &Server{
		alertQueue:         alertQueue,
		limitersByChannel:  make(map[string]*rate.Limiter),
		limitersLock:       &sync.Mutex{},
		cache:              cache,
		channelInfoManager: channelInfoManager,
		metrics:            metrics,
		logger:             logger,
		alertMapping:       alertMapping,
		cfg:                cfg,
	}
}

func (s *Server) UpdateAlertMapping(alertMapping *config.AlertMapping) error {
	if alertMapping == nil {
		s.alertMapping = &config.AlertMapping{}
		return nil
	}

	if err := alertMapping.InitAndValidate(); err != nil {
		return fmt.Errorf("failed to update alert mapping: %w", err)
	}

	s.alertMapping = alertMapping

	return nil
}

// Run starts the server and handles incoming requests. This method blocks until the context is cancelled, or a server error occurs.
func (s *Server) Run(ctx context.Context) error {
	if err := s.cfg.Validate(); err != nil {
		return fmt.Errorf("failed to validate configuration: %w", err)
	}

	if err := s.alertMapping.InitAndValidate(); err != nil {
		return fmt.Errorf("failed to initialize alert mapping: %w", err)
	}

	if err := s.channelInfoManager.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize channel info manager: %w", err)
	}

	router := mux.NewRouter()

	// Stateful alerts
	router.HandleFunc("/alert", s.alert).Methods("POST")
	router.HandleFunc("/alert/{slackChannelId}", s.alert).Methods("POST")
	router.HandleFunc("/alerts", s.alerts).Methods("POST")
	router.HandleFunc("/alerts/{slackChannelId}", s.alerts).Methods("POST")

	// Prometheus alert manager
	router.HandleFunc("/prometheus-alert", s.prometheusAlert).Methods("POST")
	router.HandleFunc("/prometheus-alert/{slackChannelId}", s.prometheusAlert).Methods("POST")

	// Test endpoint for stateful alerts, which writes the input message as an info log message
	router.HandleFunc("/alerts-test", s.testSlackAlertsHandler).Methods("POST")
	router.HandleFunc("/alerts-test/{slackChannelId}", s.testSlackAlertsHandler).Methods("POST")

	// Route mappings
	router.HandleFunc("/mappings", s.mappings).Methods("GET")

	// List channels managed by Slack Manager
	router.HandleFunc("/channels", s.channels).Methods("GET")

	// Ping
	router.HandleFunc("/ping", s.ping).Methods("GET")

	readHeaderTimeout := 2 * time.Second
	requestTimeout := s.getRequestTimeout()
	timeoutWiggleRoom := time.Second

	var handler http.Handler

	if loggingHandler := s.logger.HttpLoggingHandler(); loggingHandler != nil {
		handler = handlers.LoggingHandler(loggingHandler, router)
	} else {
		handler = router
	}

	timeoutHandler := http.TimeoutHandler(handler, requestTimeout, "Handler timeout")
	listenAddr := fmt.Sprintf(":%s", s.cfg.RestPort)

	srv := &http.Server{
		Handler:           timeoutHandler,
		Addr:              listenAddr,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readHeaderTimeout + requestTimeout + timeoutWiggleRoom,
		WriteTimeout:      requestTimeout + timeoutWiggleRoom,
		IdleTimeout:       60 * time.Second,
	}

	s.logger.
		WithField("write_timeout", fmt.Sprintf("%v", srv.WriteTimeout)).
		WithField("read_timeout", fmt.Sprintf("%v", srv.ReadTimeout)).
		WithField("read_header_timeout", fmt.Sprintf("%v", srv.ReadHeaderTimeout)).
		WithField("idle_timeout", fmt.Sprintf("%v", srv.IdleTimeout)).
		WithField("request_timeout", fmt.Sprintf("%v", requestTimeout)).
		Infof("API available on port %s", s.cfg.RestPort)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return s.channelInfoManager.Run(ctx)
	})

	errg.Go(func() error {
		return srv.ListenAndServe()
	})

	errg.Go(func() error {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			s.logger.Errorf("Failed to close http server: %s", err)
		}
		return nil
	})

	if err := errg.Wait(); err != nil {
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}

	return nil
}

func (s *Server) mappings(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	data, err := json.MarshalIndent(s.alertMapping, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal alert mappings: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	resp.Header().Add("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)

	_, err = resp.Write(data)
	if err != nil {
		s.logger.Errorf("Failed to write mappings response: %s", err)
	}

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}

func (s *Server) channels(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	channels := s.channelInfoManager.ManagedChannels()

	data, err := json.MarshalIndent(channels, "", "  ")
	if err != nil {
		err = fmt.Errorf("failed to marshal channel list: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	resp.Header().Add("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)

	_, err = resp.Write(data)
	if err != nil {
		s.logger.Errorf("Failed to write channel list response: %s", err)
	}

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}

func (s *Server) ping(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	resp.Header().Add("Content-Type", "text/plain")
	resp.WriteHeader(http.StatusOK)

	_, err := resp.Write([]byte("pong"))
	if err != nil {
		s.logger.Errorf("Failed to write ping response: %s", err)
	}

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, http.StatusOK, time.Since(started))
}

func (s *Server) writeErrorResponse(ctx context.Context, clientErr error, statusCode int, debugText map[string]string, targetChannel string, resp http.ResponseWriter, req *http.Request, started time.Time) {
	errText := clientErr.Error()

	if len(errText) > 1 {
		errText = strings.ToUpper(string(errText[0])) + errText[1:]
	}

	if statusCode < 500 {
		s.logger.Infof("Request failed: %s", errText)
	} else {
		s.logger.Errorf("Request failed: %s", errText)
	}

	resp.Header().Add("Content-Type", "text/plain")
	resp.WriteHeader(statusCode)

	if _, err := resp.Write([]byte(errText)); err != nil {
		s.logger.Errorf("Failed to write error response: %s", err)
	}

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, statusCode, time.Since(started))

	if s.cfg.ErrorReportChannelID != "" {
		alert := s.createClientErrorAlert(clientErr, statusCode, debugText, targetChannel)

		if err := s.queueAlert(ctx, alert); err != nil {
			s.logger.Errorf("Failed to queue client error alert: %s", err)
		}
	}
}

func (s *Server) queueAlert(ctx context.Context, alert *common.Alert) error {
	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}

	groupID := alert.SlackChannelID
	dedupID := internal.Hash("alert", alert.SlackChannelID, alert.CorrelationID, alert.Timestamp.Format(time.RFC3339Nano))

	if err := s.alertQueue.Send(ctx, groupID, dedupID, string(body)); err != nil {
		return fmt.Errorf("failed to send message to queue: %w", err)
	}

	return nil
}

func (s *Server) waitForRateLimit(ctx context.Context, channel string, count int, failOnRateLimitError bool) (int, error) {
	limiter := s.getRateLimiter(channel)
	attempt := 1

	for {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.RateLimit.MaxWaitPerAttempt)

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

func (s *Server) debugLogRequest(req *http.Request, body []byte) {
	s.logger.WithField("body", string(body)).Debugf("%s %s", req.Method, req.URL.Path)
}

func (s *Server) getRequestTimeout() time.Duration {
	apiRateLimitMaxTime := s.cfg.RateLimit.MaxWaitPerAttempt * time.Duration(s.cfg.RateLimit.MaxAttempts)
	return max(apiRateLimitMaxTime, s.cfg.SlackClient.MaxRateLimitErrorWaitTime, s.cfg.SlackClient.MaxTransientErrorWaitTime, s.cfg.SlackClient.MaxFatalErrorWaitTime) + time.Second
}

func max(values ...time.Duration) time.Duration {
	max := time.Duration(0)

	for _, v := range values {
		if v > max {
			max = v
		}
	}

	return max
}
