package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Channels and API keys of the channel authorization tests.
const (
	allowedChannel   = "C1111111111"
	forbiddenChannel = "C2222222222"

	testKeyRestricted   = "restricted-key"   // principal "restricted": alerts:write, only allowedChannel
	testKeyUnrestricted = "unrestricted-key" // principal "unrestricted": alerts:write and alerts:test, no access rule
)

// channelAuthTestServer is a test server with API key auth, access rules, and a queue that records the alerts.
type channelAuthTestServer struct {
	*Server

	metrics *recordingMetrics

	mu     sync.Mutex
	queued []*types.Alert
}

func newChannelAuthTestServer(t *testing.T, defaultAccess string) *channelAuthTestServer {
	t.Helper()

	server, queue, channelInfo := newTestServer(t)
	ts := &channelAuthTestServer{Server: server, metrics: newRecordingMetrics()}
	server.metrics = ts.metrics
	server.WithAPIKeyAuth()

	settings := &config.APISettings{
		RoutingRules: []*config.RoutingRule{
			{Name: "allowed", Equals: []string{"allowed-key"}, Channel: allowedChannel},
			{Name: "forbidden", Equals: []string{"forbidden-key"}, Channel: forbiddenChannel},
		},
		APIKeys: []*config.APIKey{
			{ID: "restricted", KeySHA256: config.HashAPIKey(testKeyRestricted), Scopes: []string{types.ScopeAlertsWrite, types.ScopeAlertsTest}},
			{ID: "unrestricted", KeySHA256: config.HashAPIKey(testKeyUnrestricted), Scopes: []string{types.ScopeAlertsWrite, types.ScopeAlertsTest}},
		},
		DefaultChannelAccess: defaultAccess,
		AccessRules: []*config.AccessRule{
			{Name: "restricted", Principals: []string{"restricted"}, Channels: []string{allowedChannel}},
		},
	}
	require.NoError(t, settings.InitAndValidate(&mockLogger{}))
	server.apiSettings.Store(settings)

	// Channel names are not mapped; the invalid values are used by the validation tests.
	for _, channel := range []string{
		allowedChannel, forbiddenChannel, strings.ToLower(allowedChannel), "alerts-team-a",
		"a1", "a2", "a3", "a4", "bad channel", "<!channel>", "<@U0ADMIN> <https://evil.example|click>",
	} {
		channelInfo.On("MapChannelNameToIDIfNeeded", channel).Return(channel)
	}

	channelInfo.On("GetChannelInfo", mock.Anything, mock.Anything).Return(&ChannelInfo{
		ChannelExists:      true,
		ManagerIsInChannel: true,
		UserCount:          10,
	}, nil)

	queue.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		var alert types.Alert
		require.NoError(t, json.Unmarshal([]byte(args.String(3)), &alert))

		ts.mu.Lock()
		ts.queued = append(ts.queued, &alert)
		ts.mu.Unlock()
	}).Return(nil)

	return ts
}

func (ts *channelAuthTestServer) post(t *testing.T, path, body, authorization string) *httptest.ResponseRecorder {
	t.Helper()

	engine := gin.New()
	ts.registerRoutes(engine)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	return w
}

func (ts *channelAuthTestServer) queuedAlerts() []*types.Alert {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	return ts.queued
}

