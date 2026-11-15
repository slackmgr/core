package restapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"
	"github.com/pb33f/libopenapi-validator/config"
	valerrors "github.com/pb33f/libopenapi-validator/errors"
	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	coreconfig "github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// deprecatedAliasPaths are the alias routes of POST /alerts, which must be marked as deprecated in the spec.
var deprecatedAliasPaths = []string{
	"/alert", "/alert/{slackChannelId}",
	"/slackalert", "/slackalert/{slackChannelId}",
	"/slackalerts", "/slackalerts/{slackChannelId}",
}

// openAPI parses the embedded spec and creates a validator for it once, as they are shared by all tests.
// Creating a validator warms caches in the model, so it must not run concurrently with other uses of the model.
var openAPI = sync.OnceValues(func() (*v3.Document, validator.Validator) {
	doc, err := libopenapi.NewDocument(openAPISpec)
	if err != nil {
		panic(fmt.Sprintf("failed to parse spec: %s", err))
	}

	model, err := doc.BuildV3Model()
	if err != nil {
		panic(fmt.Sprintf("failed to build v3 model: %s", err))
	}

	return &model.Model, validator.NewValidatorFromV3Model(&model.Model, config.WithFormatAssertions())
})

func newOpenAPIValidator(t *testing.T) validator.Validator {
	t.Helper()

	_, v := openAPI()

	return v
}

func formatValidationErrors(errs []*valerrors.ValidationError) string {
	msgs := make([]string, 0, len(errs))

	for _, e := range errs {
		msg := e.Message
		if e.Reason != "" {
			msg += ": " + e.Reason
		}

		for _, se := range e.SchemaValidationErrors {
			msg += fmt.Sprintf(" [%s: %s]", se.FieldPath, se.Reason)
		}

		msgs = append(msgs, msg)
	}

	return strings.Join(msgs, "\n")
}

// specOperations returns all "METHOD /path" pairs in the spec, with gin-style path parameters.
func specOperations(t *testing.T) map[string]*v3.Operation {
	t.Helper()

	model, _ := openAPI()

	ops := make(map[string]*v3.Operation)

	for path, item := range model.Paths.PathItems.FromOldest() {
		for method, op := range item.GetOperations().FromOldest() {
			ops[strings.ToUpper(method)+" "+path] = op
		}
	}

	return ops
}

func TestOpenAPISpec(t *testing.T) {
	t.Parallel()

	t.Run("is a valid OpenAPI 3.1 document", func(t *testing.T) {
		t.Parallel()

		doc, err := libopenapi.NewDocument(openAPISpec)
		require.NoError(t, err)
		assert.Equal(t, "3.1.0", doc.GetVersion())

		v, errs := validator.NewValidator(doc)
		require.Empty(t, errs)

		valid, valErrs := v.ValidateDocument()
		assert.True(t, valid, formatValidationErrors(valErrs))
	})

	t.Run("returns a copy", func(t *testing.T) {
		t.Parallel()

		spec := OpenAPISpec()
		require.Equal(t, openAPISpec, spec)

		spec[0] = 'X'
		assert.NotEqual(t, spec[0], OpenAPISpec()[0])
	})

	t.Run("documents exactly the registered routes", func(t *testing.T) {
		t.Parallel()

		server, _, _ := newTestServer(t)
		engine := gin.New()
		server.registerRoutes(engine)

		routes := make([]string, 0, len(engine.Routes()))

		for _, r := range engine.Routes() {
			path := r.Path
			if i := strings.Index(path, ":"); i >= 0 {
				path = path[:i] + "{" + path[i+1:] + "}"
			}

			routes = append(routes, r.Method+" "+path)
		}

		ops := specOperations(t)
		documented := make([]string, 0, len(ops))

		for op := range ops {
			documented = append(documented, op)
		}

		slices.Sort(routes)
		slices.Sort(documented)

		assert.Equal(t, routes, documented, "the routes in registerRoutes and the paths in openapi.yaml must match")
	})

	t.Run("marks only the alert aliases as deprecated", func(t *testing.T) {
		t.Parallel()

		for name, op := range specOperations(t) {
			path := strings.SplitN(name, " ", 2)[1]
			want := slices.Contains(deprecatedAliasPaths, path)
			got := op.Deprecated != nil && *op.Deprecated

			assert.Equal(t, want, got, "deprecated flag of %s", name)
		}
	})

	t.Run("has a unique operation ID for every operation", func(t *testing.T) {
		t.Parallel()

		seen := map[string]string{}

		for name, op := range specOperations(t) {
			require.NotEmpty(t, op.OperationId, "operationId of %s", name)

			if other, ok := seen[op.OperationId]; ok {
				t.Errorf("operationId %s is used by both %s and %s", op.OperationId, other, name)
			}

			seen[op.OperationId] = name
		}
	})
}

