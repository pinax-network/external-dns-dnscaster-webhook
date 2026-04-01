package metrics

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace    = "externaldns_webhook"
	ProviderName = "dnscaster"
)

// Metrics holds all Prometheus metrics for the webhook.
type Metrics struct {
	// Info metric
	Info *prometheus.GaugeVec

	// HTTP metrics (webhook inbound)
	HTTPRequestsTotal      *prometheus.CounterVec
	HTTPRequestDuration    *prometheus.HistogramVec
	HTTPRequestsInFlight   *prometheus.GaugeVec
	HTTPResponseSizeBytes  *prometheus.HistogramVec
	HTTPResponsesByCode    *prometheus.CounterVec
	HTTP2XXResponses       *prometheus.CounterVec
	HTTP4XXResponses       *prometheus.CounterVec
	HTTP5XXResponses       *prometheus.CounterVec
	HTTPValidationErrors   *prometheus.CounterVec
	HTTPJSONErrors         *prometheus.CounterVec
	HTTPDNScasterAPIErrors *prometheus.CounterVec

	// DNS records / changes metrics
	RecordsTotal       *prometheus.GaugeVec
	ChangesTotal       *prometheus.CounterVec
	ChangesByTypeTotal *prometheus.CounterVec

	// Endpoint operations
	AdjustEndpointsTotal *prometheus.CounterVec
	NegotiateTotal       *prometheus.CounterVec

	// DNScaster API metrics (outbound)
	DNScasterAPIErrorsTotal      *prometheus.CounterVec
	DNScasterAPIDuration         *prometheus.HistogramVec
	DNScasterResponseSizeBytes   *prometheus.HistogramVec
	DNScasterHTTPResponsesByCode *prometheus.CounterVec
	DNScasterHTTP2XXResponses    *prometheus.CounterVec
	DNScasterHTTP4XXResponses    *prometheus.CounterVec
	DNScasterHTTP5XXResponses    *prometheus.CounterVec

	// Quality metrics
	ConsecutiveErrors    *prometheus.GaugeVec
	LastSuccessTimestamp *prometheus.GaugeVec
	OperationSuccessRate *prometheus.GaugeVec

	mu               sync.Mutex
	opSuccesses      map[string]float64
	opTotals         map[string]float64
	opConsecutiveErr map[string]float64
}

var (
	instance *Metrics
	once     sync.Once
)

