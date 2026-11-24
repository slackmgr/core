package restapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Plain text API keys of the test settings (see newAuthTestServer).
const (
	testKeyWriter = "writer-key"
	testKeyReader = "reader-key"
	testKeyAdmin  = "admin-key"
)

// logEntry is a log message recorded by capturingLogger.
type logEntry struct {
	level  string
	msg    string
	fields map[string]any
}

// capturingLogger is a types.Logger that records all log messages with their fields.
type capturingLogger struct {
	mu      *sync.Mutex
	entries *[]logEntry
	fields  map[string]any
}

func newCapturingLogger() *capturingLogger {
	return &capturingLogger{mu: &sync.Mutex{}, entries: &[]logEntry{}, fields: map[string]any{}}
}

func (l *capturingLogger) Debug(msg string)               { l.log("debug", msg) }
func (l *capturingLogger) Debugf(format string, a ...any) { l.log("debug", fmt.Sprintf(format, a...)) }
func (l *capturingLogger) Info(msg string)                { l.log("info", msg) }
func (l *capturingLogger) Infof(format string, a ...any)  { l.log("info", fmt.Sprintf(format, a...)) }
func (l *capturingLogger) Error(msg string)               { l.log("error", msg) }
func (l *capturingLogger) Errorf(format string, a ...any) { l.log("error", fmt.Sprintf(format, a...)) }

func (l *capturingLogger) WithField(k string, v any) types.Logger {
	return l.WithFields(map[string]any{k: v})
}

func (l *capturingLogger) WithFields(fields map[string]any) types.Logger {
	merged := maps.Clone(l.fields)
	maps.Copy(merged, fields)

	return &capturingLogger{mu: l.mu, entries: l.entries, fields: merged}
}

func (l *capturingLogger) log(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	*l.entries = append(*l.entries, logEntry{level: level, msg: msg, fields: maps.Clone(l.fields)})
}

// find returns the first entry with the given message, or nil.
func (l *capturingLogger) find(msg string) *logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, e := range *l.entries {
		if e.msg == msg {
			return &e
		}
	}

	return nil
}

func testAPIKeySettings(t *testing.T) *config.APISettings {
	t.Helper()

	settings := &config.APISettings{APIKeys: []*config.APIKey{
		{ID: "writer", KeySHA256: config.HashAPIKey(testKeyWriter), Scopes: []string{types.ScopeAlertsWrite}},
		{ID: "reader", KeySHA256: config.HashAPIKey(testKeyReader), Scopes: []string{types.ScopeConfigRead}},
		{ID: "admin", KeySHA256: config.HashAPIKey(testKeyAdmin), Scopes: []string{types.ScopeAll}},
	}}
	require.NoError(t, settings.InitAndValidate(&mockLogger{}))

	return settings
}

// newAuthTestServer returns a test server with API key auth enabled and the test API keys, and recording metrics.
func newAuthTestServer(t *testing.T) (*Server, *recordingMetrics) {
	t.Helper()

	server := newTestServerWithChannels(t)
	server.WithAPIKeyAuth()
	server.apiSettings.Store(testAPIKeySettings(t))

	metrics := newRecordingMetrics()
	server.metrics = metrics

	return server, metrics
}

// newTestServerWithChannels returns a test server whose channel info provider has no managed channels.
func newTestServerWithChannels(t *testing.T) *Server {
	t.Helper()

	server, _, channelInfo := newTestServer(t)
	channelInfo.On("ManagedChannels").Return([]*internal.ChannelSummary{})

	return server
}

func bearer(token string) string {
	return "Bearer " + token
}

func basic(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// serve sends a request to an engine with all routes of the server, and returns the response.
func serve(t *testing.T, server *Server, method, path, authorization string) *httptest.ResponseRecorder {
	t.Helper()

	engine := gin.New()
	server.registerRoutes(engine)

	return serveEngine(t, engine, method, path, authorization)
}

func serveEngine(t *testing.T, engine *gin.Engine, method, path, authorization string) *httptest.ResponseRecorder {
	t.Helper()

	var body *strings.Reader
	if method == http.MethodPost {
		body = strings.NewReader("{") // unparseable, so that authorized alert requests get 400
	} else {
		body = strings.NewReader("")
	}

	req := httptest.NewRequestWithContext(t.Context(), method, path, body)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}

	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	return w
}

func TestAPIKeyFromRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		header  string
		wantKey string
		wantErr error
	}{
		{name: "no header", wantErr: types.ErrNoCredentials},
		{name: "blank header", header: "   ", wantErr: types.ErrNoCredentials},
		{name: "bearer", header: "Bearer abc", wantKey: "abc"},
		{name: "bearer lower case scheme", header: "bearer abc", wantKey: "abc"},
		{name: "bearer extra spaces", header: "Bearer   abc  ", wantKey: "abc"},
		{name: "bearer without token", header: "Bearer", wantErr: types.ErrInvalidCredentials},
		{name: "bearer with blank token", header: "Bearer    ", wantErr: types.ErrInvalidCredentials},
		{name: "basic password", header: basic("user", "abc"), wantKey: "abc"},
		{name: "basic password with colon", header: basic("user", "a:b"), wantKey: "a:b"},
		{name: "basic username only", header: basic("abc", ""), wantKey: "abc"},
		{name: "basic without colon", header: "Basic " + base64.StdEncoding.EncodeToString([]byte("abc")), wantKey: "abc"},
		{name: "basic lower case scheme", header: strings.Replace(basic("", "abc"), "Basic", "basic", 1), wantKey: "abc"},
		{name: "basic empty", header: basic("", ""), wantErr: types.ErrInvalidCredentials},
		{name: "basic malformed", header: "Basic !!!", wantErr: types.ErrInvalidCredentials},
		{name: "other scheme", header: "Digest username=x", wantErr: types.ErrNoCredentials},
		{name: "token without scheme", header: "abc", wantErr: types.ErrNoCredentials},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}

			key, err := apiKeyFromRequest(req)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, key)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantKey, key)
		})
	}
}

func TestServer_AuthenticateAPIKey(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)

	t.Run("known key", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", bearer(testKeyWriter))

		p, err := server.authenticateAPIKey(req)
		require.NoError(t, err)
		assert.Equal(t, &types.Principal{
			ID:         "writer",
			Scopes:     []string{types.ScopeAlertsWrite},
			Attributes: map[string]string{"auth_method": "api_key"},
		}, p)

		// The principal must not share the scopes slice with the settings.
		p.Scopes[0] = types.ScopeAll
		assert.Equal(t, []string{types.ScopeAlertsWrite}, server.apiSettings.Load().APIKeys[0].Scopes)
	})

	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", bearer("unknown"))

		p, err := server.authenticateAPIKey(req)
		require.ErrorIs(t, err, types.ErrInvalidCredentials)
		assert.Nil(t, p)
	})

	t.Run("no credentials", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)

		p, err := server.authenticateAPIKey(req)
		require.ErrorIs(t, err, types.ErrNoCredentials)
		assert.Nil(t, p)
	})
}

