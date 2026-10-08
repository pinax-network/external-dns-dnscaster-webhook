package dnscaster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

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

	out, err := client.do[ListResponse[Zone]](context.Background(), http.MethodGet, dnscasterZonePath, url.Values{"limit": {"10"}}, nil)
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

	_, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{Host: Host{ZoneID: "z-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoReturnsNetworkError(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("dial error")
	}))

	_, err := client.do[ListResponse[Zone]](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}

	if _, ok := errors.AsType[*NetworkError](err); !ok {
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

	_, err := client.do[ListResponse[Zone]](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}

	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
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

	_, err := client.do[ListResponse[Zone]](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}

	if _, ok := errors.AsType[*DataError](err); !ok {
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

	_, err := client.do[struct{}](context.Background(), http.MethodDelete, dnscasterHostPath+"h-1", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func jsonResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     header,
	}
}

func TestDoSendsRequiredHeaders(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("unexpected Accept: %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-api-key" {
			t.Fatalf("unexpected Authorization: %q", got)
		}
		if got := req.Header.Get("User-Agent"); !strings.HasPrefix(got, "external-dns-dnscaster-webhook/") {
			t.Fatalf("unexpected User-Agent: %q", got)
		}
		if got := req.Header.Get("User-Agent"); !strings.Contains(got, projectURL) {
			t.Fatalf("User-Agent is missing the contact URL: %q", got)
		}

		return jsonResponse(http.StatusOK, `{"id":"z-1"}`, nil), nil
	}))

	if _, err := client.do[Zone](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIErrorCarriesRequestID(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusUnprocessableEntity,
			`{"message":"Validation failed","errors":["Nameserver set is required."]}`,
			http.Header{"X-Request-Id": []string{"rq_abc123"}}), nil
	}))

	_, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{})

	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("expected APIError, got: %T (%v)", err, err)
	}
	if apiErr.RequestID != "rq_abc123" {
		t.Fatalf("unexpected request id: %q", apiErr.RequestID)
	}
	if !strings.Contains(apiErr.Error(), "rq_abc123") {
		t.Fatalf("request id missing from message: %s", apiErr.Error())
	}
}

func TestAPIErrorOnEmptyErrorBody(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusBadGateway, "", nil), nil
	}))

	// A 5xx with no payload must still surface as an APIError carrying the
	// status, not as a decode failure.
	_, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{})

	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		t.Fatalf("expected APIError, got: %T (%v)", err, err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected status code: %d", apiErr.StatusCode)
	}
}

func TestListAllFollowsPagesUntilMoreResultsIsFalse(t *testing.T) {
	t.Parallel()

	var afters []string
	var maxResults []string

	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		q := req.URL.Query()
		afters = append(afters, q.Get("after"))
		maxResults = append(maxResults, q.Get("max_results"))

		if got := q.Get("properties[owner]"); got != "o-1" {
			t.Fatalf("caller query param was dropped on page %d: %q", len(afters), got)
		}

		switch q.Get("after") {
		case "":
			return jsonResponse(http.StatusOK, `{"collection":[{"id":"h-1"},{"id":"h-2"}],"more_results":true}`, nil), nil
		case "h-2":
			return jsonResponse(http.StatusOK, `{"collection":[{"id":"h-3"},{"id":"h-4"}],"more_results":true}`, nil), nil
		case "h-4":
			return jsonResponse(http.StatusOK, `{"collection":[{"id":"h-5"}],"more_results":false}`, nil), nil
		default:
			t.Fatalf("unexpected after param: %q", q.Get("after"))
			return nil, nil
		}
	}))

	hosts, err := client.listAll[Host](context.Background(), dnscasterHostPath, url.Values{"properties[owner]": {"o-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ids []string
	for _, host := range hosts {
		ids = append(ids, host.ID)
	}
	if want := []string{"h-1", "h-2", "h-3", "h-4", "h-5"}; !slices.Equal(ids, want) {
		t.Fatalf("unexpected hosts: got %v, want %v", ids, want)
	}
	if want := []string{"", "h-2", "h-4"}; !slices.Equal(afters, want) {
		t.Fatalf("unexpected after params: got %v, want %v", afters, want)
	}
	if want := []string{"100", "100", "100"}; !slices.Equal(maxResults, want) {
		t.Fatalf("unexpected max_results params: got %v, want %v", maxResults, want)
	}
}

func TestListAllSendsConfiguredPageSizeClampedToAPIMaximum(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ configured, want int }{
		"unset":             {0, defaultPageSize},
		"negative":          {-5, defaultPageSize},
		"in range":          {250, 250},
		"above the maximum": {5000, maxPageSize},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got string
			client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				got = req.URL.Query().Get("max_results")
				return jsonResponse(http.StatusOK, `{"collection":[],"more_results":false}`, nil), nil
			}))
			client.DefaultPageSize = tc.configured

			if _, err := client.listAll[Zone](context.Background(), dnscasterZonePath, nil); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != strconv.Itoa(tc.want) {
				t.Fatalf("unexpected max_results: got %q, want %d", got, tc.want)
			}
		})
	}
}

