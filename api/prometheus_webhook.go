package api

import "time"

type PrometheusWebhook struct {
	Version           string             `json:"version,omitempty"`
	GroupKey          string             `json:"groupKey,omitempty"`
	TruncatedAlerts   int                `json:"truncatedAlerts,omitempty"`
	Status            string             `json:"status,omitempty"`
	Receiver          string             `json:"receiver,omitempty"`
	GroupLabels       map[string]string  `json:"groupLabels,omitempty"`
	CommonLabels      map[string]string  `json:"commonLabels,omitempty"`
	CommonAnnotations map[string]string  `json:"commonAnnotations,omitempty"`
	ExternalURL       string             `json:"externalURL,omitempty"` //nolint:tagliatelle
	Alerts            []*PrometheusAlert `json:"alerts,omitempty"`
}

type PrometheusAlert struct {
	Status       string            `json:"status,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	StartsAt     time.Time         `json:"startsAt,omitempty"`
	EndsAt       time.Time         `json:"endsAt,omitempty"`
	GeneratorURL string            `json:"generatorURL,omitempty"` //nolint:tagliatelle
	Fingerprint  string            `json:"fingerprint,omitempty"`
}
