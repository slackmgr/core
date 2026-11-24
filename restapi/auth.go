package restapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/slackmgr/types"
)

const httpAuthRequestsMetric = "http_auth_requests_total"

// Results of the authentication and authorization of a request, used as the result label of
// http_auth_requests_total.
const (
	authResultAuthenticated      = "authenticated"
	authResultAnonymous          = "anonymous"
	authResultMissingCredentials = "missing_credentials"
	authResultInvalidCredentials = "invalid_credentials" // #nosec G101 -- metric label value, not a credential
	authResultForbidden          = "forbidden"
	authResultError              = "error"
)

// allAuthResults lists every auth result, used to pre-warm metric label combinations.
var allAuthResults = []string{ //nolint:gochecknoglobals // Constant list
	authResultAuthenticated,
	authResultAnonymous,
	authResultMissingCredentials,
	authResultInvalidCredentials,
	authResultForbidden,
	authResultError,
}

// principalContextKey is the gin context key of the authenticated *types.Principal of a request.
const principalContextKey = "slackmgr.principal"

// authAttributeMethod is the [types.Principal.Attributes] key of the authentication method.
const authAttributeMethod = "auth_method"

// authMethodAPIKey is the authentication method of principals authenticated by an API key.
const authMethodAPIKey = "api_key"

// wwwAuthenticateChallenge is the WWW-Authenticate header of HTTP 401 responses.
const wwwAuthenticateChallenge = `Bearer realm="slackmgr"`

// WithAPIKeyAuth enables the built-in API key authentication. The API keys and their scopes are read from the
// current API settings (config.APISettings.APIKeys) for each request, so keys can be added, rotated and revoked
// with [Server.UpdateSettings].
//
// Enabling any authentication (this method or [Server.WithAuthenticator]) protects all endpoints except
// GET /ping and GET /openapi.yaml. Each protected endpoint requires a scope (see [types.ScopeAlertsWrite] and
// the other scopes). Authentication cannot be disabled at runtime: a settings update without API keys rejects
// all requests (or lets them through anonymously in permissive mode, see config.AuthConfig).
//
// When combined with [Server.WithAuthenticator], API keys are tried first.
func (s *Server) WithAPIKeyAuth() *Server {
	s.apiKeyAuth = true
	return s
}

// WithAuthenticator enables authentication with a custom [types.Authenticator], e.g. for OIDC tokens. Use
// [types.ChainAuthenticators] to combine several. Passing nil is a no-op.
//
// See [Server.WithAPIKeyAuth] for which endpoints are protected. When both are used, API keys are tried
// first, then the custom authenticator.
func (s *Server) WithAuthenticator(authenticator types.Authenticator) *Server {
	if authenticator == nil {
		return s
	}

	s.authenticator = authenticator

	return s
}

// WithAuthorizer sets a custom [types.Authorizer], which decides whether an authenticated principal may call an
// endpoint that requires a given scope. If not called, a principal must have the scope (see
// [types.Principal.HasScope]). Passing nil is a no-op. The authorizer is only used when authentication is
// enabled.
func (s *Server) WithAuthorizer(authorizer types.Authorizer) *Server {
	if authorizer == nil {
		return s
	}

	s.authorizer = authorizer

	return s
}

// authEnabled reports whether any authentication is enabled.
func (s *Server) authEnabled() bool {
	return s.apiKeyAuth || s.authenticator != nil
}

// authPermissive reports whether requests without credentials are let through anonymously.
func (s *Server) authPermissive() bool {
	return s.cfg.Auth != nil && s.cfg.Auth.Permissive
}

// effectiveAuthenticator returns the authenticator of the server, or nil if authentication is disabled.
func (s *Server) effectiveAuthenticator() types.Authenticator {
	switch {
	case s.apiKeyAuth && s.authenticator != nil:
		return types.ChainAuthenticators(types.AuthenticatorFunc(s.authenticateAPIKey), s.authenticator)
	case s.apiKeyAuth:
		return types.AuthenticatorFunc(s.authenticateAPIKey)
	default:
		return s.authenticator
	}
}

// effectiveAuthorizer returns the authorizer of the server: the custom authorizer, or a scope check.
func (s *Server) effectiveAuthorizer() types.Authorizer {
	if s.authorizer != nil {
		return s.authorizer
	}

	return types.AuthorizerFunc(authorizeScope)
}