func TestServer_HandleOpenAPISpec(t *testing.T) {
	t.Parallel()

	server, _, _ := newTestServer(t)
	engine := gin.New()
	server.registerRoutes(engine)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/openapi.yaml", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/yaml", w.Header().Get("Content-Type"))
	assert.Equal(t, openAPISpec, w.Body.Bytes())
}

// TestOpenAPISpec_AlertRequests checks that the spec and the server agree on which alert request bodies are valid.
func TestOpenAPISpec_AlertRequests(t *testing.T) {
	t.Parallel()

	// clientAlert returns a fully serialized types.Alert, like the go-client sends: all fields are present,
	// with zero values (empty strings, false, 0 and null) for unset fields.
	clientAlert := func(mutate func(a *types.Alert)) map[string]any {
		a := types.NewErrorAlert()
		a.Header = "Header"
		a.SlackChannelID = "C123"

		if mutate != nil {
			mutate(a)
		}

		var m map[string]any

		b, err := json.Marshal(a)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &m))

		return m
	}

	webhook := func(mutate func(w *types.Webhook)) []*types.Webhook {
		w := &types.Webhook{ID: "restart", URL: "https://example.com/restart", ButtonText: "Restart"}
		if mutate != nil {
			mutate(w)
		}

		return []*types.Webhook{w}
	}

	tests := []struct {
		name  string
		body  any
		valid bool
	}{
		{
			name:  "minimal single alert",
			body:  map[string]any{"header": "Header", "slackChannelId": "C123"},
			valid: true,
		},
		{
			name:  "text without header",
			body:  map[string]any{"text": "Text", "slackChannelId": "C123"},
			valid: true,
		},
		{
			name: "example alert from the examples repo",
			body: map[string]any{
				"correlationId": "foo-4573945", "header": ":status: App error", "text": "TESTING TESTING!\nApp `foo` is failing",
				"severity": "warning", "issueFollowUpEnabled": true, "autoResolveSeconds": 60, "autoResolveAsInconclusive": false,
				"slackChannelId": "", "routeKey": "a", "host": "10.11.12.13", "username": "", "iconEmoji": ":polar_bear:",
				"fallbackText": "App foo is failing", "notificationDelaySeconds": 0, "archivingDelaySeconds": 30,
			},
			valid: true,
		},
		{
			name:  "fully serialized go-client alert",
			body:  clientAlert(nil),
			valid: true,
		},
		{
			name: "go-client alert with all nested objects",
			body: clientAlert(func(a *types.Alert) {
				a.IssueFollowUpEnabled = true
				a.AutoResolveSeconds = 3600
				a.Link = "https://example.com"
				a.IconEmoji = ":fire:"
				a.Fields = []*types.Field{{Title: "Env", Value: "prod"}}
				a.Escalation = []*types.Escalation{{Severity: types.AlertPanic, DelaySeconds: 60, SlackMentions: []string{"<!here>"}, MoveToChannel: "C456"}}
				a.IgnoreIfTextContains = []string{"noise"}
				a.Metadata = map[string]any{"key": "value"}
				a.Webhooks = webhook(func(w *types.Webhook) {
					w.ButtonStyle = types.WebhookButtonStyleDanger
					w.AccessLevel = types.WebhookAccessLevelChannelMembers
					w.DisplayMode = types.WebhookDisplayModeAlways
					w.Payload = map[string]any{"service": "foo"}
					w.PlainTextInput = []*types.WebhookPlainTextInput{{ID: "reason", Description: "Reason", MaxLength: 100}}
					w.CheckboxInput = []*types.WebhookCheckboxInput{{ID: "opts", Options: []*types.WebhookCheckboxOption{{Value: "force", Text: "Force"}}}}
					w.AfterClick = &types.WebhookAfterClick{HideButtons: types.WebhookHideButtonsClicked, StatusText: "Restarting", ResetAfterSeconds: 600}
				})
			}),
			valid: true,
		},
		{
			name:  "alerts array at the root level",
			body:  []any{clientAlert(nil), clientAlert(nil)},
			valid: true,
		},
		{
			name:  "alerts in an alerts field",
			body:  map[string]any{"alerts": []any{clientAlert(nil), clientAlert(nil)}},
			valid: true,
		},
		{
			name:  "header and text both missing",
			body:  map[string]any{"slackChannelId": "C123"},
			valid: false,
		},
		{
			name:  "header and text both empty",
			body:  clientAlert(func(a *types.Alert) { a.Header = "" }),
			valid: false,
		},
		{
			name:  "invalid severity",
			body:  clientAlert(func(a *types.Alert) { a.Severity = "bogus" }),
			valid: false,
		},
		{
			name:  "invalid slack channel ID",
			body:  clientAlert(func(a *types.Alert) { a.SlackChannelID = "not a channel!" }),
			valid: false,
		},
		{
			name:  "invalid icon emoji",
			body:  clientAlert(func(a *types.Alert) { a.IconEmoji = "fire" }),
			valid: false,
		},
		{
			name:  "invalid link",
			body:  clientAlert(func(a *types.Alert) { a.Link = "not a url" }),
			valid: false,
		},
		{
			name:  "correlation ID too long",
			body:  clientAlert(func(a *types.Alert) { a.CorrelationID = strings.Repeat("x", types.MaxCorrelationIDLength+1) }),
			valid: false,
		},
		{
			name:  "issue follow-up without auto resolve",
			body:  clientAlert(func(a *types.Alert) { a.IssueFollowUpEnabled = true }),
			valid: false,
		},
		{
			name: "auto resolve too high",
			body: clientAlert(func(a *types.Alert) {
				a.IssueFollowUpEnabled = true
				a.AutoResolveSeconds = types.MaxAutoResolveSeconds + 1
			}),
			valid: false,
		},
		{
			name: "too many fields",
			body: clientAlert(func(a *types.Alert) {
				for range types.MaxFieldCount + 1 {
					a.Fields = append(a.Fields, &types.Field{Title: "t", Value: "v"})
				}
			}),
			valid: false,
		},
		{
			name: "too many escalations",
			body: clientAlert(func(a *types.Alert) {
				for i := range types.MaxEscalationCount + 1 {
					a.Escalation = append(a.Escalation, &types.Escalation{Severity: types.AlertError, DelaySeconds: 60 * (i + 1)})
				}
			}),
			valid: false,
		},
		{
			name: "escalation delay too low",
			body: clientAlert(func(a *types.Alert) {
				a.Escalation = []*types.Escalation{{Severity: types.AlertError, DelaySeconds: 10}}
			}),
			valid: false,
		},
		{
			name: "escalation with invalid severity",
			body: clientAlert(func(a *types.Alert) {
				a.Escalation = []*types.Escalation{{Severity: types.AlertInfo, DelaySeconds: 60}}
			}),
			valid: false,
		},
		{
			name: "escalation with invalid mention",
			body: clientAlert(func(a *types.Alert) {
				a.Escalation = []*types.Escalation{{Severity: types.AlertError, DelaySeconds: 60, SlackMentions: []string{"@here"}}}
			}),
			valid: false,
		},
		{
			name: "too many ignore terms",
			body: clientAlert(func(a *types.Alert) {
				a.IgnoreIfTextContains = slices.Repeat([]string{"x"}, types.MaxIgnoreIfTextContainsCount+1)
			}),
			valid: false,
		},
		{
			name:  "webhook without button text",
			body:  clientAlert(func(a *types.Alert) { a.Webhooks = webhook(func(w *types.Webhook) { w.ButtonText = "" }) }),
			valid: false,
		},
		{
			name: "webhook button text too long",
			body: clientAlert(func(a *types.Alert) {
				a.Webhooks = webhook(func(w *types.Webhook) { w.ButtonText = strings.Repeat("x", types.MaxWebhookButtonTextLength+1) })
			}),
			valid: false,
		},
		{
			name:  "webhook with invalid button style",
			body:  clientAlert(func(a *types.Alert) { a.Webhooks = webhook(func(w *types.Webhook) { w.ButtonStyle = "loud" }) }),
			valid: false,
		},
		{
			name:  "webhook with invalid access level",
			body:  clientAlert(func(a *types.Alert) { a.Webhooks = webhook(func(w *types.Webhook) { w.AccessLevel = "everyone" }) }),
			valid: false,
		},
		{
			name: "danger webhook without confirmation dialog",
			body: clientAlert(func(a *types.Alert) {
				a.Webhooks = webhook(func(w *types.Webhook) {
					w.ButtonStyle = types.WebhookButtonStyleDanger
					w.SkipConfirmationDialog = true
				})
			}),
			valid: false,
		},
		{
			name: "webhook with inputs without confirmation dialog",
			body: clientAlert(func(a *types.Alert) {
				a.Webhooks = webhook(func(w *types.Webhook) {
					w.SkipConfirmationDialog = true
					w.PlainTextInput = []*types.WebhookPlainTextInput{{ID: "reason", MaxLength: 100}}
				})
			}),
			valid: false,
		},
		{
			name: "webhook after click reset too high",
			body: clientAlert(func(a *types.Alert) {
				a.Webhooks = webhook(func(w *types.Webhook) {
					w.AfterClick = &types.WebhookAfterClick{ResetAfterSeconds: types.MaxWebhookAfterClickResetSeconds + 1}
				})
			}),
			valid: false,
		},
		{
			name: "too many webhooks",
			body: clientAlert(func(a *types.Alert) {
				for i := range types.MaxWebhookCount + 1 {
					a.Webhooks = append(a.Webhooks, &types.Webhook{ID: fmt.Sprintf("w%d", i), URL: "https://example.com", ButtonText: "Click"})
				}
			}),
			valid: false,
		},
	}

	v := newOpenAPIValidator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := json.Marshal(tt.body)
			require.NoError(t, err)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alerts", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			specValid, errs := v.ValidateHttpRequestSync(req)
			assert.Equal(t, tt.valid, specValid, "spec validation: %s", formatValidationErrors(errs))

			// The server must agree with the spec.
			serverErr := serverValidateAlerts(body)
			if tt.valid {
				assert.NoError(t, serverErr, "server validation")
			} else {
				assert.Error(t, serverErr, "server validation")
			}
		})
	}
}

