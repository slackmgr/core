package internal

import "github.com/slackmgr/types"

// prefixedMetrics wraps a types.Metrics implementation and prepends a fixed
// string to every metric name before forwarding calls to the inner instance.
// This allows the library to use short, unprefixed metric names internally
// while ensuring all published metrics carry a consistent namespace prefix.
type prefixedMetrics struct {
	inner  types.Metrics
	prefix string
}

// NewPrefixedMetrics returns a types.Metrics that prepends prefix to every
// metric name. If prefix is empty the inner instance is returned unchanged.
//
//nolint:ireturn // Must return the interface: when prefix is empty we return the caller's own implementation.
func NewPrefixedMetrics(inner types.Metrics, prefix string) types.Metrics {
	if prefix == "" {
		return inner
	}

	return &prefixedMetrics{inner: inner, prefix: prefix}
}

func (p *prefixedMetrics) RegisterCounter(name, help string, labels ...string) {
	p.inner.RegisterCounter(p.prefix+name, help, labels...)
}

func (p *prefixedMetrics) RegisterGauge(name, help string, labels ...string) {
	p.inner.RegisterGauge(p.prefix+name, help, labels...)
}

func (p *prefixedMetrics) RegisterHistogram(name, help string, buckets []float64, labels ...string) {
	p.inner.RegisterHistogram(p.prefix+name, help, buckets, labels...)
}

func (p *prefixedMetrics) Add(name string, value float64, labelValues ...string) {
	p.inner.Add(p.prefix+name, value, labelValues...)
}

func (p *prefixedMetrics) Inc(name string, labelValues ...string) {
	p.inner.Inc(p.prefix+name, labelValues...)
}

func (p *prefixedMetrics) Set(name string, value float64, labelValues ...string) {
	p.inner.Set(p.prefix+name, value, labelValues...)
}

func (p *prefixedMetrics) Observe(name string, value float64, labelValues ...string) {
	p.inner.Observe(p.prefix+name, value, labelValues...)
}
