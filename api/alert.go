package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/peteraglen/slack-manager/client"
)

const NA = "N/A"

func (s *Server) alert(resp http.ResponseWriter, req *http.Request) {
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

	var alert client.Alert

	if err := json.Unmarshal(body, &alert); err != nil {
		err = fmt.Errorf("failed to decode POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	alerts := []*client.Alert{&alert}

	s.handleAlerts(resp, req, alerts, started)
}

func (s *Server) alerts(resp http.ResponseWriter, req *http.Request) {
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

	var input *client.Alerts

	if err := json.Unmarshal(body, &input); err != nil {
		err = fmt.Errorf("failed to decode POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	s.handleAlerts(resp, req, input.Alerts, started)
}

func (s *Server) handleAlerts(resp http.ResponseWriter, req *http.Request, alerts []*client.Alert, started time.Time) {
	if len(alerts) == 0 {
		err := fmt.Errorf("alert list is empty")
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	s.setSlackChannelID(req, alerts...)

	alertsByChannel := make(map[string][]*client.Alert)
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
			alertsByChannel[channel] = []*client.Alert{alert}
		}
	}

	if !atLeastOneAlert {
		resp.WriteHeader(http.StatusNoContent)
		return
	}

	alertLimitPerChannel := s.config.RateLimit.AllowedBurst

	for channel, channelAlerts := range alertsByChannel {
		channelInfo, err := s.channelInfoManager.GetChannelInfo(req.Context(), channel)
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
			err := fmt.Errorf("SUDO Slack Manager is not in channel %s", channel)
			s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, getClientErrorDebugText(channelAlerts[0]), getAlertChannelWithRouteKey(channelAlerts[0]), resp, req, started)
			return
		}

		if channelInfo.UserCount > s.config.Slack.MaxChannelUserCount {
			err := fmt.Errorf("the number of users (%d) in channel %s exceeds the limit (%d)", channelInfo.UserCount, channel, s.config.Slack.MaxChannelUserCount)
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
				if err := w.EncryptPayload([]byte(s.config.EncryptionKey)); err != nil {
					err = fmt.Errorf("failed to encrypt webhook: %w", err)
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

	// metrics.AddHttpRequestMetric(req.URL.Path, req.Method, http.StatusNoContent, time.Since(started))
}

func (s *Server) testSlackAlertsHandler(resp http.ResponseWriter, req *http.Request) {
	started := time.Now()

	if req.ContentLength <= 0 {
		err := errors.New("missing POST body")
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	var input *client.Alerts

	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		err = fmt.Errorf("failed to decode POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	s.setSlackChannelID(req, input.Alerts...)

	body, err := json.Marshal(input)
	if err != nil {
		err = fmt.Errorf("failed to marshal POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusInternalServerError, nil, "", resp, req, started)
		return
	}

	s.logger.Infof("BODY: %s", string(body))

	resp.WriteHeader(http.StatusNoContent)
}

func getClientErrorDebugText(alert *client.Alert) map[string]string {
	if alert == nil {
		return nil
	}

	return map[string]string{
		"CorrelationId": alert.CorrelationID,
		"Header":        alert.Header,
		"Body":          alert.Text,
	}
}

func reduceAlertCountForChannel(channel string, alerts []*client.Alert, limit int) ([]*client.Alert, []*client.Alert) {
	if len(alerts) <= limit {
		return alerts, []*client.Alert{}
	}

	reducedAlerts := alerts[0:limit]
	overflow := len(alerts) - len(reducedAlerts)
	firstVictim := alerts[limit]
	rateLimitAlert := createRateLimitAlert(channel, overflow, firstVictim)
	reducedAlerts = append(reducedAlerts, rateLimitAlert)

	return reducedAlerts, alerts[limit:]
}

func createRateLimitAlert(channel string, overflow int, template *client.Alert) *client.Alert {
	summary := client.NewPanicAlert()
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

func (s *Server) createClientErrorAlert(err error, statusCode int, debugText map[string]string, targetChannel string) *client.Alert {
	severity := client.AlertWarning

	if statusCode >= 500 {
		severity = client.AlertError
	}

	if targetChannel == "" {
		targetChannel = NA
	}

	alert := client.NewAlert(severity)

	alert.CorrelationID = fmt.Sprintf("__client_error_%s_%s", targetChannel, hash(err.Error()))
	alert.Header = fmt.Sprintf(":status: Client error %d", statusCode)
	alert.FallbackText = fmt.Sprintf("Client error %d", statusCode)
	alert.SlackChannelID = s.config.ErrorReportChannelID
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

func (s *Server) setSlackChannelID(req *http.Request, alerts ...*client.Alert) {
	vars := mux.Vars(req)

	if vars == nil {
		return
	}

	channelIDFromURL, foundInURL := vars["slackChannelId"]

	alertMappings := s.config.GetAlertMappings()

	for _, alert := range alerts {
		// The channel ID may actually be a channel name. If so, attempt to map to channel ID.
		alert.SlackChannelID = s.channelInfoManager.MapChannelNameToIDIfNeeded(alert.SlackChannelID)

		// Channel found in the alert body -> move on
		if alert.SlackChannelID != "" {
			continue
		}

		// Channel found in the url, no need to check the route key
		if foundInURL {
			alert.SlackChannelID = channelIDFromURL
			continue
		}

		// Find an alert mapping rule matching the route key (if any)
		if channel, found := s.findChannelForRouteKey(req.Context(), alertMappings, alert.RouteKey); found {
			alert.SlackChannelID = channel
		}
	}
}

func (s *Server) findChannelForRouteKey(ctx context.Context, alertMappings *RouteMapping, routeKey string) (string, bool) {
	cacheKey := "slack-manager::findChannelForRouteKey::" + routeKey

	if val, found := s.cache.Get(ctx, cacheKey); found {
		if val == "N/A" {
			return "", false
		}
		return val, true
	}

	channel, found := alertMappings.Match(routeKey)

	if found {
		s.cache.Set(ctx, cacheKey, channel, 30*time.Second)
		return channel, true
	}

	s.cache.Set(ctx, cacheKey, "N/A", 30*time.Second)
	return "", false
}

func ignoreAlert(alert *client.Alert) (bool, string) {
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

func (s *Server) logAlerts(text, reason string, started time.Time, alerts ...*client.Alert) {
	d := fmt.Sprintf("%v", time.Since(started))

	for _, alert := range alerts {
		entry := s.logger.WithField("duration", d).WithField("correlation_id", alert.CorrelationID).WithField("slack_channel_id", alert.SlackChannelID).WithField("header", alert.Header).WithField("fallback_text", alert.FallbackText)
		if reason != "" {
			entry = entry.WithField("reason", reason)
		}
		entry.Info(text)
	}
}

func getAlertChannelWithRouteKey(alert *client.Alert) string {
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