func TestServer_ChannelAuthorization(t *testing.T) {
	t.Parallel()

	alert := `{"header":"h","slackChannelId":"` + allowedChannel + `"}`
	forbidden := `{"header":"h","slackChannelId":"` + forbiddenChannel + `"}`

	tests := []struct {
		name          string
		defaultAccess string
		permissive    bool
		authDisabled  bool
		path          string
		body          string
		authorization string
		wantStatus    int
		wantError     string
		wantRejected  float64 // alerts_rejected_total{reason="forbidden_channel"}
	}{
		{name: "allowed channel", path: "/alerts", body: `{"alerts":[` + alert + `]}`, authorization: bearer(testKeyRestricted), wantStatus: http.StatusAccepted},
		{name: "allowed lower-case channel", path: "/alerts", body: `{"alerts":[{"header":"h","slackChannelId":"c1111111111"}]}`, authorization: bearer(testKeyRestricted), wantStatus: http.StatusAccepted},
		{
			name: "forbidden channel in body", path: "/alerts", body: `{"alerts":[` + forbidden + `]}`, authorization: bearer(testKeyRestricted),
			wantStatus: http.StatusForbidden, wantError: "Principal restricted is not allowed to send alerts to channel " + forbiddenChannel, wantRejected: 1,
		},
		{
			name: "forbidden channel in path", path: "/alerts/" + forbiddenChannel, body: `{"alerts":[{"header":"h"}]}`, authorization: bearer(testKeyRestricted),
			wantStatus: http.StatusForbidden, wantRejected: 1,
		},
		{
			name: "forbidden channel by route key", path: "/alerts", body: `{"alerts":[{"header":"h","routeKey":"forbidden-key"}]}`, authorization: bearer(testKeyRestricted),
			wantStatus: http.StatusForbidden, wantRejected: 1,
		},
		{name: "allowed channel by route key", path: "/alerts", body: `{"alerts":[{"header":"h","routeKey":"allowed-key"}]}`, authorization: bearer(testKeyRestricted), wantStatus: http.StatusAccepted},
		{
			name: "unmapped channel name", path: "/alerts", body: `{"alerts":[{"header":"h","slackChannelId":"alerts-team-a"}]}`, authorization: bearer(testKeyRestricted),
			wantStatus: http.StatusForbidden, wantError: "Principal restricted is not allowed to send alerts to channel ALERTS-TEAM-A", wantRejected: 1,
		},
		{
			name: "forbidden escalation channel", path: "/alerts",
			body:          `{"alerts":[{"header":"h","slackChannelId":"` + allowedChannel + `","escalation":[{"delaySeconds":60,"severity":"panic","moveToChannel":"` + forbiddenChannel + `"}]}]}`,
			authorization: bearer(testKeyRestricted), wantStatus: http.StatusForbidden,
			wantError:    "Principal restricted is not allowed to move issues to channel " + forbiddenChannel + " (escalation moveToChannel)",
			wantRejected: 1,
		},
		{
			name: "allowed escalation channel", path: "/alerts",
			body:          `{"alerts":[{"header":"h","slackChannelId":"` + allowedChannel + `","escalation":[{"delaySeconds":60,"severity":"panic","moveToChannel":"` + allowedChannel + `"}]}]}`,
			authorization: bearer(testKeyRestricted), wantStatus: http.StatusAccepted,
		},
		{
			name: "mixed batch is rejected as a whole", path: "/alerts", body: `{"alerts":[` + alert + `,` + forbidden + `]}`, authorization: bearer(testKeyRestricted),
			wantStatus: http.StatusForbidden, wantRejected: 2,
		},
		{
			name: "forbidden before invalid", path: "/alerts", body: `{"alerts":[{"slackChannelId":"` + forbiddenChannel + `"}]}`, authorization: bearer(testKeyRestricted),
			wantStatus: http.StatusForbidden, wantRejected: 1,
		},
		{
			name: "prometheus webhook", path: "/prometheus-alert/" + forbiddenChannel, authorization: bearer(testKeyRestricted),
			body: `{"version":"4","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"A"},"annotations":{"summary":"s"},` +
				`"startsAt":"2026-11-14T10:00:00Z","endsAt":"0001-01-01T00:00:00Z"}]}`,
			wantStatus: http.StatusForbidden, wantRejected: 1,
		},
		{name: "test endpoint, forbidden", path: "/alerts-test", body: forbidden, authorization: bearer(testKeyRestricted), wantStatus: http.StatusForbidden},
		{name: "test endpoint, allowed", path: "/alerts-test", body: alert, authorization: bearer(testKeyRestricted), wantStatus: http.StatusNoContent},
		{name: "no rule, default allow", path: "/alerts", body: `{"alerts":[` + forbidden + `]}`, authorization: bearer(testKeyUnrestricted), wantStatus: http.StatusAccepted},
		{
			name: "no rule, default deny", defaultAccess: config.ChannelAccessDeny, path: "/alerts", body: `{"alerts":[` + alert + `]}`, authorization: bearer(testKeyUnrestricted),
			wantStatus: http.StatusForbidden, wantRejected: 1,
		},
		{name: "rule, default deny", defaultAccess: config.ChannelAccessDeny, path: "/alerts", body: `{"alerts":[` + alert + `]}`, authorization: bearer(testKeyRestricted), wantStatus: http.StatusAccepted},
		{
			name: "invalid escalation channel is not echoed", path: "/alerts",
			body:          `{"alerts":[{"header":"h","slackChannelId":"` + allowedChannel + `","escalation":[{"delaySeconds":60,"severity":"panic","moveToChannel":"<@U0ADMIN> <https://evil.example|click>"}]}]}`,
			authorization: bearer(testKeyRestricted), wantStatus: http.StatusBadRequest,
			wantError: "Input validation failed: alerts[0].escalation[0].moveToChannel is not a valid Slack channel ID or name",
		},
		{
			name: "invalid channel is not echoed", path: "/alerts", body: `{"alerts":[` + alert + `,{"header":"h","slackChannelId":"<!channel>"}]}`,
			authorization: bearer(testKeyRestricted), wantStatus: http.StatusBadRequest,
			wantError: "Input validation failed: alerts[1].slackChannelId is not a valid Slack channel ID or name",
		},
		{
			name: "too many escalations", path: "/alerts",
			body: `{"alerts":[{"header":"h","slackChannelId":"` + allowedChannel + `","escalation":[` +
				`{"delaySeconds":60,"moveToChannel":"a1"},{"delaySeconds":120,"moveToChannel":"a2"},{"delaySeconds":180,"moveToChannel":"a3"},{"delaySeconds":240,"moveToChannel":"a4"}]}]}`,
			authorization: bearer(testKeyRestricted), wantStatus: http.StatusBadRequest,
			wantError: "Input validation failed: alerts[0] has too many escalation points, expected <=3",
		},
		{name: "anonymous in permissive mode is not restricted", defaultAccess: config.ChannelAccessDeny, permissive: true, path: "/alerts", body: `{"alerts":[` + forbidden + `]}`, wantStatus: http.StatusAccepted},
		{name: "auth disabled is not restricted", defaultAccess: config.ChannelAccessDeny, authDisabled: true, path: "/alerts", body: `{"alerts":[` + forbidden + `]}`, authorization: bearer(testKeyRestricted), wantStatus: http.StatusAccepted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := newChannelAuthTestServer(t, tt.defaultAccess)
			ts.cfg.Auth = &config.AuthConfig{Permissive: tt.permissive}
			ts.apiKeyAuth = !tt.authDisabled

			w := ts.post(t, tt.path, tt.body, tt.authorization)

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			if tt.wantError != "" {
				assert.JSONEq(t, `{"error":"`+tt.wantError+`"}`, w.Body.String())
			}

			if tt.wantStatus == http.StatusForbidden || tt.wantStatus == http.StatusBadRequest {
				assert.Empty(t, ts.queuedAlerts(), "nothing may be queued")
			}

			assert.InDelta(t, tt.wantRejected, ts.metrics.counter(alertsRejectedTotalMetric, rejectReasonForbiddenChannel), 0)
		})
	}
}

