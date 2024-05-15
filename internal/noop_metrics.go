package internal

import "time"

type NoopMetrics struct{}

func (m *NoopMetrics) RegisterCounter(name, help string, labels ...string) {
}

func (m *NoopMetrics) AddToCounter(name string, value float64, labelValues ...string) {
}

func (m *NoopMetrics) AddHttpRequestMetric(path, method string, statusCode int, duration time.Duration) {
}