// authorizeScope allows the action if the principal has the scope.
func authorizeScope(_ context.Context, principal *types.Principal, scope string) error {
	if principal.HasScope(scope) {
		return nil
	}

	return types.ErrForbidden
}

// authenticateAPIKey authenticates the request with an API key from the current API settings, sent as a bearer
// token or as the password of HTTP basic authentication.
func (s *Server) authenticateAPIKey(r *http.Request) (*types.Principal, error) {
	key, err := apiKeyFromRequest(r)
	if err != nil {
		return nil, err
	}

	apiKey := s.apiSettings.Load().LookupAPIKey(key)
	if apiKey == nil {
		return nil, fmt.Errorf("unknown API key: %w", types.ErrInvalidCredentials)
	}

	return &types.Principal{
		ID:         apiKey.ID,
		Scopes:     slices.Clone(apiKey.Scopes),
		Attributes: map[string]string{authAttributeMethod: authMethodAPIKey},
	}, nil
}

// apiKeyFromRequest returns the API key in the Authorization header: a bearer token, or the password of HTTP
// basic authentication (the username if the password is empty). It returns [types.ErrNoCredentials] if the
// header is missing or has another scheme, and [types.ErrInvalidCredentials] if it is malformed or empty.
func apiKeyFromRequest(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", types.ErrNoCredentials
	}

	scheme, credentials, _ := strings.Cut(header, " ")
	credentials = strings.TrimSpace(credentials)

	switch {
	case strings.EqualFold(scheme, "Bearer"):
		if credentials == "" {
			return "", fmt.Errorf("empty bearer token: %w", types.ErrInvalidCredentials)
		}

		return credentials, nil
	case strings.EqualFold(scheme, "Basic"):
		decoded, err := base64.StdEncoding.DecodeString(credentials)
		if err != nil {
			return "", fmt.Errorf("malformed basic credentials: %w", types.ErrInvalidCredentials)
		}

		username, password, _ := strings.Cut(string(decoded), ":")
		if password == "" {
			password = username
		}

		if password == "" {
			return "", fmt.Errorf("empty basic credentials: %w", types.ErrInvalidCredentials)
		}

		return password, nil
	default:
		return "", types.ErrNoCredentials
	}
}

// authMiddleware returns a middleware that authenticates the request with authenticator, and authorizes the
// principal for scope with authorizer. A nil authenticator means that authentication is disabled, and the
// middleware lets all requests through.
//
// A principal with an invalid ID (see validatePrincipalID) is rejected as having invalid credentials.
//
// A request with an Authorization header that the authenticator does not understand ([types.ErrNoCredentials])
// is rejected as having invalid credentials, also in permissive mode.
//
// Auth failures are not reported to the error report channel, since unauthenticated callers could flood it.
func (s *Server) authMiddleware(scope string, authenticator types.Authenticator, authorizer types.Authorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authenticator == nil {
			c.Next()
			return
		}

		principal, err := authenticator.Authenticate(c.Request)

		switch {
		case err == nil && principal != nil:
			if idErr := validatePrincipalID(principal.ID); idErr != nil {
				s.abortAuth(c, http.StatusUnauthorized, authResultInvalidCredentials, "Invalid credentials", idErr)
				return
			}
		case errors.Is(err, types.ErrNoCredentials):
			// An Authorization header that no authenticator understands (e.g. an unsupported scheme, or a
			// non-JWT bearer token with only OIDC enabled) means credentials were sent, but are invalid. Such
			// requests are never let through anonymously, also not in permissive mode.
			if strings.TrimSpace(c.GetHeader("Authorization")) != "" {
				s.abortAuth(c, http.StatusUnauthorized, authResultInvalidCredentials, "Invalid credentials", err)
				return
			}

			if s.authPermissive() {
				s.metrics.CounterInc(httpAuthRequestsMetric, authResultAnonymous)
				c.Next()

				return
			}

			s.abortAuth(c, http.StatusUnauthorized, authResultMissingCredentials, "Authentication required", err)

			return
		case err == nil, errors.Is(err, types.ErrInvalidCredentials):
			s.abortAuth(c, http.StatusUnauthorized, authResultInvalidCredentials, "Invalid credentials", err)
			return
		default:
			s.abortAuth(c, http.StatusInternalServerError, authResultError, "Internal server error", fmt.Errorf("authentication failed: %w", err))
			return
		}

		logger := s.logger.WithField("principal", principal.ID)

		if err := authorizer.Authorize(c.Request.Context(), principal, scope); err != nil {
			if errors.Is(err, types.ErrForbidden) {
				s.abortAuth(c, http.StatusForbidden, authResultForbidden, fmt.Sprintf("Forbidden: the %s scope is required", scope), err, principal)
			} else {
				s.abortAuth(c, http.StatusInternalServerError, authResultError, "Internal server error", fmt.Errorf("authorization failed: %w", err), principal)
			}

			return
		}

		s.metrics.CounterInc(httpAuthRequestsMetric, authResultAuthenticated)
		logger.Debugf("Request authenticated for scope %s", scope)

		c.Set(principalContextKey, principal)
		c.Next()
	}
}

