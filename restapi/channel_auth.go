package restapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/types"
)

// Rejection reasons of the channel authorization (see alertsRejectedTotalMetric).
const (
	rejectReasonForbiddenChannel    = "forbidden_channel"
	rejectReasonAuthorizationFailed = "authorization_failed"
)

// effectiveChannelAuthorizer returns the custom authorizer if it implements [types.ChannelAuthorizer], or the
// built-in channel access rules of the given settings.
func (s *Server) effectiveChannelAuthorizer(settings *config.APISettings) types.ChannelAuthorizer {
	if ca, ok := s.authorizer.(types.ChannelAuthorizer); ok {
		return ca
	}

	return types.ChannelAuthorizerFunc(func(_ context.Context, principal *types.Principal, channelID string) error {
		if settings.ChannelAccessAllowed(principal.ID, channelID) {
			return nil
		}

		return types.ErrForbidden
	})
}

// authorizeAlertChannels checks that the authenticated principal of the request may send each alert to its
// channel, and to every channel that its escalations may move the issue to (moveToChannel). It must be called
// after the channels have been resolved by setSlackChannelID. Requests without a principal (authentication
// disabled, or anonymous in permissive mode) are not restricted.
//
// It returns the first alert that is not allowed, and an *alertRejectionError with the reason
// rejectReasonForbiddenChannel, or rejectReasonAuthorizationFailed if the authorizer failed.
func (s *Server) authorizeAlertChannels(c *gin.Context, alerts []*types.Alert) (*types.Alert, error) {
	principal := principalFromContext(c)
	if principal == nil {
		return nil, nil
	}

	authorizer := s.effectiveChannelAuthorizer(s.apiSettings.Load())
	results := make(map[string]error)

	authorize := func(channelID string) error {
		channelID = strings.ToUpper(strings.TrimSpace(channelID))

		if err, ok := results[channelID]; ok {
			return err
		}

		err := authorizer.AuthorizeChannel(c.Request.Context(), principal, channelID)
		results[channelID] = err

		return err
	}

	for _, alert := range alerts {
		if err := authorize(alert.SlackChannelID); err != nil {
			return alert, channelAuthorizationError(err, "principal %s is not allowed to send alerts to channel %s", principal.ID, strings.ToUpper(alert.SlackChannelID))
		}

		for _, e := range alert.Escalation {
			if e == nil || e.MoveToChannel == "" {
				continue
			}

			if err := authorize(e.MoveToChannel); err != nil {
				return alert, channelAuthorizationError(err, "principal %s is not allowed to move issues to channel %s (escalation moveToChannel)", principal.ID, strings.ToUpper(e.MoveToChannel))
			}
		}
	}

	return nil, nil
}

// channelAuthorizationError returns an *alertRejectionError for a failed channel authorization: forbidden, with
// the given message, if err is [types.ErrForbidden], and an authorization failure otherwise.
func channelAuthorizationError(err error, format string, a ...any) error {
	if errors.Is(err, types.ErrForbidden) {
		return newAlertRejection(rejectReasonForbiddenChannel, format, a...)
	}

	return &alertRejectionError{reason: rejectReasonAuthorizationFailed, err: fmt.Errorf("channel authorization failed: %w", err)}
}

// channelAuthorizationStatus returns the HTTP status code of an error returned by authorizeAlertChannels.
func channelAuthorizationStatus(err error) int {
	if rejectReasonOf(err, rejectReasonForbiddenChannel) == rejectReasonAuthorizationFailed {
		return http.StatusInternalServerError
	}

	return http.StatusForbidden
}

// applyClientBinding sets the client of the alerts to the ID of the authenticated principal of the request,
// according to config.AuthConfig.ClientBinding. Requests without a principal are not changed.
func (s *Server) applyClientBinding(c *gin.Context, alerts []*types.Alert) {
	principal := principalFromContext(c)
	if principal == nil {
		return
	}

	switch s.cfg.Auth.EffectiveClientBinding() {
	case config.ClientBindingFill:
		for _, alert := range alerts {
			if strings.TrimSpace(alert.Client) == "" {
				alert.Client = principal.ID
			}
		}
	case config.ClientBindingOverride:
		for _, alert := range alerts {
			if alert.Client != "" && alert.Client != principal.ID {
				s.logger.WithField("principal", principal.ID).WithField("client", alert.Client).Debug("Alert client overridden by the principal ID")
			}

			alert.Client = principal.ID
		}
	}
}
