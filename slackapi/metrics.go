package slackapi

type Metrics interface {
	RegisterCounter(name, help string, labels ...string)
	AddToCounter(name string, value float64, labelValues ...string)
}