// channelAuthorizerStub is an Authorizer that allows all scopes, and implements ChannelAuthorizer.
type channelAuthorizerStub struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (a *channelAuthorizerStub) Authorize(context.Context, *types.Principal, string) error {
	return nil
}

func (a *channelAuthorizerStub) AuthorizeChannel(_ context.Context, p *types.Principal, channelID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, p.ID+":"+channelID)

	if channelID == allowedChannel {
		return nil
	}

	return a.err
}

func TestServer_ChannelAuthorization_CustomAuthorizer(t *testing.T) {
	t.Parallel()

	batch := `{"alerts":[` +
		`{"header":"h","slackChannelId":"` + allowedChannel + `"},` +
		`{"header":"h","slackChannelId":"` + allowedChannel + `"},` +
		`{"header":"h","slackChannelId":"c1111111111","escalation":[{"delaySeconds":60,"severity":"panic","moveToChannel":"` + forbiddenChannel + `"}]}]}`

	t.Run("replaces the access rules and is called once per channel", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, config.ChannelAccessDeny)
		authorizer := &channelAuthorizerStub{} // allows all channels (err is nil)
		ts.WithAuthorizer(authorizer)

		// The access rules would forbid forbiddenChannel for "restricted".
		w := ts.post(t, "/alerts", batch, bearer(testKeyRestricted))
		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		assert.Len(t, ts.queuedAlerts(), 3)

		authorizer.mu.Lock()
		defer authorizer.mu.Unlock()
		assert.Equal(t, []string{"restricted:" + allowedChannel, "restricted:" + forbiddenChannel}, authorizer.calls)
	})

	t.Run("is not called for invalid channels", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, "")
		authorizer := &channelAuthorizerStub{}
		ts.WithAuthorizer(authorizer)

		body := `{"alerts":[{"header":"h","slackChannelId":"` + allowedChannel + `","escalation":[{"delaySeconds":60,"severity":"panic","moveToChannel":"bad channel"}]}]}`

		w := ts.post(t, "/alerts", body, bearer(testKeyRestricted))
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.InDelta(t, 1.0, ts.metrics.counter(alertsRejectedTotalMetric, rejectReasonValidation), 0)

		w = ts.post(t, "/alerts-test", body, bearer(testKeyRestricted))
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		authorizer.mu.Lock()
		defer authorizer.mu.Unlock()
		assert.Empty(t, authorizer.calls)
	})

	t.Run("forbidden", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, "")
		ts.WithAuthorizer(&channelAuthorizerStub{err: types.ErrForbidden})

		w := ts.post(t, "/alerts", batch, bearer(testKeyUnrestricted))
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		assert.InDelta(t, 3.0, ts.metrics.counter(alertsRejectedTotalMetric, rejectReasonForbiddenChannel), 0)
	})

	t.Run("internal error", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, "")
		ts.WithAuthorizer(&channelAuthorizerStub{err: errors.New("policy service unavailable")})

		w := ts.post(t, "/alerts", batch, bearer(testKeyUnrestricted))
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		assert.JSONEq(t, `{"error":"Internal server error"}`, w.Body.String())
		assert.InDelta(t, 3.0, ts.metrics.counter(alertsRejectedTotalMetric, rejectReasonAuthorizationFailed), 0)
		assert.Empty(t, ts.queuedAlerts())
	})

	t.Run("internal error on the test endpoint", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, "")
		ts.WithAuthorizer(&channelAuthorizerStub{err: errors.New("policy service unavailable")})

		w := ts.post(t, "/alerts-test", `{"header":"h","slackChannelId":"`+forbiddenChannel+`"}`, bearer(testKeyUnrestricted))
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	})

	t.Run("an authorizer without channel support uses the access rules", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, "")
		ts.WithAuthorizer(types.AuthorizerFunc(func(context.Context, *types.Principal, string) error { return nil }))

		w := ts.post(t, "/alerts", `{"alerts":[{"header":"h","slackChannelId":"`+forbiddenChannel+`"}]}`, bearer(testKeyRestricted))
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	})
}