func TestListAllFailsWhenCursorDoesNotAdvance(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		// Always claims more results while returning the same last ID.
		return jsonResponse(http.StatusOK, `{"collection":[{"id":"h-1"}],"more_results":true}`, nil), nil
	}))

	// Truncating silently would make external-dns recreate the missing records,
	// so a stuck cursor has to be an error.
	hosts, err := client.listAll[Host](context.Background(), dnscasterHostPath, nil)
	if err == nil {
		t.Fatalf("expected error, got %d hosts", len(hosts))
	}
	if hosts != nil {
		t.Fatalf("expected no partial results, got: %v", hosts)
	}
	if !strings.Contains(err.Error(), "cursor did not advance") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListAllFailsOnEmptyPageWithMoreResults(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"collection":[],"more_results":true}`, nil), nil
	}))

	if _, err := client.listAll[Zone](context.Background(), dnscasterZonePath, nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestDoRetriesRateLimitedRequests(t *testing.T) {
	t.Parallel()

	calls := 0
	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return jsonResponse(http.StatusTooManyRequests, `{"message":"slow down"}`, http.Header{
				"Retry-After":         []string{"0"},
				"Ratelimit-Remaining": []string{"0"},
			}), nil
		}
		return jsonResponse(http.StatusOK, `{"id":"h-1"}`, nil), nil
	}))

	host, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
	if host.ID != "h-1" {
		t.Fatalf("unexpected host: %+v", host)
	}
}

func TestDoRetriesServerErrorsForGetOnly(t *testing.T) {
	t.Parallel()

	t.Run("get is retried", func(t *testing.T) {
		t.Parallel()

		calls := 0
		client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return jsonResponse(http.StatusServiceUnavailable, "", http.Header{"Retry-After": []string{"0"}}), nil
			}
			return jsonResponse(http.StatusOK, `{"collection":[],"more_results":false}`, nil), nil
		}))

		if _, err := client.do[ListResponse[Zone]](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("post is not retried", func(t *testing.T) {
		t.Parallel()

		calls := 0
		client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls++
			return jsonResponse(http.StatusBadGateway, "", http.Header{"Retry-After": []string{"0"}}), nil
		}))

		// The host may already have been created, so replaying the POST could
		// duplicate it.
		if _, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{}); err == nil {
			t.Fatalf("expected error")
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})
}

func TestDoStopsRetryingAfterMaxRetries(t *testing.T) {
	t.Parallel()

	calls := 0
	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls++
		return jsonResponse(http.StatusTooManyRequests, `{"message":"slow down"}`, http.Header{
			"Retry-After": []string{"0"},
		}), nil
	}))

	if _, err := client.do[Zone](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil); err == nil {
		t.Fatalf("expected error")
	}
	if want := maxRetries + 1; calls != want {
		t.Fatalf("expected %d calls, got %d", want, calls)
	}
}

func TestDoAbortsRetryOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		cancel()
		// No Retry-After, so the client would otherwise back off for 500ms.
		return jsonResponse(http.StatusTooManyRequests, `{"message":"slow down"}`, nil), nil
	}))

	if _, err := client.do[Zone](ctx, http.MethodGet, dnscasterZonePath, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestRetryDelay(t *testing.T) {
	t.Parallel()

	if got := retryDelay(http.Header{"Retry-After": []string{"9"}}, 0); got != 9*time.Second {
		t.Fatalf("expected Retry-After to win, got %v", got)
	}
	if got := retryDelay(http.Header{"Retry-After": []string{"600"}}, 0); got != maxRetryDelay {
		t.Fatalf("expected Retry-After to be capped, got %v", got)
	}
	if got := retryDelay(make(http.Header), 0); got != retryBaseDelay {
		t.Fatalf("unexpected first backoff: %v", got)
	}
	if got := retryDelay(make(http.Header), 2); got != 4*retryBaseDelay {
		t.Fatalf("unexpected third backoff: %v", got)
	}
	if got := retryDelay(make(http.Header), 20); got != maxRetryDelay {
		t.Fatalf("expected backoff to be capped, got %v", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	if d, ok := parseRetryAfter("9"); !ok || d != 9*time.Second {
		t.Fatalf("seconds: got %v %v", d, ok)
	}
	if _, ok := parseRetryAfter(""); ok {
		t.Fatalf("empty value should not be usable")
	}
	if _, ok := parseRetryAfter("nonsense"); ok {
		t.Fatalf("unparsable value should not be usable")
	}
	if _, ok := parseRetryAfter("-1"); ok {
		t.Fatalf("negative value should not be usable")
	}
	if d, ok := parseRetryAfter("Mon, 22 Apr 2021 20:00:00 GMT"); !ok || d != 0 {
		t.Fatalf("past date should clamp to zero: got %v %v", d, ok)
	}
}