// New creates and registers all metrics.
func New(version string) *Metrics {
	once.Do(func() {
		m := &Metrics{
			Info: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "info",
					Help:      "Build and provider information.",
				},
				[]string{"version", "provider"},
			),
			HTTPRequestsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_requests_total",
					Help:      "Total number of inbound HTTP requests.",
				},
				[]string{"provider", "method", "path", "status"},
			),
			HTTPRequestDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_request_duration_seconds",
					Help:      "Duration of inbound HTTP requests in seconds.",
					Buckets:   prometheus.DefBuckets,
				},
				[]string{"provider", "method", "path"},
			),
			HTTPRequestsInFlight: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_requests_in_flight",
					Help:      "Current number of in-flight inbound HTTP requests.",
				},
				[]string{"provider"},
			),
			HTTPResponseSizeBytes: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_response_size_bytes",
					Help:      "Size of inbound HTTP responses in bytes.",
					Buckets:   prometheus.ExponentialBuckets(128, 2, 10),
				},
				[]string{"provider", "method", "path", "status"},
			),
			HTTPResponsesByCode: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_responses_total",
					Help:      "Total number of inbound HTTP responses by status code class.",
				},
				[]string{"provider", "path", "code_class"},
			),
			HTTP2XXResponses: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_2xx_responses_total",
					Help:      "Total number of 2xx webhook responses.",
				},
				[]string{"provider", "path"},
			),
			HTTP4XXResponses: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_4xx_responses_total",
					Help:      "Total number of 4xx webhook responses.",
				},
				[]string{"provider", "path"},
			),
			HTTP5XXResponses: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_5xx_responses_total",
					Help:      "Total number of 5xx webhook responses.",
				},
				[]string{"provider", "path"},
			),
			HTTPValidationErrors: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_validation_errors_total",
					Help:      "Total number of webhook HTTP header validation errors.",
				},
				[]string{"provider", "path", "header_type"},
			),
			HTTPJSONErrors: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_json_errors_total",
					Help:      "Total number of webhook JSON encoding/decoding errors.",
				},
				[]string{"provider", "path"},
			),
			HTTPDNScasterAPIErrors: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "http_dnscaster_api_errors_total",
					Help:      "DNScaster API errors observed while handling inbound webhook routes.",
				},
				[]string{"provider", "path", "operation"},
			),
			RecordsTotal: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: namespace,
					Subsystem: "provider",
					Name:      "records_total",
					Help:      "Current number of DNS records returned by the provider.",
				},
				[]string{"provider"},
			),
			ChangesTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "provider",
					Name:      "changes_total",
					Help:      "Total number of DNS changes requested via ApplyChanges.",
				},
				[]string{"provider", "route"},
			),
			ChangesByTypeTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "provider",
					Name:      "changes_by_type_total",
					Help:      "Total number of DNS changes grouped by change type.",
				},
				[]string{"provider", "change_type"},
			),
			AdjustEndpointsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "adjust_endpoints_total",
					Help:      "Total number of AdjustEndpoints operations.",
				},
				[]string{"provider"},
			),
			NegotiateTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "webhook",
					Name:      "negotiate_total",
					Help:      "Total number of Negotiate operations.",
				},
				[]string{"provider"},
			),
			DNScasterAPIErrorsTotal: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "api_errors_total",
					Help:      "Total number of DNScaster API errors.",
				},
				[]string{"provider", "method", "path", "status"},
			),
			DNScasterAPIDuration: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "api_request_duration_seconds",
					Help:      "Duration of DNScaster API calls in seconds.",
					Buckets:   prometheus.DefBuckets,
				},
				[]string{"provider", "method", "path", "status"},
			),
			DNScasterResponseSizeBytes: prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "api_response_size_bytes",
					Help:      "Size of DNScaster API responses in bytes.",
					Buckets:   prometheus.ExponentialBuckets(128, 2, 10),
				},
				[]string{"provider", "method", "path", "status"},
			),
			DNScasterHTTPResponsesByCode: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "http_responses_total",
					Help:      "Total DNScaster API responses by status class.",
				},
				[]string{"provider", "path", "code_class"},
			),
			DNScasterHTTP2XXResponses: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "http_2xx_responses_total",
					Help:      "Total number of 2xx responses from DNScaster API.",
				},
				[]string{"provider", "path"},
			),
			DNScasterHTTP4XXResponses: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "http_4xx_responses_total",
					Help:      "Total number of 4xx responses from DNScaster API.",
				},
				[]string{"provider", "path"},
			),
			DNScasterHTTP5XXResponses: prometheus.NewCounterVec(
				prometheus.CounterOpts{
					Namespace: namespace,
					Subsystem: "dnscaster",
					Name:      "http_5xx_responses_total",
					Help:      "Total number of 5xx responses from DNScaster API.",
				},
				[]string{"provider", "path"},
			),
			ConsecutiveErrors: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: namespace,
					Subsystem: "quality",
					Name:      "consecutive_errors",
					Help:      "Current count of consecutive operation errors.",
				},
				[]string{"provider", "operation"},
			),
			LastSuccessTimestamp: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: namespace,
					Subsystem: "quality",
					Name:      "last_success_timestamp_seconds",
					Help:      "Unix timestamp of the last successful operation.",
				},
				[]string{"provider", "operation"},
			),
			OperationSuccessRate: prometheus.NewGaugeVec(
				prometheus.GaugeOpts{
					Namespace: namespace,
					Subsystem: "quality",
					Name:      "operation_success_rate",
					Help:      "Success rate of operations as success/total in range [0,1].",
				},
				[]string{"provider", "operation"},
			),
			opSuccesses:      map[string]float64{},
			opTotals:         map[string]float64{},
			opConsecutiveErr: map[string]float64{},
		}

		prometheus.MustRegister(
			m.Info,
			m.HTTPRequestsTotal,
			m.HTTPRequestDuration,
			m.HTTPRequestsInFlight,
			m.HTTPResponseSizeBytes,
			m.HTTPResponsesByCode,
			m.HTTP2XXResponses,
			m.HTTP4XXResponses,
			m.HTTP5XXResponses,
			m.HTTPValidationErrors,
			m.HTTPJSONErrors,
			m.HTTPDNScasterAPIErrors,
			m.RecordsTotal,
			m.ChangesTotal,
			m.ChangesByTypeTotal,
			m.AdjustEndpointsTotal,
			m.NegotiateTotal,
			m.DNScasterAPIErrorsTotal,
			m.DNScasterAPIDuration,
			m.DNScasterResponseSizeBytes,
			m.DNScasterHTTPResponsesByCode,
			m.DNScasterHTTP2XXResponses,
			m.DNScasterHTTP4XXResponses,
			m.DNScasterHTTP5XXResponses,
			m.ConsecutiveErrors,
			m.LastSuccessTimestamp,
			m.OperationSuccessRate,
		)

		m.Info.WithLabelValues(version, ProviderName).Set(1)
		instance = m
	})

	return instance
}

