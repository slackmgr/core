package restapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
)

const NA = "N/A"

// alertsInput is a struct that represents the input of the alerts endpoint.
// It supports two formats (for backwards compatibility reasons):
//  1. A single alert, where all fields are at the root level
//  2. Multiple alerts, where each alert is an object in an array
type alertsInput struct {
	common.Alert

	Alerts []*common.Alert `json:"alerts"`
}

func (s *Server) handleAlerts(c *gin.Context) {
	started := time.Now()

	// ContentLength == 0 means explicitly empty body.
	// ContentLength == -1 means chunked encoding (unknown length), which is valid.
	if c.Request.ContentLength == 0 {
		err := errors.New("missing POST body")
		s.writeErrorResponse(c, err, http.StatusBadRequest, nil)
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		err = fmt.Errorf("failed to read POST body: %w", err)
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil)
		return
	}

	s.debugLogRequest(c, body)

	alerts, err := parseAlertInput(body)
	if err != nil {
		err = fmt.Errorf("failed to parse POST body: %w", err)
		s.writeErrorResponse(c, err, http.StatusBadRequest, nil)
		return
	}

	s.processAlerts(c, alerts, started)
}

func (s *Server) processAlerts(c *gin.Context, alerts []*common.Alert, started time.Time) {
	if len(alerts) == 0 {
		c.Status(http.StatusNoContent)
		return
	}

	if err := s.setSlackChannelID(c, alerts...); err != nil {
		s.writeErrorResponse(c, err, http.StatusBadRequest, nil)
		return
	}

	alertsByChannel := make(map[string][]*common.Alert)
	atLeastOneAlert := false

	for _, alert := range alerts {
		alert.Clean()

		if err := alert.Validate(); err != nil {
			err = fmt.Errorf("input validation failed: %w", err)
			s.writeErrorResponse(c, err, http.StatusBadRequest, alert)
			return
		}

		ignore, ignoreReason := ignoreAlert(alert)

		if ignore {
			s.logAlerts("Alert ignored", ignoreReason, started, alert)
			continue
		}

		channel := alert.SlackChannelID
		atLeastOneAlert = true
		channelAlerts, ok := alertsByChannel[channel]

		if ok {
			alertsByChannel[channel] = append(channelAlerts, alert)
		} else {
			alertsByChannel[channel] = []*common.Alert{alert}
		}
	}

	if !atLeastOneAlert {
		c.Status(http.StatusNoContent)
		return
	}

	alertLimitPerChannel := s.cfg.RateLimitPerAlertChannel.AllowedBurst

	// Check that no channel exceeds the maximum number of alerts allowed per request.
	for channel, channelAlerts := range alertsByChannel {
		if len(channelAlerts) > alertLimitPerChannel {
			err := fmt.Errorf("too many alerts for channel %s: %d alerts (limit: %d)", channel, len(channelAlerts), alertLimitPerChannel)
			s.writeErrorResponse(c, err, http.StatusBadRequest, channelAlerts[0])
			return
		}
	}

	for channel, channelAlerts := range alertsByChannel {
		channelInfo, err := s.channelInfoProvider.GetChannelInfo(c.Request.Context(), channel)
		if err != nil {
			err = fmt.Errorf("failed to fetch info for channel %s: %w", channel, err)
			s.writeErrorResponse(c, err, http.StatusInternalServerError, channelAlerts[0])
			return
		}

		if err := s.validateChannelInfo(channel, channelInfo); err != nil {
			s.writeErrorResponse(c, err, http.StatusBadRequest, channelAlerts[0])
			return
		}

		alertCount := len(channelAlerts)

		if err = s.waitForRateLimit(c.Request.Context(), channel, alertCount); err != nil {
			if errors.Is(err, ErrRateLimit) {
				err = fmt.Errorf("rate limit exceeded for %d alerts in channel %s", alertCount, channel)
				s.writeErrorResponse(c, err, http.StatusTooManyRequests, channelAlerts[0])
				return
			}

			s.writeErrorResponse(c, err, http.StatusInternalServerError, channelAlerts[0])
			return
		}

		for _, alert := range channelAlerts {
			for _, w := range alert.Webhooks {
				if err := internal.EncryptWebhookPayload(w, []byte(s.cfg.EncryptionKey)); err != nil {
					err = fmt.Errorf("failed to encrypt webhook payload: %w", err)
					s.writeErrorResponse(c, err, http.StatusInternalServerError, alert)
					return
				}
			}

			if err := s.queueAlert(c.Request.Context(), alert); err != nil {
				s.writeErrorResponse(c, err, http.StatusInternalServerError, alert)
				return
			}

			s.logAlerts("Alert accepted", "", started, alert)
		}
	}

	c.Status(http.StatusAccepted)
}