func TestServer_AuthMiddleware_APIKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		method        string
		path          string
		authorization string
		permissive    bool
		wantStatus    int
		wantResult    string // http_auth_requests_total result, empty for public endpoints
		wantError     string
	}{
		{name: "ping is public", method: http.MethodGet, path: "/ping", wantStatus: http.StatusOK},
		{name: "openapi spec is public", method: http.MethodGet, path: "/openapi.yaml", wantStatus: http.StatusOK},
		{name: "ping ignores invalid credentials", method: http.MethodGet, path: "/ping", authorization: bearer("unknown"), wantStatus: http.StatusOK},
		{name: "missing credentials", method: http.MethodGet, path: "/mappings", wantStatus: http.StatusUnauthorized, wantResult: authResultMissingCredentials, wantError: "Authentication required"},
		{name: "unsupported scheme", method: http.MethodGet, path: "/mappings", authorization: "Digest username=x", wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials, wantError: "Invalid credentials"},
		{name: "permissive rejects unsupported scheme", method: http.MethodGet, path: "/mappings", authorization: "Digest username=x", permissive: true, wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "permissive allows blank authorization header", method: http.MethodGet, path: "/mappings", authorization: "   ", permissive: true, wantStatus: http.StatusOK, wantResult: authResultAnonymous},
		{name: "unknown key", method: http.MethodGet, path: "/mappings", authorization: bearer("unknown"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials, wantError: "Invalid credentials"},
		{name: "malformed basic credentials", method: http.MethodGet, path: "/mappings", authorization: "Basic !!!", wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "reader reads config", method: http.MethodGet, path: "/mappings", authorization: bearer(testKeyReader), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "reader reads channels", method: http.MethodGet, path: "/channels", authorization: bearer(testKeyReader), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "reader with basic auth", method: http.MethodGet, path: "/mappings", authorization: basic("anything", testKeyReader), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "reader cannot send alerts", method: http.MethodPost, path: "/alerts", authorization: bearer(testKeyReader), wantStatus: http.StatusForbidden, wantResult: authResultForbidden, wantError: "Forbidden: the alerts:write scope is required"},
		{name: "reader cannot test alerts", method: http.MethodPost, path: "/alerts-test", authorization: bearer(testKeyReader), wantStatus: http.StatusForbidden, wantResult: authResultForbidden, wantError: "Forbidden: the alerts:test scope is required"},
		{name: "writer sends alerts", method: http.MethodPost, path: "/alerts", authorization: bearer(testKeyWriter), wantStatus: http.StatusBadRequest, wantResult: authResultAuthenticated},
		{name: "writer sends alerts to channel", method: http.MethodPost, path: "/alerts/C123", authorization: bearer(testKeyWriter), wantStatus: http.StatusBadRequest, wantResult: authResultAuthenticated},
		{name: "writer uses deprecated alias", method: http.MethodPost, path: "/slackalert", authorization: bearer(testKeyWriter), wantStatus: http.StatusBadRequest, wantResult: authResultAuthenticated},
		{name: "writer sends prometheus webhook", method: http.MethodPost, path: "/prometheus-alert", authorization: bearer(testKeyWriter), wantStatus: http.StatusBadRequest, wantResult: authResultAuthenticated},
		{name: "writer cannot read config", method: http.MethodGet, path: "/mappings", authorization: bearer(testKeyWriter), wantStatus: http.StatusForbidden, wantResult: authResultForbidden, wantError: "Forbidden: the config:read scope is required"},
		{name: "unauthenticated prometheus webhook", method: http.MethodPost, path: "/prometheus-alert/C123", wantStatus: http.StatusUnauthorized, wantResult: authResultMissingCredentials},
		{name: "admin tests alerts", method: http.MethodPost, path: "/alerts-test", authorization: bearer(testKeyAdmin), wantStatus: http.StatusBadRequest, wantResult: authResultAuthenticated},
		{name: "admin reads config", method: http.MethodGet, path: "/channels", authorization: bearer(testKeyAdmin), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "permissive allows missing credentials", method: http.MethodGet, path: "/mappings", permissive: true, wantStatus: http.StatusOK, wantResult: authResultAnonymous},
		{name: "permissive rejects invalid credentials", method: http.MethodGet, path: "/mappings", authorization: bearer("unknown"), permissive: true, wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "permissive rejects missing scope", method: http.MethodPost, path: "/alerts", authorization: bearer(testKeyReader), permissive: true, wantStatus: http.StatusForbidden, wantResult: authResultForbidden},
		{name: "permissive authenticates valid key", method: http.MethodGet, path: "/mappings", authorization: bearer(testKeyReader), permissive: true, wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, metrics := newAuthTestServer(t)
			server.cfg.Auth = &config.AuthConfig{Permissive: tt.permissive}

			w := serve(t, server, tt.method, tt.path, tt.authorization)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			if tt.wantStatus == http.StatusUnauthorized {
				assert.Equal(t, `Bearer realm="slackmgr"`, w.Header().Get("WWW-Authenticate"))
			} else {
				assert.Empty(t, w.Header().Get("WWW-Authenticate"))
			}

			if tt.wantError != "" {
				assert.JSONEq(t, fmt.Sprintf(`{"error":%q}`, tt.wantError), w.Body.String())
			}

			for _, result := range allAuthResults {
				want := 0.0
				if result == tt.wantResult {
					want = 1
				}

				assert.InDelta(t, want, metrics.counter(httpAuthRequestsMetric, result), 0, "result %s", result)
			}
		})
	}
}

