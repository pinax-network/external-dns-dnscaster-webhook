package dnscaster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func testClient(rt http.RoundTripper) *DNScasterApiClient {
	return &DNScasterApiClient{
		DNScasterDefaults:         &DNScasterDefaults{DefaultTTL: 300},
		DNScasterConnectionConfig: &DNScasterConnectionConfig{ApiKey: "test-api-key"},
		Client:                    &http.Client{Transport: rt},
	}
}

func TestDoBuildsRequestAndDecodesJSON(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		if req.URL.Host != dnscasterBaseUrl {
			t.Fatalf("unexpected host: %s", req.URL.Host)
		}
		if req.URL.Path != "/"+dnscasterZonePath {
			t.Fatalf("unexpected path: %s", req.URL.Path)
		}
		if req.Header.Get("Accept") != "application/json" {
			t.Fatalf("missing Accept header")
		}
		if req.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Fatalf("missing auth header")
		}
		if got := req.URL.Query().Get("limit"); got != "10" {
			t.Fatalf("unexpected query param limit: %s", got)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"collection":[{"id":"z-1","domain":"example.com"}]}`)),
			Header:     make(http.Header),
		}, nil
	}))

	var out ListResponse[Zone]
	err := client.do(context.Background(), http.MethodGet, dnscasterZonePath, url.Values{"limit": {"10"}}, nil, &out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Collection) != 1 || out.Collection[0].Domain != "example.com" {
		t.Fatalf("unexpected response body decode: %+v", out)
	}
}

func TestDoSetsContentTypeForRequestBody(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected Content-Type to be application/json")
		}
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), `"zone_id":"z-1"`) {
			t.Fatalf("unexpected request body: %s", body)
		}

		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"h-1"}`)),
			Header:     make(http.Header),
		}, nil
	}))

	err := client.do(context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{Host: Host{ZoneID: "z-1"}}, &Host{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoReturnsNetworkError(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("dial error")
	}))

	err := client.do(context.Background(), http.MethodGet, dnscasterZonePath, nil, nil, &ListResponse[Zone]{})
	if err == nil {
		t.Fatalf("expected error")
	}

	var netErr *NetworkError
	if !errors.As(err, &netErr) {
		t.Fatalf("expected NetworkError, got: %T (%v)", err, err)
	}
}

func TestDoReturnsAPIError(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"message":"bad request","errors":["invalid"]}`)),
			Header:     make(http.Header),
		}, nil
	}))

	err := client.do(context.Background(), http.MethodGet, dnscasterZonePath, nil, nil, &ListResponse[Zone]{})
	if err == nil {
		t.Fatalf("expected error")
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got: %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected status code: %d", apiErr.StatusCode)
	}
}

func TestDoReturnsDataErrorOnUndecodableAPIErrorBody(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"message":`)),
			Header:     make(http.Header),
		}, nil
	}))

	err := client.do(context.Background(), http.MethodGet, dnscasterZonePath, nil, nil, &ListResponse[Zone]{})
	if err == nil {
		t.Fatalf("expected error")
	}

	var dataErr *DataError
	if !errors.As(err, &dataErr) {
		t.Fatalf("expected DataError, got: %T (%v)", err, err)
	}
}

func TestDoReturnsNilOnAcceptedDelete(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodDelete {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	}))

	err := client.do(context.Background(), http.MethodDelete, dnscasterHostPath+"h-1", nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