// processQueuedAlert processes a single alert from the raw alert input queue (rather than from an API request).
// It is similar to processAlerts, but skips for example rate limiting.
// The function returns a processingError to indicate whether the error is retryable or not.
func (s *Server) processQueuedAlert(ctx context.Context, alert *common.Alert) error {
	started := time.Now()

	// Attempt to set the Slack channel ID (if not already set).
	// Any errors here are non-retryable, as they indicate invalid input.
	if err := s.setSlackChannelID(nil, alert); err != nil {
		return newNonRetryableProcessingError("failed to determine Slack channel ID: %w", err)
	}

	alert.Clean()

	// Validate the alert body.
	// Any errors here are non-retryable, as they indicate invalid input.
	if err := alert.Validate(); err != nil {
		return newNonRetryableProcessingError("input validation failed: %w", err)
	}

	ignore, ignoreReason := ignoreAlert(alert)

	if ignore {
		s.logAlerts("Alert ignored", ignoreReason, started, alert)
		return nil
	}

	// Find the channel info for the alert channel.
	// Errors here are retryable, as they may indicate transient issues when communicating with Slack.
	channelInfo, err := s.channelInfoProvider.GetChannelInfo(ctx, alert.SlackChannelID)
	if err != nil {
		return newRetryableProcessingError("failed to fetch info for channel %s: %w", alert.SlackChannelID, err)
	}

	// Check that the channel is valid for posting alerts.
	// Any errors here are non-retryable, as they indicate invalid input.
	if err := s.validateChannelInfo(alert.SlackChannelID, channelInfo); err != nil {
		return newNonRetryableProcessingError("alert channel validation failed: %w", err)
	}

	// Encrypt any webhook payloads.
	// Errors here are non-retryable, as they indicate invalid input or invalid API configuration.
	for _, w := range alert.Webhooks {
		if err := internal.EncryptWebhookPayload(w, []byte(s.cfg.EncryptionKey)); err != nil {
			return newNonRetryableProcessingError("failed to encrypt webhook payload: %w", err)
		}
	}

	// Queue the alert for processing.
	// Errors here are retryable, as they may indicate transient queuing issues.
	if err := s.queueAlert(ctx, alert); err != nil {
		return newRetryableProcessingError("failed to queue alert: %w", err)
	}

	s.logAlerts("Alert accepted", "", started, alert)

	return nil
}

func (s *Server) handleAlertsTest(c *gin.Context) {
	// ContentLength == 0 means explicitly empty body.
	// ContentLength == -1 means chunked encoding (unknown length), which is valid.
	if c.Request.ContentLength == 0 {
		err := errors.New("missing POST body")
		s.writeErrorResponse(c, err, http.StatusBadRequest, nil)
		return
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		err = fmt.Errorf("failed to read POST body: %w", err)
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil)
		return
	}

	alerts, err := parseAlertInput(body)
	if err != nil {
		err = fmt.Errorf("failed to parse POST body: %w", err)
		s.writeErrorResponse(c, err, http.StatusBadRequest, nil)
		return
	}

	if err := s.setSlackChannelID(c, alerts...); err != nil {
		s.writeErrorResponse(c, err, http.StatusBadRequest, nil)
		return
	}

	body, err = json.Marshal(alerts)
	if err != nil {
		err = fmt.Errorf("failed to marshal POST body: %w", err)
		s.writeErrorResponse(c, err, http.StatusInternalServerError, nil)
		return
	}

	s.logger.Infof("BODY: %s", string(body))

	c.Status(http.StatusNoContent)
}

func (s *Server) validateChannelInfo(channel string, channelInfo *ChannelInfo) error {
	if !channelInfo.ChannelExists {
		return fmt.Errorf("unable to find channel %s in workspace", channel)
	}

	if channelInfo.ChannelIsArchived {
		return fmt.Errorf("channel %s is archived", channel)
	}

	if !channelInfo.ManagerIsInChannel {
		return fmt.Errorf("the Slack Manager integration is not in channel %s", channel)
	}

	if channelInfo.UserCount > s.cfg.MaxUsersInAlertChannel {
		return fmt.Errorf("the number of users (%d) in channel %s exceeds the limit (%d)", channelInfo.UserCount, channel, s.cfg.MaxUsersInAlertChannel)
	}

	return nil
}