func TestServer_AuthMiddleware_Disabled(t *testing.T) {
	t.Parallel()

	server := newTestServerWithChannels(t)
	metrics := newRecordingMetrics()
	server.metrics = metrics

	for _, path := range []string{"/mappings", "/channels"} {
		w := serve(t, server, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, w.Code, path)
	}

	// Credentials are ignored when authentication is disabled.
	w := serve(t, server, http.MethodPost, "/alerts", bearer("anything"))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	for _, result := range allAuthResults {
		assert.InDelta(t, 0.0, metrics.counter(httpAuthRequestsMetric, result), 0, result)
	}
}

func TestServer_AuthMiddleware_CustomAuthenticator(t *testing.T) {
	t.Parallel()

	oidc := types.AuthenticatorFunc(func(r *http.Request) (*types.Principal, error) {
		switch r.Header.Get("Authorization") {
		case "":
			return nil, types.ErrNoCredentials
		case bearer("oidc-token"):
			return &types.Principal{ID: "oidc-client", Scopes: []string{types.ScopeConfigRead}}, nil
		case bearer("idp-down"):
			return nil, errors.New("identity provider unavailable")
		case bearer("nil-nil"):
			return nil, nil //nolint:nilnil // tests a misbehaving authenticator
		case bearer("empty-id"):
			return &types.Principal{ID: "", Scopes: []string{types.ScopeConfigRead}}, nil
		case bearer("padded-id"):
			return &types.Principal{ID: " oidc:x ", Scopes: []string{types.ScopeConfigRead}}, nil
		case bearer("long-id"):
			return &types.Principal{ID: strings.Repeat("a", types.MaxClientLength+1), Scopes: []string{types.ScopeConfigRead}}, nil
		case bearer("max-id"):
			return &types.Principal{ID: strings.Repeat("é", types.MaxClientLength), Scopes: []string{types.ScopeConfigRead}}, nil
		case bearer("opaque"), "Basic dXNlcjpwYXNz":
			return nil, types.ErrNoCredentials // not a JWT, or another scheme, like the OIDC authenticator
		default:
			return nil, types.ErrInvalidCredentials
		}
	})

	tests := []struct {
		name          string
		apiKeys       bool
		permissive    bool
		authorization string
		wantStatus    int
		wantResult    string
	}{
		{name: "custom only, valid token", authorization: bearer("oidc-token"), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "custom only, api keys are not accepted", authorization: bearer(testKeyReader), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "custom only, no credentials", wantStatus: http.StatusUnauthorized, wantResult: authResultMissingCredentials},
		{name: "custom only, internal error", authorization: bearer("idp-down"), wantStatus: http.StatusInternalServerError, wantResult: authResultError},
		{name: "custom only, nil principal without error", authorization: bearer("nil-nil"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "chained, api key", apiKeys: true, authorization: bearer(testKeyReader), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "chained, oidc token", apiKeys: true, authorization: bearer("oidc-token"), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "chained, invalid token", apiKeys: true, authorization: bearer("unknown"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "chained, no credentials", apiKeys: true, wantStatus: http.StatusUnauthorized, wantResult: authResultMissingCredentials},
		{name: "chained, internal error", apiKeys: true, authorization: bearer("idp-down"), wantStatus: http.StatusInternalServerError, wantResult: authResultError},
		{name: "custom only, empty principal ID", authorization: bearer("empty-id"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "custom only, padded principal ID", authorization: bearer("padded-id"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "custom only, too long principal ID", authorization: bearer("long-id"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "custom only, max length principal ID", authorization: bearer("max-id"), wantStatus: http.StatusOK, wantResult: authResultAuthenticated},
		{name: "custom only, unrecognized token", authorization: bearer("opaque"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "permissive, unrecognized token", permissive: true, authorization: bearer("opaque"), wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "permissive, unrecognized scheme", permissive: true, authorization: "Basic dXNlcjpwYXNz", wantStatus: http.StatusUnauthorized, wantResult: authResultInvalidCredentials},
		{name: "permissive, no credentials", permissive: true, wantStatus: http.StatusOK, wantResult: authResultAnonymous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, metrics := newAuthTestServer(t)
			server.apiKeyAuth = tt.apiKeys
			server.cfg.Auth = &config.AuthConfig{Permissive: tt.permissive}
			server.WithAuthenticator(oidc)

			w := serve(t, server, http.MethodGet, "/mappings", tt.authorization)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			assert.InDelta(t, 1.0, metrics.counter(httpAuthRequestsMetric, tt.wantResult), 0)

			if tt.wantStatus == http.StatusInternalServerError {
				assert.JSONEq(t, `{"error":"Internal server error"}`, w.Body.String())
			}
		})
	}
}

func TestServer_AuthMiddleware_CustomAuthorizer(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls []string
	)

	authorizer := types.AuthorizerFunc(func(_ context.Context, p *types.Principal, scope string) error {
		mu.Lock()
		calls = append(calls, p.ID+":"+scope)
		mu.Unlock()

		switch p.ID {
		case "writer":
			return nil // allowed everything, regardless of its scopes
		case "reader":
			return fmt.Errorf("denied by policy: %w", types.ErrForbidden)
		default:
			return errors.New("policy service unavailable")
		}
	})

	tests := []struct {
		name          string
		authorization string
		wantStatus    int
		wantResult    string
		wantCall      string
	}{
		{name: "allowed", authorization: bearer(testKeyWriter), wantStatus: http.StatusOK, wantResult: authResultAuthenticated, wantCall: "writer:config:read"},
		{name: "forbidden", authorization: bearer(testKeyReader), wantStatus: http.StatusForbidden, wantResult: authResultForbidden, wantCall: "reader:config:read"},
		{name: "internal error", authorization: bearer(testKeyAdmin), wantStatus: http.StatusInternalServerError, wantResult: authResultError, wantCall: "admin:config:read"},
		{name: "not called without credentials", wantStatus: http.StatusUnauthorized, wantResult: authResultMissingCredentials},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, metrics := newAuthTestServer(t)
			server.WithAuthorizer(authorizer)

			w := serve(t, server, http.MethodGet, "/mappings", tt.authorization)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			assert.InDelta(t, 1.0, metrics.counter(httpAuthRequestsMetric, tt.wantResult), 0)

			if tt.wantCall != "" {
				mu.Lock()
				assert.Contains(t, calls, tt.wantCall)
				mu.Unlock()
			}
		})
	}
}

func TestServer_AuthMiddleware_KeyRotation(t *testing.T) {
	t.Parallel()

	server, _ := newAuthTestServer(t)

	assert.Equal(t, http.StatusOK, serve(t, server, http.MethodGet, "/mappings", bearer(testKeyReader)).Code)

	require.NoError(t, server.UpdateSettings(&config.APISettings{APIKeys: []*config.APIKey{
		{ID: "reader", KeySHA256: config.HashAPIKey("rotated-key"), Scopes: []string{types.ScopeConfigRead}},
	}}))

	assert.Equal(t, http.StatusUnauthorized, serve(t, server, http.MethodGet, "/mappings", bearer(testKeyReader)).Code)
	assert.Equal(t, http.StatusOK, serve(t, server, http.MethodGet, "/mappings", bearer("rotated-key")).Code)

	// Removing all keys rejects all requests: it does not disable authentication.
	require.NoError(t, server.UpdateSettings(&config.APISettings{}))

	assert.Equal(t, http.StatusUnauthorized, serve(t, server, http.MethodGet, "/mappings", bearer("rotated-key")).Code)
	assert.Equal(t, http.StatusUnauthorized, serve(t, server, http.MethodGet, "/mappings", "").Code)
}

func TestServer_AuthMiddleware_Logging(t *testing.T) {
	t.Parallel()

	newServer := func(t *testing.T) (*Server, *capturingLogger, *gin.Engine) {
		t.Helper()

		server, _ := newAuthTestServer(t)
		logger := newCapturingLogger()
		server.logger = logger

		engine := gin.New()
		engine.Use(server.jsonLogMiddleware())
		server.registerRoutes(engine)

		return server, logger, engine
	}

	t.Run("logs the principal of authenticated requests", func(t *testing.T) {
		t.Parallel()

		_, logger, engine := newServer(t)

		w := serveEngine(t, engine, http.MethodGet, "/mappings", bearer(testKeyReader))
		require.Equal(t, http.StatusOK, w.Code)

		entry := logger.find("Request")
		require.NotNil(t, entry)
		assert.Equal(t, "reader", entry.fields["principal"])
		assert.Equal(t, http.StatusOK, entry.fields["status"])
	})

	t.Run("logs no principal for public endpoints", func(t *testing.T) {
		t.Parallel()

		_, logger, engine := newServer(t)

		w := serveEngine(t, engine, http.MethodGet, "/ping", bearer(testKeyReader))
		require.Equal(t, http.StatusOK, w.Code)

		entry := logger.find("Request")
		require.NotNil(t, entry)
		assert.NotContains(t, entry.fields, "principal")
	})

	t.Run("logs failures without credentials", func(t *testing.T) {
		t.Parallel()

		_, logger, engine := newServer(t)

		w := serveEngine(t, engine, http.MethodGet, "/mappings", bearer("secret-unknown-key"))
		require.Equal(t, http.StatusUnauthorized, w.Code)

		entry := logger.find("Request authentication failed")
		require.NotNil(t, entry)
		assert.Equal(t, "info", entry.level)
		assert.Equal(t, authResultInvalidCredentials, entry.fields["auth_result"])
		assert.Equal(t, "/mappings", entry.fields["path"])
		assert.Equal(t, http.MethodGet, entry.fields["method"])
		assert.NotContains(t, entry.fields, "principal")

		for k, v := range entry.fields {
			assert.NotContains(t, fmt.Sprint(v), "secret-unknown-key", "field %s leaks the credentials", k)
		}
	})

	t.Run("logs the principal of forbidden requests", func(t *testing.T) {
		t.Parallel()

		_, logger, engine := newServer(t)

		w := serveEngine(t, engine, http.MethodPost, "/alerts", bearer(testKeyReader))
		require.Equal(t, http.StatusForbidden, w.Code)

		entry := logger.find("Request authentication failed")
		require.NotNil(t, entry)
		assert.Equal(t, "reader", entry.fields["principal"])
		assert.Equal(t, authResultForbidden, entry.fields["auth_result"])
	})

	t.Run("logs internal errors at error level", func(t *testing.T) {
		t.Parallel()

		server, logger, _ := newServer(t)
		server.apiKeyAuth = false
		server.WithAuthenticator(types.AuthenticatorFunc(func(*http.Request) (*types.Principal, error) {
			return nil, errors.New("identity provider unavailable")
		}))

		engine := gin.New()
		server.registerRoutes(engine)

		w := serveEngine(t, engine, http.MethodGet, "/mappings", bearer("x"))
		require.Equal(t, http.StatusInternalServerError, w.Code)

		entry := logger.find("Request authentication failed")
		require.NotNil(t, entry)
		assert.Equal(t, "error", entry.level)
		assert.Equal(t, "authentication failed: identity provider unavailable", entry.fields["error"])
	})
}

func TestServer_LogAuthState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		apiKeyAuth    bool
		authenticator bool
		permissive    bool
		noKeys        bool
		wantMsgs      []string
		wantNoMsgs    []string
	}{
		{
			name:       "disabled",
			wantMsgs:   []string{"API authentication is disabled: all endpoints are open"},
			wantNoMsgs: []string{"API authentication is enabled"},
		},
		{
			name:       "api keys",
			apiKeyAuth: true,
			wantMsgs:   []string{"API authentication is enabled"},
			wantNoMsgs: []string{"API authentication is disabled: all endpoints are open", "API authentication is permissive: requests without credentials are let through anonymously"},
		},
		{
			name:       "api keys without keys",
			apiKeyAuth: true,
			noKeys:     true,
			wantMsgs:   []string{"API key authentication is enabled, but no API keys are configured: all requests to protected endpoints are rejected (or anonymous in permissive mode)"},
		},
		{
			name:          "custom authenticator without keys",
			apiKeyAuth:    true,
			authenticator: true,
			noKeys:        true,
			wantMsgs:      []string{"API authentication is enabled"},
			wantNoMsgs:    []string{"API key authentication is enabled, but no API keys are configured: all requests to protected endpoints are rejected (or anonymous in permissive mode)"},
		},
		{
			name:       "permissive",
			apiKeyAuth: true,
			permissive: true,
			wantMsgs:   []string{"API authentication is enabled", "API authentication is permissive: requests without credentials are let through anonymously"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, _ := newAuthTestServer(t)
			logger := newCapturingLogger()
			server.logger = logger
			server.apiKeyAuth = tt.apiKeyAuth
			server.cfg.Auth = &config.AuthConfig{Permissive: tt.permissive}

			if tt.authenticator {
				server.WithAuthenticator(types.AuthenticatorFunc(func(*http.Request) (*types.Principal, error) {
					return nil, types.ErrNoCredentials
				}))
			}

			if tt.noKeys {
				server.apiSettings.Store(&config.APISettings{})
			}

			server.logAuthState()

			for _, msg := range tt.wantMsgs {
				assert.NotNil(t, logger.find(msg), "missing log message %q", msg)
			}

			for _, msg := range tt.wantNoMsgs {
				assert.Nil(t, logger.find(msg), "unexpected log message %q", msg)
			}
		})
	}

	t.Run("update settings without keys logs an error", func(t *testing.T) {
		t.Parallel()

		server, _ := newAuthTestServer(t)
		logger := newCapturingLogger()
		server.logger = logger

		require.NoError(t, server.UpdateSettings(&config.APISettings{}))

		entry := logger.find("API key authentication is enabled, but no API keys are configured: all requests to protected endpoints are rejected (or anonymous in permissive mode)")
		require.NotNil(t, entry)
		assert.Equal(t, "error", entry.level)
	})
}