func TestServer_ClientBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		binding       string
		permissive    bool
		authorization string
		clients       []string // client of each alert in the request
		wantClients   []string
	}{
		{name: "default fills empty clients", authorization: bearer(testKeyUnrestricted), clients: []string{"", "  ", "grafana"}, wantClients: []string{"unrestricted", "unrestricted", "grafana"}},
		{name: "fill", binding: config.ClientBindingFill, authorization: bearer(testKeyUnrestricted), clients: []string{"", "grafana"}, wantClients: []string{"unrestricted", "grafana"}},
		{name: "override", binding: config.ClientBindingOverride, authorization: bearer(testKeyUnrestricted), clients: []string{"", "grafana", "unrestricted"}, wantClients: []string{"unrestricted", "unrestricted", "unrestricted"}},
		{name: "off", binding: config.ClientBindingOff, authorization: bearer(testKeyUnrestricted), clients: []string{"", "grafana"}, wantClients: []string{"", "grafana"}},
		{name: "anonymous is not bound", binding: config.ClientBindingOverride, permissive: true, clients: []string{"", "grafana"}, wantClients: []string{"", "grafana"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := newChannelAuthTestServer(t, "")
			ts.cfg.Auth = &config.AuthConfig{Permissive: tt.permissive, ClientBinding: tt.binding}

			alerts := make([]map[string]any, 0, len(tt.clients))
			for i, client := range tt.clients {
				alerts = append(alerts, map[string]any{"header": "h", "correlationId": string(rune('a' + i)), "slackChannelId": allowedChannel, "client": client})
			}

			body, err := json.Marshal(map[string]any{"alerts": alerts})
			require.NoError(t, err)

			w := ts.post(t, "/alerts", string(body), tt.authorization)
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())

			queued := ts.queuedAlerts()
			require.Len(t, queued, len(tt.wantClients))

			got := make(map[string]string, len(queued))
			for _, a := range queued {
				got[a.CorrelationID] = a.Client
			}

			for i, want := range tt.wantClients {
				assert.Equal(t, want, got[string(rune('a'+i))], "client of alert %d", i)
			}
		})
	}

	t.Run("principal IDs are never truncated", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			id         string
			wantStatus int
		}{
			{id: strings.Repeat("p", types.MaxClientLength), wantStatus: http.StatusAccepted},
			{id: strings.Repeat("p", types.MaxClientLength+1), wantStatus: http.StatusUnauthorized},
		} {
			ts := newChannelAuthTestServer(t, "")
			ts.apiKeyAuth = false
			ts.cfg.Auth = &config.AuthConfig{ClientBinding: config.ClientBindingOverride}
			ts.WithAuthenticator(types.AuthenticatorFunc(func(*http.Request) (*types.Principal, error) {
				return &types.Principal{ID: tc.id, Scopes: []string{types.ScopeAll}}, nil
			}))

			w := ts.post(t, "/alerts", `{"alerts":[{"header":"h","slackChannelId":"`+allowedChannel+`"}]}`, bearer("x"))
			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())

			if tc.wantStatus == http.StatusAccepted {
				queued := ts.queuedAlerts()
				require.Len(t, queued, 1)
				assert.Equal(t, tc.id, queued[0].Client)
			} else {
				assert.Empty(t, ts.queuedAlerts())
			}
		}
	})

	t.Run("binds clients on the test endpoint", func(t *testing.T) {
		t.Parallel()

		ts := newChannelAuthTestServer(t, "")
		logger := newCapturingLogger()
		ts.logger = logger

		w := ts.post(t, "/alerts-test", `{"header":"h","slackChannelId":"`+allowedChannel+`"}`, bearer(testKeyUnrestricted))
		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

		entry := logger.find("Received alert body")
		require.NotNil(t, entry)
		assert.Contains(t, entry.fields["body"], `"client":"unrestricted"`)
	})
}