// Get returns the singleton metrics instance.
func Get() *Metrics {
	if instance == nil {
		return New("unknown")
	}

	return instance
}

func StatusClass(status int) string {
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500 && status < 600:
		return "5xx"
	default:
		return "other"
	}
}

func (m *Metrics) ObserveWebhookRequest(method, path string, status int, duration time.Duration, responseBytes int) {
	statusLabel := strconv.Itoa(status)
	m.HTTPRequestsTotal.WithLabelValues(ProviderName, method, path, statusLabel).Inc()
	m.HTTPRequestDuration.WithLabelValues(ProviderName, method, path).Observe(duration.Seconds())
	m.HTTPResponseSizeBytes.WithLabelValues(ProviderName, method, path, statusLabel).Observe(float64(responseBytes))
	m.HTTPResponsesByCode.WithLabelValues(ProviderName, path, StatusClass(status)).Inc()
	switch StatusClass(status) {
	case "2xx":
		m.HTTP2XXResponses.WithLabelValues(ProviderName, path).Inc()
	case "4xx":
		m.HTTP4XXResponses.WithLabelValues(ProviderName, path).Inc()
	case "5xx":
		m.HTTP5XXResponses.WithLabelValues(ProviderName, path).Inc()
	}
}

func (m *Metrics) ObserveDNScasterCall(method, path string, status int, duration time.Duration, responseBytes int) {
	statusLabel := strconv.Itoa(status)
	m.DNScasterAPIDuration.WithLabelValues(ProviderName, method, path, statusLabel).Observe(duration.Seconds())
	m.DNScasterResponseSizeBytes.WithLabelValues(ProviderName, method, path, statusLabel).Observe(float64(responseBytes))
	m.DNScasterHTTPResponsesByCode.WithLabelValues(ProviderName, path, StatusClass(status)).Inc()
	switch StatusClass(status) {
	case "2xx":
		m.DNScasterHTTP2XXResponses.WithLabelValues(ProviderName, path).Inc()
	case "4xx":
		m.DNScasterHTTP4XXResponses.WithLabelValues(ProviderName, path).Inc()
	case "5xx":
		m.DNScasterHTTP5XXResponses.WithLabelValues(ProviderName, path).Inc()
	}

	if status >= http.StatusBadRequest || status == 0 {
		m.DNScasterAPIErrorsTotal.WithLabelValues(ProviderName, method, path, statusLabel).Inc()
	}
}

func (m *Metrics) MarkOperation(operation string, success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.opTotals[operation]++
	if success {
		m.opSuccesses[operation]++
		m.opConsecutiveErr[operation] = 0
		m.LastSuccessTimestamp.WithLabelValues(ProviderName, operation).Set(float64(time.Now().Unix()))
	} else {
		m.opConsecutiveErr[operation]++
	}

	total := m.opTotals[operation]
	successes := m.opSuccesses[operation]
	rate := 0.0
	if total > 0 {
		rate = successes / total
	}

	m.ConsecutiveErrors.WithLabelValues(ProviderName, operation).Set(m.opConsecutiveErr[operation])
	m.OperationSuccessRate.WithLabelValues(ProviderName, operation).Set(rate)
}
