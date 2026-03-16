package metrics

import "github.com/prometheus/client_golang/prometheus"

const (
	ProviderName = "dnscaster"
)

// Metrics holds all Prometheus metrics for the webhook.
type Metrics struct {
	// Info metric
	Info *prometheus.GaugeVec
}

var instance *Metrics

// New creates and registers all metrics.
func New(version string) *Metrics {
	if instance != nil {
		return instance
	}
	m := &Metrics{}

	// Set info metric
	m.Info.WithLabelValues(version, ProviderName).Set(1)

	instance = m

	return m
}

// Get returns the singleton metrics instance.
func Get() *Metrics {
	if instance == nil {
		return New("unknown")
	}

	return instance
}
