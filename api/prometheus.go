package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/peteraglen/slack-manager/client"
	"github.com/peteraglen/slack-manager/common"
)

const True = "true"

func (s *Server) prometheusAlert(resp http.ResponseWriter, req *http.Request) {
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

	var webhook client.PrometheusWebhook

	if err := json.Unmarshal(body, &webhook); err != nil {
		err = fmt.Errorf("failed to decode POST body: %w", err)
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	if len(webhook.Alerts) == 0 {
		err = fmt.Errorf("prometheus alert list is empty")
		s.writeErrorResponse(req.Context(), err, http.StatusBadRequest, nil, "", resp, req, started)
		return
	}

	alerts := s.mapPrometheusAlert(&webhook)

	s.handleAlerts(resp, req, alerts, started)
}

func (s *Server) mapPrometheusAlert(webhook *client.PrometheusWebhook) []*client.Alert {
	alerts := []*client.Alert{}

	for _, promAlert := range webhook.Alerts {
		// Ensure that all annotation and label keys exist in lower-case versions
		createLowerCaseKeys(promAlert.Annotations)
		createLowerCaseKeys(promAlert.Labels)

		// Map from lower-case keys only, to ensure maximum compatibility
		// Annotation keys take precedence over label keys
		correlationID := find(promAlert.Annotations, promAlert.Labels, "correlationid", "correlation_id")
		header := valueOrDefault(find(promAlert.Annotations, promAlert.Labels, "header", "title", "summary"), "Prometheus alert")
		text := find(promAlert.Annotations, promAlert.Labels, "text", "description", "body")
		fallbackText := find(promAlert.Annotations, promAlert.Labels, "fallbacktext")
		author := find(promAlert.Annotations, promAlert.Labels, "author")
		host := find(promAlert.Annotations, promAlert.Labels, "host")
		footer := find(promAlert.Annotations, promAlert.Labels, "footer")
		link := find(promAlert.Annotations, promAlert.Labels, "link")
		autoResolve := find(promAlert.Annotations, promAlert.Labels, "autoresolveseconds")
		autoResolveAsInconclusive := valueOrDefault(strings.ToLower(find(promAlert.Annotations, promAlert.Labels, "autoresolveasinconclusive")), "true")
		severityString := strings.ToLower(find(promAlert.Annotations, promAlert.Labels, "severity", "level"))
		channel := find(promAlert.Annotations, promAlert.Labels, "slackchannelid", "channel", "slackchannel", "channelid", "slack_channel_id", "slack_channel", "channel_id")
		routeKey := find(promAlert.Annotations, promAlert.Labels, "routekey", "route", "route_key")
		issueFollowUpEnabled := valueOrDefault(strings.ToLower(find(promAlert.Annotations, promAlert.Labels, "issuefollowupenabled")), "true")
		username := find(promAlert.Annotations, promAlert.Labels, "username")
		icon := find(promAlert.Annotations, promAlert.Labels, "iconemoji", "icon")
		notificationDelay := find(promAlert.Annotations, promAlert.Labels, "notificationdelayseconds")
		archivingDelay := find(promAlert.Annotations, promAlert.Labels, "archivingdelayseconds", "deletiondelayseconds")
		ignoreIfTextContains := find(promAlert.Annotations, promAlert.Labels, "ignoreiftextcontains")
		failOnRateLimitError := find(promAlert.Annotations, promAlert.Labels, "failonratelimiterror")

		if fallbackText == "" {
			fallbackText = header
		}

		if severityString == "critical" || severityString == "" {
			severityString = "error"
		}

		severity := client.AlertSeverity(severityString)

		if !client.SeverityIsValid(severity) {
			s.logger.Infof("Invalid severity '%s' in Prometheus alert", severity)
			severity = client.AlertError
		}

		if promAlert.Status == "resolved" {
			severity = client.AlertResolved
		}

		autoResolveSeconds, err := strconv.Atoi(autoResolve)
		if err != nil || autoResolveSeconds < 30 {
			autoResolveSeconds = 3600 // 1 hour
		}

		notificationDelaySeconds, err := strconv.Atoi(notificationDelay)
		if err != nil || notificationDelaySeconds < 0 || notificationDelaySeconds > autoResolveSeconds {
			notificationDelaySeconds = 0
		}

		archivingDelaySeconds, err := strconv.Atoi(archivingDelay)
		if err != nil || archivingDelaySeconds < 0 {
			archivingDelaySeconds = 6 * 3600
		}

		if !strings.Contains(header, ":status:") && !strings.Contains(text, ":status:") {
			header = fmt.Sprintf(":status: %s", header)
		}

		// If no correlation ID is specified, generate one based on a specific set of labels
		if correlationID == "" {
			correlationID = correlationIDFromLabels(promAlert.Labels)
		}

		// If no correlation ID is specified AND no labels were found to generate a correlation ID, generate one based on the group key and the alert start time
		if correlationID == "" {
			correlationID = fmt.Sprintf("%s-%s", common.Hash(webhook.GroupKey), promAlert.StartsAt.Format(time.RFC3339Nano))
		}

		// Add some metadata to the alert, for debug purposes only
		metadata := map[string]interface{}{
			"status":      promAlert.Status,
			"labels":      promAlert.Labels,
			"annotations": promAlert.Annotations,
			"startsAt":    promAlert.StartsAt,
			"endsAt":      promAlert.EndsAt,
			"groupKey":    webhook.GroupKey,
		}

		a := client.Alert{
			Timestamp:                 time.Now().UTC(),
			CorrelationID:             correlationID,
			Header:                    header,
			Text:                      text,
			FallbackText:              fallbackText,
			Host:                      host,
			Author:                    author,
			Footer:                    footer,
			Link:                      link,
			IssueFollowUpEnabled:      issueFollowUpEnabled == True,
			AutoResolveSeconds:        autoResolveSeconds,
			AutoResolveAsInconclusive: autoResolveAsInconclusive == True,
			SlackChannelID:            channel,
			RouteKey:                  routeKey,
			Severity:                  severity,
			Username:                  username,
			IconEmoji:                 icon,
			NotificationDelaySeconds:  notificationDelaySeconds,
			ArchivingDelaySeconds:     archivingDelaySeconds,
			FailOnRateLimitError:      failOnRateLimitError == True,
			Metadata:                  metadata,
		}

		if ignoreIfTextContains != "" {
			a.IgnoreIfTextContains = []string{ignoreIfTextContains}
		}

		alerts = append(alerts, &a)
	}

	return alerts
}

func correlationIDFromLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	values := []string{}

	namespace, ok := labels["namespace"]
	if ok {
		values = append(values, "namespace::"+namespace)
	}

	if v, ok := labels["alertname"]; ok {
		values = append(values, "alertname::"+v)
	}

	if v, ok := labels["job"]; ok {
		values = append(values, "job::"+v)
	}

	if v, ok := labels["service"]; ok {
		values = append(values, "service::"+v)
	}

	if v, ok := labels["destination_service_name"]; ok {
		values = append(values, "destination_service_name::"+v)
	}

	if namespace == "monitoring" {
		if v, ok := labels["instance"]; ok {
			values = append(values, "instance::"+v)
		}
	}

	if len(values) == 0 {
		return ""
	}

	return common.Hash(values...)
}

func find(map1, map2 map[string]string, keys ...string) string {
	for _, key := range keys {
		if map1 != nil {
			if val, ok := map1[key]; ok {
				return strings.TrimSpace(val)
			}
		}

		if map2 != nil {
			if val, ok := map2[key]; ok {
				return strings.TrimSpace(val)
			}
		}
	}

	return ""
}

func valueOrDefault(value, defaultValue string) string {
	if value != "" {
		return value
	}
	return defaultValue
}

func createLowerCaseKeys(m map[string]string) {
	if m == nil {
		return
	}

	for key, value := range m {
		lowerKey := strings.ToLower(key)

		if lowerKey != key {
			m[lowerKey] = value
		}
	}
}
