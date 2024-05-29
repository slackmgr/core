package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
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

func (s *Server) handleAlerts(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	if req.ContentLength <= 0 {
		err := errors.New("missing POST body")
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		err = fmt.Errorf("failed to read POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	s.debugLogRequest(req, body)

	alerts, err := parseAlertInput(body)
	if err != nil {
		err = fmt.Errorf("failed to parse POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	s.processAlerts(resp, req, alerts, started)
}

func (s *Server) processAlerts(resp http.ResponseWriter, req *http.Request, alerts []*common.Alert, started time.Time) {
	if len(alerts) == 0 {
		resp.WriteHeader(http.StatusNoContent)
		return
	}

	if err := s.setSlackChannelID(req, alerts...); err != nil {
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	alertsByChannel := make(map[string][]*common.Alert)
	atLeastOneAlert := false

	for _, alert := range alerts {
		alert.Clean()

		if err := alert.Validate(); err != nil {
			err = fmt.Errorf("input validation failed: %w", err)
			s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, getClientErrorDebugText(alert), getAlertChannelWithRouteKey(alert), resp, req, started)
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
		resp.WriteHeader(http.StatusNoContent)
		return
	}

	alertLimitPerChannel := s.cfg.RateLimit.AllowedBurst

	for channel, channelAlerts := range alertsByChannel {
		channelInfo, err := s.channelInfoSyncer.GetChannelInfo(req.Context(), channel)
		if err != nil {
			err = fmt.Errorf("failed to fetch info for channel %s: %w", channel, err)
			s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		if !channelInfo.ChannelExists {
			err := fmt.Errorf("unable to find channel %s in workspace", channel)
			s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		if channelInfo.ChannelIsArchived {
			err := fmt.Errorf("channel %s is archived", channel)
			s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		if !channelInfo.ManagerIsInChannel {
			err := fmt.Errorf("the Slack Manager integration is not in channel %s", channel)
			s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		if channelInfo.UserCount > s.cfg.MaxUsersInAlertChannel {
			err := fmt.Errorf("the number of users (%d) in channel %s exceeds the limit (%d)", channelInfo.UserCount, channel, s.cfg.MaxUsersInAlertChannel)
			s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		keptAlerts, skippedAlerts := reduceAlertCountForChannel(channel, channelAlerts, alertLimitPerChannel)

		if len(skippedAlerts) > 0 {
			channelAlerts = keptAlerts
			s.logAlerts("Alert dropped", "Too many alerts in request", started, skippedAlerts...)
		}

		failOnRateLimitError := channelAlerts[0].FailOnRateLimitError
		alertCount := len(channelAlerts)

		permitCount, err := s.waitForRateLimit(req.Context(), channel, alertCount, failOnRateLimitError)
		if err != nil {
			s.writeErrorResponse(req.Context(), err, http.StatusTooManyRequests, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		if permitCount < alertCount {
			keptAlerts := channelAlerts[0:permitCount]
			skippedAlerts := channelAlerts[permitCount:]
			overflow := alertCount - permitCount
			firstVictim := channelAlerts[permitCount]
			rateLimitAlert := createRateLimitAlert(channel, overflow, firstVictim)
			keptAlerts = append(keptAlerts, rateLimitAlert)

			s.logAlerts("Alert dropped", "Rate limiting", started, skippedAlerts...)

			channelAlerts = keptAlerts
		}

		for _, alert := range channelAlerts {
			for _, w := range alert.Webhooks {
				if err := internal.EncryptWebhookPayload(w, []byte(s.cfg.EncryptionKey)); err != nil {
					err = fmt.Errorf("failed to encrypt webhook payload: %w", err)
					s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, getClientErrorDebugText(alert), getAlertChannelWithRouteKey(alert), resp, req, started)
					return
				}
			}

			if err := s.queueAlert(req.Context(), alert); err != nil {
				s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, getClientErrorDebugText(alert), getAlertChannelWithRouteKey(alert), resp, req, started)
				return
			}

			s.logAlerts("Alert accepted", "", started, alert)
		}
	}

	resp.WriteHeader(http.StatusNoContent)

	s.metrics.AddHTTPRequestMetric(req.URL.Path, req.Method, http.StatusNoContent, time.Since(started))
}

func (s *Server) handleAlertsTest(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	if req.ContentLength <= 0 {
		err := errors.New("missing POST body")
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		err = fmt.Errorf("failed to read POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	alerts, err := parseAlertInput(body)
	if err != nil {
		err = fmt.Errorf("failed to parse POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	if err := s.setSlackChannelID(req, alerts...); err != nil {
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	body, err = json.Marshal(alerts)
	if err != nil {
		err = fmt.Errorf("failed to marshal POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	s.logger.Infof("BODY: %s", string(body))

	resp.WriteHeader(http.StatusNoContent)
}

func (s *Server) createClientErrorAlert(err error, statusCode int, debugText map[string]string, targetChannel string) *common.Alert {
	severity := common.AlertWarning

	if statusCode >= 500 {
		severity = common.AlertError
	}

	if targetChannel == "" {
		targetChannel = NA
	}

	alert := common.NewAlert(severity)

	alert.CorrelationID = fmt.Sprintf("__client_error_%s_%s", targetChannel, internal.Hash(err.Error()))
	alert.Header = fmt.Sprintf(":status: Client error %d", statusCode)
	alert.FallbackText = fmt.Sprintf("Client error %d", statusCode)
	alert.SlackChannelID = s.cfg.ErrorReportChannelID
	alert.IssueFollowUpEnabled = true
	alert.AutoResolveSeconds = 3600
	alert.ArchivingDelaySeconds = 24 * 3600

	alert.Text = fmt.Sprintf("*Target*: `%s`\n*Error*: `%s`", targetChannel, err.Error())

	for k, v := range debugText {
		v = strings.ReplaceAll(v, "`", "")
		v = strings.ReplaceAll(v, "*", "")
		v = strings.ReplaceAll(v, "~", "")
		v = strings.ReplaceAll(v, "_", "")
		v = strings.ReplaceAll(v, ":status:", "")
		v = strings.ReplaceAll(v, "<", "")
		v = strings.ReplaceAll(v, ">", "")
		v = strings.ReplaceAll(v, "\n", " ")
		v = strings.TrimSpace(v)

		if len(v) > 100 {
			v = v[:97] + "..."
		}

		alert.Text += fmt.Sprintf("\n*%s*: `%s`", k, v)
	}

	return alert
}

func (s *Server) setSlackChannelID(req *http.Request, alerts ...*common.Alert) error {
	if len(alerts) == 0 {
		return nil
	}

	var channelIDFromURL string
	var getChannelIDFromURL sync.Once

	for _, alert := range alerts {
		// The channel ID may actually be a channel name. If so, attempt to map to channel ID.
		alert.SlackChannelID = s.channelInfoSyncer.MapChannelNameToIDIfNeeded(alert.SlackChannelID)

		// Channel found in the alert body -> move on
		if alert.SlackChannelID != "" {
			continue
		}

		// Try to get the channel ID from the URL, exactly once
		getChannelIDFromURL.Do(func() {
			vars := mux.Vars(req)
			if vars != nil {
				if val, ok := vars["slackChannelId"]; ok {
					channelIDFromURL = strings.TrimSpace(val)
				}
			}
		})

		// Channel found in the url, no need to check the route key
		if channelIDFromURL != "" {
			alert.SlackChannelID = channelIDFromURL
			continue
		}

		// Find an alert mapping rule matching the route key and alert type (if any)
		if channel, ok := s.apiSettings.Match(alert.RouteKey, alert.Type, s.logger); ok {
			alert.SlackChannelID = channel
		} else {
			if alert.RouteKey == "" {
				return fmt.Errorf("alert has no route key, and no fallback mapping exists")
			}
			return fmt.Errorf("no mapping exists for route key %s and alert type %s", alert.RouteKey, alert.Type)
		}
	}

	return nil
}

func (s *Server) logAlerts(text, reason string, started time.Time, alerts ...*common.Alert) {
	d := fmt.Sprintf("%v", time.Since(started))

	for _, alert := range alerts {
		entry := s.logger.WithField("duration", d).WithField("correlation_id", alert.CorrelationID).WithField("slack_channel_id", alert.SlackChannelID).WithField("header", alert.Header).WithField("fallback_text", alert.FallbackText)
		if reason != "" {
			entry = entry.WithField("reason", reason)
		}
		entry.Info(text)
	}
}

func getClientErrorDebugText(alert *common.Alert) map[string]string {
	if alert == nil {
		return nil
	}

	return map[string]string{
		"CorrelationId": alert.CorrelationID,
		"Header":        alert.Header,
		"Body":          alert.Text,
	}
}

func reduceAlertCountForChannel(channel string, alerts []*common.Alert, limit int) ([]*common.Alert, []*common.Alert) {
	if len(alerts) <= limit {
		return alerts, []*common.Alert{}
	}

	reducedAlerts := alerts[0:limit]
	overflow := len(alerts) - len(reducedAlerts)
	firstVictim := alerts[limit]
	rateLimitAlert := createRateLimitAlert(channel, overflow, firstVictim)
	reducedAlerts = append(reducedAlerts, rateLimitAlert)

	return reducedAlerts, alerts[limit:]
}

func createRateLimitAlert(channel string, overflow int, template *common.Alert) *common.Alert {
	summary := common.NewPanicAlert()
	summary.CorrelationID = fmt.Sprintf("__rate_limit_%s", channel)
	summary.Header = ":status: Too many alerts"
	summary.Text = fmt.Sprintf("%d alerts were dropped", overflow)
	summary.FallbackText = "Too many alerts"
	summary.IconEmoji = template.IconEmoji
	summary.Username = template.Username
	summary.SlackChannelID = channel

	if template.IssueFollowUpEnabled {
		summary.IssueFollowUpEnabled = true
		summary.AutoResolveSeconds = 3600
	}

	return summary
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

func getAlertChannelWithRouteKey(alert *common.Alert) string {
	var result string

	if alert.SlackChannelID != "" {
		result = alert.SlackChannelID
	} else {
		result = NA
	}

	if alert.RouteKey != "" {
		result += fmt.Sprintf(" [%s]", alert.RouteKey)
	}

	return result
}
