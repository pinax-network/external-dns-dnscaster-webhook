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