func TestServer_RegisterAuthMetrics(t *testing.T) {
	t.Parallel()

	t.Run("enabled", func(t *testing.T) {
		t.Parallel()

		server, metrics := newAuthTestServer(t)
		server.registerAuthMetrics()

		metrics.mu.Lock()
		defer metrics.mu.Unlock()

		for _, result := range allAuthResults {
			assert.Contains(t, metrics.counters, httpAuthRequestsMetric+"{"+result+"}")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		metrics := newRecordingMetrics()
		server.metrics = metrics
		server.registerAuthMetrics()

		metrics.mu.Lock()
		defer metrics.mu.Unlock()

		assert.Empty(t, metrics.counters)
	})
}

func TestServer_AuthOptions(t *testing.T) {
	t.Parallel()

	server, _, _ := newTestServer(t)
	assert.False(t, server.authEnabled())
	assert.Nil(t, server.effectiveAuthenticator())

	assert.Same(t, server, server.WithAuthenticator(nil))
	assert.Same(t, server, server.WithAuthorizer(nil))
	assert.False(t, server.authEnabled())
	assert.Nil(t, server.authorizer)

	assert.Same(t, server, server.WithAPIKeyAuth())
	assert.True(t, server.authEnabled())
	assert.NotNil(t, server.effectiveAuthenticator())

	assert.False(t, server.authPermissive())
	server.cfg.Auth = &config.AuthConfig{}
	assert.False(t, server.authPermissive())
	server.cfg.Auth.Permissive = true
	assert.True(t, server.authPermissive())
}

func TestPrincipalFromContext(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Nil(t, principalFromContext(c))

	c.Set(principalContextKey, "not a principal")
	assert.Nil(t, principalFromContext(c))

	p := &types.Principal{ID: "a"}
	c.Set(principalContextKey, p)
	assert.Same(t, p, principalFromContext(c))
}
