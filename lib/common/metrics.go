package common

import "time"

type Metrics interface {
	RegisterCounter(name, help string, labels ...string)
	AddToCounter(name string, value float64, labelValues ...string)
	AddHttpRequestMetric(path, method string, statusCode int, duration time.Duration)
}
