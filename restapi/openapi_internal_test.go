package restapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
// It uses the deprecated POST /alert alias, which accepts all body forms in the spec (see TestOpenAPISpec_AlertBodyForms).
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

	// valid is whether the server accepts the body. serverOnly marks rules across fields, which are
	// described in the spec, but validated only by the server (to keep the schemas plain for code generators).
	// lenient marks values that the server accepts for compatibility, but the spec does not (e.g. empty enums).
	tests := []struct {
		name       string
		body       any
		valid      bool
		serverOnly bool
		lenient    bool
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
			name:    "empty severity",
			body:    map[string]any{"header": "Header", "slackChannelId": "C123", "severity": ""},
			valid:   true,
			lenient: true,
		},
		{
			name:    "upper case severity",
			body:    map[string]any{"header": "Header", "slackChannelId": "C123", "severity": "Error"},
			valid:   true,
			lenient: true,
		},
		{
			name:    "legacy severity",
			body:    map[string]any{"header": "Header", "slackChannelId": "C123", "severity": "critical"},
			valid:   true,
			lenient: true,
		},
		{
			name: "omitted webhook enums",
			body: clientAlert(func(a *types.Alert) {
				a.Webhooks = webhook(func(w *types.Webhook) {
					w.AfterClick = &types.WebhookAfterClick{StatusText: "Restarting"}
				})
			}),
			valid: true,
		},
		{
			name: "explicitly empty webhook enums",
			body: map[string]any{"header": "Header", "slackChannelId": "C123", "webhooks": []any{map[string]any{
				"id": "restart", "url": "https://example.com/restart", "buttonText": "Restart",
				"buttonStyle": "", "accessLevel": "", "displayMode": "", "afterClick": map[string]any{"hideButtons": ""},
			}}},
			valid:   true,
			lenient: true,
		},
		{
			name:       "header and text both missing",
			body:       map[string]any{"slackChannelId": "C123"},
			valid:      false,
			serverOnly: true,
		},
		{
			name:       "header and text both empty",
			body:       clientAlert(func(a *types.Alert) { a.Header = "" }),
			valid:      false,
			serverOnly: true,
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
			name:       "issue follow-up without auto resolve",
			body:       clientAlert(func(a *types.Alert) { a.IssueFollowUpEnabled = true }),
			valid:      false,
			serverOnly: true,
		},
		{
			name: "auto resolve too high",
			body: clientAlert(func(a *types.Alert) {
				a.IssueFollowUpEnabled = true
				a.AutoResolveSeconds = types.MaxAutoResolveSeconds + 1
			}),
			valid:      false,
			serverOnly: true,
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
			valid:      false,
			serverOnly: true,
		},
		{
			name: "webhook with inputs without confirmation dialog",
			body: clientAlert(func(a *types.Alert) {
				a.Webhooks = webhook(func(w *types.Webhook) {
					w.SkipConfirmationDialog = true
					w.PlainTextInput = []*types.WebhookPlainTextInput{{ID: "reason", MaxLength: 100}}
				})
			}),
			valid:      false,
			serverOnly: true,
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

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/alert", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			specValid, errs := v.ValidateHttpRequestSync(req)
			assert.Equal(t, (tt.valid && !tt.lenient) || tt.serverOnly, specValid, "spec validation: %s", formatValidationErrors(errs))

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

// TestOpenAPISpec_AlertBodyForms checks which alert body forms each path accepts in the spec. POST /alerts only
// accepts an alerts array (AlertBatch), so that generated clients get a plain request type. The server accepts all
// forms on all paths.
func TestOpenAPISpec_AlertBodyForms(t *testing.T) {
	t.Parallel()

	alert := `{"header":"Header","slackChannelId":"C123"}`

	forms := map[string]string{
		"batch":  `{"alerts":[` + alert + `]}`,
		"single": alert,
		"array":  `[` + alert + `]`,
	}

	tests := []struct {
		path  string
		forms []string
	}{
		{path: "/alerts", forms: []string{"batch"}},
		{path: "/alerts/C123", forms: []string{"batch"}},
		{path: "/alert", forms: []string{"batch", "single", "array"}},
		{path: "/alert/C123", forms: []string{"batch", "single", "array"}},
		{path: "/slackalert", forms: []string{"batch", "single", "array"}},
		{path: "/slackalert/C123", forms: []string{"batch", "single", "array"}},
		{path: "/slackalerts", forms: []string{"batch", "single", "array"}},
		{path: "/slackalerts/C123", forms: []string{"batch", "single", "array"}},
		{path: "/alerts-test", forms: []string{"batch", "single", "array"}},
		{path: "/alerts-test/C123", forms: []string{"batch", "single", "array"}},
	}

	v := newOpenAPIValidator(t)

	for _, tt := range tests {
		for form, body := range forms {
			t.Run(tt.path+" "+form, func(t *testing.T) {
				t.Parallel()

				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tt.path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")

				valid, errs := v.ValidateHttpRequestSync(req)
				assert.Equal(t, slices.Contains(tt.forms, form), valid, formatValidationErrors(errs))

				assert.NoError(t, serverValidateAlerts([]byte(body)), "the server accepts all forms")
			})
		}
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

	validAlert := `{"alerts":[{"header":"Header","slackChannelId":"C123"}]}`

	prometheusWebhook := `{"version":"4","groupKey":"g","status":"firing","receiver":"slackmgr","alerts":[` +
		`{"status":"firing","labels":{"alertname":"HighLatency","channel":"C123"},"annotations":{"summary":"Latency is high"},` +
		`"startsAt":"2026-11-14T10:00:00Z","endsAt":"0001-01-01T00:00:00Z"}]}`

	enableAuth := func(s *Server, _ *mockFifoQueueProducer, _ *mockChannelInfoProvider) {
		s.WithAPIKeyAuth()
		s.apiSettings.Store(testAPIKeySettings(t))
	}

	tests := []struct {
		name          string
		method        string
		path          string
		body          string
		authorization string
		setup         func(s *Server, q *mockFifoQueueProducer, c *mockChannelInfoProvider)
		wantStatus    int
	}{
		{
			name: "send alerts", method: http.MethodPost, path: "/alerts", body: validAlert, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts to channel", method: http.MethodPost, path: "/alerts/C123", body: `{"alerts":[{"header":"Header"}]}`, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts with deprecated alias", method: http.MethodPost, path: "/slackalerts", body: validAlert, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts that are all ignored", method: http.MethodPost, path: "/alerts",
			body:       `{"alerts":[{"text":"some noise","slackChannelId":"C123","ignoreIfTextContains":["noise"]}]}`,
			wantStatus: http.StatusNoContent,
		},
		{
			name: "send invalid alert", method: http.MethodPost, path: "/alerts", body: `{"alerts":[{"slackChannelId":"C123"}]}`, wantStatus: http.StatusBadRequest,
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
		{
			name: "send alerts with api key", method: http.MethodPost, path: "/alerts", body: validAlert,
			authorization: bearer(testKeyWriter), setup: enableAuth, wantStatus: http.StatusAccepted,
		},
		{
			name: "send alerts without credentials", method: http.MethodPost, path: "/alerts", body: validAlert,
			setup: enableAuth, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "send alerts with invalid credentials", method: http.MethodPost, path: "/alerts", body: validAlert,
			authorization: bearer("unknown"), setup: enableAuth, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "send alerts without scope", method: http.MethodPost, path: "/alerts", body: validAlert,
			authorization: bearer(testKeyReader), setup: enableAuth, wantStatus: http.StatusForbidden,
		},
		{
			name: "send prometheus webhook without scope", method: http.MethodPost, path: "/prometheus-alert", body: prometheusWebhook,
			authorization: bearer(testKeyReader), setup: enableAuth, wantStatus: http.StatusForbidden,
		},
		{
			name: "test alerts without credentials", method: http.MethodPost, path: "/alerts-test", body: validAlert,
			setup: enableAuth, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "get channels without credentials", method: http.MethodGet, path: "/channels", setup: enableAuth, wantStatus: http.StatusUnauthorized,
		},
		{
			name: "get mappings without scope", method: http.MethodGet, path: "/mappings", authorization: basic("", testKeyWriter),
			setup: enableAuth, wantStatus: http.StatusForbidden,
		},
		{
			name: "get channels with failing authenticator", method: http.MethodGet, path: "/channels", authorization: bearer("x"),
			setup: func(s *Server, _ *mockFifoQueueProducer, _ *mockChannelInfoProvider) {
				s.WithAuthenticator(types.AuthenticatorFunc(func(*http.Request) (*types.Principal, error) {
					return nil, errors.New("identity provider unavailable")
				}))
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name: "ping without credentials", method: http.MethodGet, path: "/ping", setup: enableAuth, wantStatus: http.StatusOK,
		},
		{
			name: "send alerts to forbidden channel", method: http.MethodPost, path: "/alerts", body: validAlert,
			authorization: bearer(testKeyWriter),
			setup: func(s *Server, q *mockFifoQueueProducer, c *mockChannelInfoProvider) {
				enableAuth(s, q, c)
				s.WithAuthorizer(&channelAuthorizerStub{err: types.ErrForbidden})
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "test alerts to forbidden channel", method: http.MethodPost, path: "/alerts-test", body: validAlert,
			authorization: bearer(testKeyAdmin),
			setup: func(s *Server, q *mockFifoQueueProducer, c *mockChannelInfoProvider) {
				enableAuth(s, q, c)
				s.WithAuthorizer(&channelAuthorizerStub{err: types.ErrForbidden})
			},
			wantStatus: http.StatusForbidden,
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

				if tt.authorization != "" {
					req.Header.Set("Authorization", tt.authorization)
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

// TestOpenAPISpec_Security checks that the security requirements in the spec match the scopes that the server
// requires: every operation lists the bearerAuth and basicAuth schemes with the scope of its route, plus an empty
// requirement (authentication is optional), and public operations list none.
func TestOpenAPISpec_Security(t *testing.T) {
	t.Parallel()

	publicOperations := []string{"GET /ping", "GET /openapi.yaml"}

	for name, op := range specOperations(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			method, path, _ := strings.Cut(name, " ")
			path = strings.ReplaceAll(path, "{slackChannelId}", "C123")

			// The server must agree on whether the operation is public.
			server, _ := newAuthTestServer(t)
			w := serve(t, server, method, path, "")

			if slices.Contains(publicOperations, name) {
				assert.Empty(t, op.Security, "public operation must have no security requirements")
				assert.NotEqual(t, http.StatusUnauthorized, w.Code)

				return
			}

			assert.Equal(t, http.StatusUnauthorized, w.Code, "protected operation must reject requests without credentials")
			require.Len(t, op.Security, 3)

			var scope string

			for i, schemeName := range []string{"bearerAuth", "basicAuth"} {
				scopes, ok := op.Security[i].Requirements.Get(schemeName)
				require.True(t, ok, "security[%d] must be %s", i, schemeName)
				require.Len(t, scopes, 1)
				require.True(t, types.IsKnownScope(scopes[0]), scopes[0])

				if scope == "" {
					scope = scopes[0]
				}

				assert.Equal(t, scope, scopes[0], "both schemes must require the same scope")
			}

			assert.Zero(t, op.Security[2].Requirements.Len(), "security[2] must be empty (authentication is optional)")

			_, has401 := op.Responses.Codes.Get("401")
			_, has403 := op.Responses.Codes.Get("403")
			assert.True(t, has401, "protected operation must document 401")
			assert.True(t, has403, "protected operation must document 403")

			// A principal with exactly the documented scope passes, and one with another scope is forbidden.
			for _, other := range []string{types.ScopeAlertsWrite, types.ScopeAlertsTest, types.ScopeConfigRead} {
				server := newTestServerWithChannels(t)
				server.WithAuthenticator(types.AuthenticatorFunc(func(*http.Request) (*types.Principal, error) {
					return &types.Principal{ID: "p", Scopes: []string{other}}, nil
				}))

				w := serve(t, server, method, path, bearer("x"))

				if other == scope {
					assert.NotContains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, w.Code, "scope %s must be allowed", other)
				} else {
					assert.Equal(t, http.StatusForbidden, w.Code, "scope %s must be forbidden", other)
				}
			}
		})
	}
}

// TestOpenAPISpec_SchemaFields checks that the schemas in the spec have exactly the JSON fields of the Go types
// they describe, so that a field added to (or removed from) a type cannot be missed in the spec.
func TestOpenAPISpec_SchemaFields(t *testing.T) {
	t.Parallel()

	schemaTypes := map[string]reflect.Type{
		"Alert":                 reflect.TypeFor[types.Alert](),
		"Field":                 reflect.TypeFor[types.Field](),
		"Escalation":            reflect.TypeFor[types.Escalation](),
		"Webhook":               reflect.TypeFor[types.Webhook](),
		"WebhookPlainTextInput": reflect.TypeFor[types.WebhookPlainTextInput](),
		"WebhookCheckboxInput":  reflect.TypeFor[types.WebhookCheckboxInput](),
		"WebhookCheckboxOption": reflect.TypeFor[types.WebhookCheckboxOption](),
		"WebhookAfterClick":     reflect.TypeFor[types.WebhookAfterClick](),
		"PrometheusWebhook":     reflect.TypeFor[PrometheusWebhook](),
		"PrometheusAlert":       reflect.TypeFor[PrometheusAlert](),
		"Mappings":              reflect.TypeFor[legacyAlertMappings](),
		"MappingRule":           reflect.TypeFor[legacyMappingRule](),
		"ChannelSummary":        reflect.TypeFor[internal.ChannelSummary](),
		"ErrorResponse":         reflect.TypeFor[errorResponse](),
	}

	t.Run("every struct type in an alert has a schema", func(t *testing.T) {
		t.Parallel()

		mapped := make(map[reflect.Type]bool, len(schemaTypes))
		for _, typ := range schemaTypes {
			mapped[typ] = true
		}

		for _, typ := range nestedStructTypes(reflect.TypeFor[types.Alert]()) {
			assert.True(t, mapped[typ], "%s is used in types.Alert, but is not mapped to a schema in this test", typ)
		}
	})

	model, _ := openAPI()

	for name, typ := range schemaTypes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			proxy := model.Components.Schemas.GetOrZero(name)
			require.NotNil(t, proxy, "schema %s not found", name)

			schema := proxy.Schema()
			require.NotNil(t, schema, "schema %s cannot be built", name)

			var properties []string
			if schema.Properties != nil {
				for prop := range schema.Properties.KeysFromOldest() {
					properties = append(properties, prop)
				}
			}

			fields := jsonFieldNames(typ)

			slices.Sort(properties)
			slices.Sort(fields)

			assert.Equal(t, fields, properties, "the properties of schema %s must match the JSON fields of %s", name, typ)
		})
	}
}

// jsonFieldNames returns the JSON field names of a struct type.
func jsonFieldNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())

	for field := range typ.Fields() {
		if !field.IsExported() {
			continue
		}

		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")

		switch name {
		case "-":
			continue
		case "":
			name = field.Name
		}

		names = append(names, name)
	}

	return names
}

// nestedStructTypes returns the struct types used by the fields of a struct type, recursively.
func nestedStructTypes(typ reflect.Type) []reflect.Type {
	seen := map[reflect.Type]bool{}

	var visit func(t reflect.Type)

	visit = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}

		// time.Time is a string in JSON.
		if t.Kind() != reflect.Struct || seen[t] || t == reflect.TypeFor[time.Time]() {
			return
		}

		seen[t] = true

		for field := range t.Fields() {
			if field.IsExported() {
				visit(field.Type)
			}
		}
	}

	visit(typ)

	delete(seen, typ)

	return slices.Collect(maps.Keys(seen))
}
