package webhook

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func init() {
	log.Init()
}

type fakeProvider struct {
	records      []*endpoint.Endpoint
	recordsErr   error
	applyErr     error
	domainFilter endpoint.DomainFilterInterface
	applySeen    *plan.Changes
}

func (f *fakeProvider) Records(context.Context) ([]*endpoint.Endpoint, error) {
	return f.records, f.recordsErr
}

func (f *fakeProvider) ApplyChanges(_ context.Context, c *plan.Changes) error {
	f.applySeen = c
	return f.applyErr
}

func (f *fakeProvider) AdjustEndpoints(e []*endpoint.Endpoint) ([]*endpoint.Endpoint, error) {
	return e, nil
}

func (f *fakeProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	if f.domainFilter != nil {
		return f.domainFilter
	}
	return endpoint.NewDomainFilter(nil)
}

func TestRecordsRequiresAcceptHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/records", nil)

	New(&fakeProvider{}).Records(w, r)
	if w.Code != http.StatusNotAcceptable {
		t.Fatalf("expected 406, got %d", w.Code)
	}
}

func TestRecordsReturnsProviderData(t *testing.T) {
	t.Parallel()

	p := &fakeProvider{records: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/records", nil)
	r.Header.Set("Accept", string(mediaTypeVersion1))

	New(p).Records(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get(contentTypeHeader); ct != string(mediaTypeVersion1) {
		t.Fatalf("unexpected content type: %s", ct)
	}
}

func TestApplyChangesRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/records", bytes.NewBufferString("{"))
	r.Header.Set(contentTypeHeader, string(mediaTypeVersion1))

	New(&fakeProvider{}).ApplyChanges(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestApplyChangesCallsProvider(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/records", bytes.NewBufferString(`{"Create":[{"dnsName":"a.example.com","targets":["1.2.3.4"],"recordType":"A"}]}`))
	r.Header.Set(contentTypeHeader, string(mediaTypeVersion1))

	New(provider).ApplyChanges(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Code)
	}
	if provider.applySeen == nil || len(provider.applySeen.Create) != 1 {
		t.Fatalf("expected provider ApplyChanges to be called with create records")
	}
}

func TestApplyChangesProviderError(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{applyErr: errors.New("boom")}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/records", bytes.NewBufferString(`{"Create":[]}`))
	r.Header.Set(contentTypeHeader, string(mediaTypeVersion1))

	New(provider).ApplyChanges(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestMetricsMiddlewareObservesRequest(t *testing.T) {
	m := metrics.New("test")
	middleware := metrics.NewMetricsMiddleware(m)

	h := middleware.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	beforeRequests := testutil.ToFloat64(m.HTTPRequestsTotal.WithLabelValues(metrics.ProviderName, http.MethodGet, "/records", "204"))
	before2xx := testutil.ToFloat64(m.HTTP2XXResponses.WithLabelValues(metrics.ProviderName, "/records"))
	beforeInFlight := testutil.ToFloat64(m.HTTPRequestsInFlight.WithLabelValues(metrics.ProviderName))

	req := httptest.NewRequest(http.MethodGet, "/records", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	afterRequests := testutil.ToFloat64(m.HTTPRequestsTotal.WithLabelValues(metrics.ProviderName, http.MethodGet, "/records", "204"))
	after2xx := testutil.ToFloat64(m.HTTP2XXResponses.WithLabelValues(metrics.ProviderName, "/records"))
	afterInFlight := testutil.ToFloat64(m.HTTPRequestsInFlight.WithLabelValues(metrics.ProviderName))

	if afterRequests != beforeRequests+1 {
		t.Fatalf("expected http request total to increment by 1, got before=%v after=%v", beforeRequests, afterRequests)
	}
	if after2xx != before2xx+1 {
		t.Fatalf("expected 2xx response total to increment by 1, got before=%v after=%v", before2xx, after2xx)
	}
	if beforeInFlight != 0 || afterInFlight != 0 {
		t.Fatalf("expected in-flight gauge to be 0 before and after request, got before=%v after=%v", beforeInFlight, afterInFlight)
	}
}

func TestWebhookMetricsValidationAndJSONErrors(t *testing.T) {
	m := metrics.New("test")

	wh := New(&fakeProvider{})

	beforeValidation := testutil.ToFloat64(m.HTTPValidationErrors.WithLabelValues(metrics.ProviderName, "/records", "accept"))
	beforeJSON := testutil.ToFloat64(m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, "/records"))

	r1 := httptest.NewRequest(http.MethodGet, "/records", nil)
	w1 := httptest.NewRecorder()
	wh.Records(w1, r1)

	r2 := httptest.NewRequest(http.MethodPost, "/records", bytes.NewBufferString("{"))
	r2.Header.Set(contentTypeHeader, string(mediaTypeVersion1))
	w2 := httptest.NewRecorder()
	wh.ApplyChanges(w2, r2)

	afterValidation := testutil.ToFloat64(m.HTTPValidationErrors.WithLabelValues(metrics.ProviderName, "/records", "accept"))
	afterJSON := testutil.ToFloat64(m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, "/records"))

	if afterValidation != beforeValidation+1 {
		t.Fatalf("expected validation errors to increment by 1, got before=%v after=%v", beforeValidation, afterValidation)
	}
	if afterJSON != beforeJSON+1 {
		t.Fatalf("expected json errors to increment by 1, got before=%v after=%v", beforeJSON, afterJSON)
	}
}