// setSlackChannelID sets the SlackChannelID field on each alert.
//
// The channel ID is determined in the following order:
//  1. If the alert has a SlackChannelID in the body, use that.
//  2. If the URL contains a channel ID parameter, use that.
//  3. If the alert has a non-empty route key, use the mapping rules to find the channel ID.
//  4. If no route key is present, use the fallback mapping if it exists.
//
// If no channel ID can be determined for at least one of the alerts, an error is returned.
func (s *Server) setSlackChannelID(c *gin.Context, alerts ...*common.Alert) error {
	if len(alerts) == 0 {
		return nil
	}

	channelIDFromURLParam := ""

	// Try to get the channel ID from the URL (if any).
	if c != nil {
		channelIDFromURLParam = c.Param("slackChannelId")
	}

	for _, alert := range alerts {
		// If the channel ID in the body is empty, use the channel ID from the URL (which may also be empty).
		// If both are empty, the route key will be used to find a mapping below.
		if alert.SlackChannelID == "" {
			alert.SlackChannelID = channelIDFromURLParam
		}

		// The channel ID may actually be a channel name. If so, attempt to map the name to a channel ID.
		if alert.SlackChannelID != "" {
			alert.SlackChannelID = s.channelInfoProvider.MapChannelNameToIDIfNeeded(alert.SlackChannelID)
		}

		// Channel ID found in the alert body or in the URL -> move on (no need to process the route key).
		if alert.SlackChannelID != "" {
			continue
		}

		// Find an alert mapping rule matching the route key and alert type (if any).
		// If the route key is empty, the fallback mapping will be used, if it exists.
		if channel, ok := s.apiSettings.Match(alert.RouteKey, alert.Type, s.logger); ok {
			alert.SlackChannelID = channel
			continue
		}

		// The route key is empty and no fallback mapping exists -> return an error.
		if alert.RouteKey == "" {
			return errors.New("alert has no route key, and no fallback mapping exists")
		}

		// No mapping found for the route key and alert type -> return an error.
		return fmt.Errorf("no mapping exists for route key %s and alert type %s", alert.RouteKey, alert.Type)
	}

	return nil
}

func (s *Server) logAlerts(text, reason string, started time.Time, alerts ...*common.Alert) {
	d := fmt.Sprintf("%v", time.Since(started))

	for _, alert := range alerts {
		entry := s.logger.WithField("duration", d).WithField("correlation_id", alert.CorrelationID).WithField("channel_id", alert.SlackChannelID).WithField("header", alert.Header).WithField("fallback_text", alert.FallbackText)
		if reason != "" {
			entry = entry.WithField("reason", reason)
		}

		entry.Info(text)
	}
}

func ignoreAlert(alert *common.Alert) (bool, string) {
	if alert == nil || len(alert.IgnoreIfTextContains) == 0 || alert.Text == "" {
		return false, ""
	}

	for _, ignore := range alert.IgnoreIfTextContains {
		ignore = strings.TrimSpace(strings.ToLower(ignore))

		if ignore == "" {
			continue
		}

		if strings.Contains(strings.ToLower(alert.Text), ignore) {
			return true, "Ignore term in text field"
		}
	}

	return false, ""
}

func parseAlertInput(body []byte) ([]*common.Alert, error) {
	var alerts []*common.Alert

	// Scenario 1: the input is an array of alerts, i.e. the root level is an array
	if strings.HasPrefix(string(body), "[") {
		if err := json.Unmarshal(body, &alerts); err != nil {
			return nil, fmt.Errorf("failed to json unmarshal input: %w", err)
		}

		return alerts, nil
	}

	// Scenario 2: the root level is an object.
	// In this case, we may still have multiple alerts (see alertsInput struct for details)
	// Yes, this is a bit of a hack, but backwards compatibility...
	var input *alertsInput

	if err := json.Unmarshal(body, &input); err != nil {
		return nil, fmt.Errorf("failed to json unmarshal input: %w", err)
	}

	// If the input contains an array of alerts, return that.
	// Any other fields on the root level are ignored (we can't have it both ways).
	if len(input.Alerts) > 0 {
		return input.Alerts, nil
	}

	// Nothing in the alerts array -> assume the input is a single alert, with all fields at the root level.
	return []*common.Alert{&input.Alert}, nil
}
