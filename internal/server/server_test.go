package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/webhook"
)

const webhookMediaType = "application/external.dns.webhook+json;version=1"

func init() {
	log.Init()
}

// slowProvider stands in for a DNScaster API that is slower than the request
// deadline. It only returns early once its context is cancelled.
type slowProvider struct {
	provider.BaseProvider
}

func (slowProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	return nil, waitForCancel(ctx)
}

func (slowProvider) ApplyChanges(ctx context.Context, _ *plan.Changes) error {
	return waitForCancel(ctx)
}

// waitForCancel blocks until ctx is cancelled. The fallback keeps a missing
// deadline from hanging the test: it reports success, which the test rejects.
func waitForCancel(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return nil
	}
}

func TestRequestDeadlineAnswersWithServerError(t *testing.T) {
	t.Parallel()

	config := configuration.Config{ServerWriteTimeout: 50 * time.Millisecond}
	router := newMainRouter(config, webhook.New(slowProvider{}))

	for name, newRequest := range map[string]func() *http.Request{
		"records": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/records", nil)
			r.Header.Set("Accept", webhookMediaType)
			return r
		},
		"apply changes": func() *http.Request {
			r := httptest.NewRequest(http.MethodPost, "/records", strings.NewReader("{}"))
			r.Header.Set("Content-Type", webhookMediaType)
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			router.ServeHTTP(w, newRequest())

			// external-dns retries a 5xx on its next sync, whereas a request
			// that outlives its client timeout makes it exit.
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500, got %d", w.Code)
			}
		})
	}
}

func TestRequestDeadlineDisabled(t *testing.T) {
	t.Parallel()

	for _, timeout := range []time.Duration{0, -time.Second} {
		hasDeadline := true
		h := requestDeadline(timeout)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, hasDeadline = r.Context().Deadline()
		}))

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/records", nil))
		if hasDeadline {
			t.Fatalf("timeout %v: expected no deadline on the request context", timeout)
		}
	}
}