// serverValidateAlerts parses and validates an alert request body like the server does.
func serverValidateAlerts(body []byte) error {
	alerts, err := parseAlertInput(body)
	if err != nil {
		return err
	}

	if len(alerts) == 0 {
		return errors.New("no alerts")
	}

	for _, a := range alerts {
		a.Clean()

		if err := a.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// TestOpenAPISpec_Responses checks that the responses of the server conform to the spec.
func TestOpenAPISpec_Responses(t *testing.T) {
	t.Parallel()

	validAlert := `{"header":"Header","slackChannelId":"C123"}`

	prometheusWebhook := `{"version":"4","groupKey":"g","status":"firing","receiver":"slackmgr","alerts":[` +
		`{"status":"firing","labels":{"alertname":"HighLatency","channel":"C123"},"annotations":{"summary":"Latency is high"},` +
		`"startsAt":"2026-11-14T10:00:00Z","endsAt":"0001-01-01T00:00:00Z"}]}`

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		setup      func(s *Server, q *mockFifoQueueProducer, c *mockChannelInfoProvider)
		wantStatus int
	}{
		{
			name: "send alerts", method: http.MethodPost, path: "/alerts", body: validAlert, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts to channel", method: http.MethodPost, path: "/alerts/C123", body: `{"header":"Header"}`, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts with deprecated alias", method: http.MethodPost, path: "/slackalerts", body: validAlert, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts that are all ignored", method: http.MethodPost, path: "/alerts",
			body:       `{"text":"some noise","slackChannelId":"C123","ignoreIfTextContains":["noise"]}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name: "send invalid alert", method: http.MethodPost, path: "/alerts", body: `{"slackChannelId":"C123"}`, wantStatus: http.StatusBadRequest,
		},
		{
			name: "send unparseable body", method: http.MethodPost, path: "/alerts", body: `{`, wantStatus: http.StatusBadRequest,
		},
		{
			name: "send rate limited alert", method: http.MethodPost, path: "/alerts", body: validAlert,
			setup: func(s *Server, _ *mockFifoQueueProducer, _ *mockChannelInfoProvider) {
				s.cfg.RateLimitPerAlertChannel.AlertsPerSecond = 0.001
				s.cfg.RateLimitPerAlertChannel.AllowedBurst = 1
				_, _ = takeOne(t.Context(), s.alertRateLimiter, "C123", 1, *s.cfg.RateLimitPerAlertChannel)
			},
			wantStatus: http.StatusTooManyRequests,
		},
		{
			name: "send alert with failing queue", method: http.MethodPost, path: "/alerts", body: validAlert,
			setup: func(_ *Server, q *mockFifoQueueProducer, _ *mockChannelInfoProvider) {
				q.ExpectedCalls = nil
				q.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("queue down"))
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "send prometheus webhook", method: http.MethodPost, path: "/prometheus-alert", body: prometheusWebhook, wantStatus: http.StatusAccepted,
		},
		{
			name: "send empty prometheus webhook", method: http.MethodPost, path: "/prometheus-alert/C123", body: `{"alerts":[]}`, wantStatus: http.StatusBadRequest,
		},
		{
			name: "test alerts", method: http.MethodPost, path: "/alerts-test", body: validAlert, wantStatus: http.StatusNoContent,
		},
		{
			name: "get mappings", method: http.MethodGet, path: "/mappings",
			setup: func(s *Server, _ *mockFifoQueueProducer, _ *mockChannelInfoProvider) {
				s.apiSettings.Store(&coreconfig.APISettings{RoutingRules: []*coreconfig.RoutingRule{
					{Name: "team-a", Equals: []string{"a"}, Channel: "C123"},
					{Name: "fallback", MatchAll: true, Channel: "C456"},
				}})
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "get channels", method: http.MethodGet, path: "/channels", wantStatus: http.StatusOK,
		},
		{
			name: "ping", method: http.MethodGet, path: "/ping", wantStatus: http.StatusOK,
		},
		{
			name: "ping with status", method: http.MethodGet, path: "/ping?status=503", wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "get openapi spec", method: http.MethodGet, path: "/openapi.yaml", wantStatus: http.StatusOK,
		},
	}

	v := newOpenAPIValidator(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, queue, channelInfo := newTestServerWithInitializedSettings(t)
			channelInfo.On("MapChannelNameToIDIfNeeded", mock.Anything).Return("C123")
			channelInfo.On("GetChannelInfo", mock.Anything, mock.Anything).Return(&ChannelInfo{
				ChannelExists:      true,
				ManagerIsInChannel: true,
				UserCount:          10,
			}, nil)
			channelInfo.On("ManagedChannels").Return([]*internal.ChannelSummary{{ID: "C123", Name: "alerts"}})
			queue.On("Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

			if tt.setup != nil {
				tt.setup(server, queue, channelInfo)
			}

			engine := gin.New()
			server.registerRoutes(engine)

			newRequest := func() *http.Request {
				var body io.Reader = http.NoBody
				if tt.body != "" {
					body = strings.NewReader(tt.body)
				}

				req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, body)
				if tt.body != "" {
					req.Header.Set("Content-Type", "application/json")
				}

				return req
			}

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, newRequest())

			require.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			valid, errs := v.ValidateHttpResponse(newRequest(), w.Result())
			assert.True(t, valid, formatValidationErrors(errs))
		})
	}
}
