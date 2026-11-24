package config

import "fmt"

// Values of [AuthConfig.ClientBinding].
const (
	// ClientBindingFill sets the client of alerts without one to the ID of the authenticated principal.
	ClientBindingFill = "fill"

	// ClientBindingOverride always sets the client of alerts to the ID of the authenticated principal.
	ClientBindingOverride = "override"

	// ClientBindingOff leaves the client of alerts as sent.
	ClientBindingOff = "off"
)

// AuthConfig configures how the REST API enforces authentication. It only applies when authentication is
// enabled with Server.WithAPIKeyAuth or Server.WithAuthenticator; without them, the API is open.
type AuthConfig struct {
	// Permissive lets requests without credentials through anonymously, instead of rejecting them with HTTP
	// 401. Requests with credentials are still authenticated and authorized: invalid credentials get HTTP 401,
	// and missing scopes get HTTP 403. A request with an Authorization header counts as having credentials,
	// even if no authenticator understands it (e.g. an unsupported scheme, or a non-JWT bearer token with only
	// OIDC enabled): it gets HTTP 401.
	//
	// Use it to roll out authentication without a hard cutover: enable it, migrate the clients one by one,
	// and disable it when the http_auth_requests_total{result="anonymous"} metric has dropped to zero.
	//
	// Default: false.
	Permissive bool `json:"permissive" mapstructure:"permissive" yaml:"permissive"`

	// ClientBinding binds the client of alerts (types.Alert.Client, used for insights attribution) to the ID of
	// the authenticated principal that sent them:
	//   - [ClientBindingFill]: alerts without a client get the principal ID
	//   - [ClientBindingOverride]: all alerts get the principal ID, so that clients cannot be spoofed
	//   - [ClientBindingOff]: the client is left as sent
	//
	// Alerts from unauthenticated requests (anonymous in permissive mode) are never changed. Principal IDs are
	// never truncated: the API rejects principals with IDs longer than types.MaxClientLength (HTTP 401).
	//
	// Default: "" (which means [ClientBindingFill]).
	ClientBinding string `json:"clientBinding" mapstructure:"clientBinding" yaml:"clientBinding"`
}

// EffectiveClientBinding returns [AuthConfig.ClientBinding], or [ClientBindingFill] if it is empty. A nil
// AuthConfig has the default client binding.
func (c *AuthConfig) EffectiveClientBinding() string {
	if c == nil || c.ClientBinding == "" {
		return ClientBindingFill
	}

	return c.ClientBinding
}

// Validate checks that [AuthConfig.ClientBinding] is empty or a known value.
func (c *AuthConfig) Validate() error {
	switch c.ClientBinding {
	case "", ClientBindingFill, ClientBindingOverride, ClientBindingOff:
		return nil
	default:
		return fmt.Errorf("client binding must be %q, %q or %q", ClientBindingFill, ClientBindingOverride, ClientBindingOff)
	}
}
