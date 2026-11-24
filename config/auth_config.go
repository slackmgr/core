package config

// AuthConfig configures how the REST API enforces authentication. It only applies when authentication is
// enabled with Server.WithAPIKeyAuth or Server.WithAuthenticator; without them, the API is open.
type AuthConfig struct {
	// Permissive lets requests without credentials through anonymously, instead of rejecting them with HTTP
	// 401. Requests with credentials are still authenticated and authorized: invalid credentials get HTTP 401,
	// and missing scopes get HTTP 403.
	//
	// Use it to roll out authentication without a hard cutover: enable it, migrate the clients one by one,
	// and disable it when the http_auth_requests_total{result="anonymous"} metric has dropped to zero.
	//
	// Default: false.
	Permissive bool `json:"permissive" mapstructure:"permissive" yaml:"permissive"`
}
