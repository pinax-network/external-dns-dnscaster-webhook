package metrics

import (
	"net/http"
	"time"
)

type MetricsMiddleware struct {
	metrics *Metrics
}

func NewMetricsMiddleware(m *Metrics) *MetricsMiddleware {
	return &MetricsMiddleware{metrics: m}
}

func (m *MetricsMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mw := newMetricsResponseWriter(w)
		m.metrics.HTTPRequestsInFlight.WithLabelValues(ProviderName).Inc()
		start := time.Now()
		defer func() {
			m.metrics.HTTPRequestsInFlight.WithLabelValues(ProviderName).Dec()
			m.metrics.ObserveWebhookRequest(r.Method, r.URL.Path, mw.Status(), time.Since(start), mw.BytesWritten())
		}()

		next.ServeHTTP(mw, r)
	})
}

type metricsResponseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func newMetricsResponseWriter(w http.ResponseWriter) *metricsResponseWriter {
	return &metricsResponseWriter{ResponseWriter: w, status: http.StatusOK}
}

func (w *metricsResponseWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *metricsResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func (w *metricsResponseWriter) Status() int {
	return w.status
}

func (w *metricsResponseWriter) BytesWritten() int {
	return w.size
}