// abortAuth logs a failed authentication or authorization, counts it, and aborts the request with an error
// response. The principal is optional (only known for authorization failures).
func (s *Server) abortAuth(c *gin.Context, status int, result, message string, err error, principal ...*types.Principal) {
	s.metrics.CounterInc(httpAuthRequestsMetric, result)

	logger := s.logger.
		WithField("client_ip", c.ClientIP()).
		WithField("method", c.Request.Method).
		WithField("path", c.Request.URL.Path).
		WithField("auth_result", result)

	if len(principal) > 0 && principal[0] != nil {
		logger = logger.WithField("principal", principal[0].ID)
	}

	if err != nil {
		logger = logger.WithField("error", err.Error())
	}

	if status >= http.StatusInternalServerError {
		logger.Error("Request authentication failed")
	} else {
		logger.Info("Request authentication failed")
	}

	if status == http.StatusUnauthorized {
		c.Header("WWW-Authenticate", wwwAuthenticateChallenge)
	}

	c.AbortWithStatusJSON(status, errorResponse{Error: message})
}

// validatePrincipalID checks that a principal ID can be used as is in access rules and as the client of alerts
// (see config.AuthConfig.ClientBinding): it must be non-empty, without leading or trailing whitespace, and at most
// [types.MaxClientLength] characters, so that it is never trimmed or truncated (which could make two principals
// share a client). It returns an error wrapping [types.ErrInvalidCredentials] otherwise.
func validatePrincipalID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("principal ID is empty: %w", types.ErrInvalidCredentials)
	case strings.TrimSpace(id) != id:
		return fmt.Errorf("principal ID has leading or trailing whitespace: %w", types.ErrInvalidCredentials)
	case utf8.RuneCountInString(id) > types.MaxClientLength:
		return fmt.Errorf("principal ID is longer than %d characters: %w", types.MaxClientLength, types.ErrInvalidCredentials)
	default:
		return nil
	}
}

// principalFromContext returns the authenticated principal of the request, or nil if there is none
// (authentication disabled, a public endpoint, or an anonymous request in permissive mode).
func principalFromContext(c *gin.Context) *types.Principal {
	if v, ok := c.Get(principalContextKey); ok {
		if principal, ok := v.(*types.Principal); ok {
			return principal
		}
	}

	return nil
}

// registerAuthMetrics registers the auth metrics, initialized to zero for all results, if authentication is
// enabled.
func (s *Server) registerAuthMetrics() {
	if !s.authEnabled() {
		return
	}

	s.metrics.RegisterCounter(httpAuthRequestsMetric, "Total requests to protected endpoints by authentication and authorization result", "result")

	for _, result := range allAuthResults {
		s.metrics.CounterAdd(httpAuthRequestsMetric, 0, result)
	}
}

// logAuthState logs whether authentication is enabled, and warns about API key authentication without keys.
func (s *Server) logAuthState() {
	if !s.authEnabled() {
		s.logger.Info("API authentication is disabled: all endpoints are open")
		return
	}

	s.logger.
		WithField("api_key_auth", s.apiKeyAuth).
		WithField("custom_authenticator", s.authenticator != nil).
		WithField("custom_authorizer", s.authorizer != nil).
		WithField("permissive", s.authPermissive()).
		Info("API authentication is enabled")

	if s.authPermissive() {
		s.logger.Info("API authentication is permissive: requests without credentials are let through anonymously")
	}

	s.logAPIKeyState()
}

// logAPIKeyState logs an error if API key authentication is the only authentication, and no keys are configured.
func (s *Server) logAPIKeyState() {
	if s.apiKeyAuth && s.authenticator == nil && len(s.apiSettings.Load().APIKeys) == 0 {
		s.logger.Error("API key authentication is enabled, but no API keys are configured: all requests to protected endpoints are rejected (or anonymous in permissive mode)")
	}
}
