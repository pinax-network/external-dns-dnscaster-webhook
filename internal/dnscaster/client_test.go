package dnscaster

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
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

	dialErr := errors.New("dial error")
	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, dialErr
	}))

	_, err := client.do[ListResponse[Zone]](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil)
	if err == nil {
		t.Fatalf("expected error")
	}

	if _, ok := errors.AsType[*NetworkError](err); !ok {
		t.Fatalf("expected NetworkError, got: %T (%v)", err, err)
	}
	if !errors.Is(err, dialErr) {
		t.Fatalf("expected the dial error to be wrapped, got: %v", err)
	}
	if !strings.Contains(err.Error(), "dial error") {
		t.Fatalf("expected the dial error in the message, got: %v", err)
	}
}

func TestDoReturnsDataError(t *testing.T) {
	t.Parallel()

	readErr := errors.New("connection reset")
	for name, tc := range map[string]struct {
		body      any
		response  *http.Response
		operation string
	}{
		"unmarshalable request body": {
			body:      make(chan int),
			operation: "marshal",
		},
		"undecodable response body": {
			response:  jsonResponse(http.StatusOK, `{"id":`, nil),
			operation: "unmarshal",
		},
		"unreadable response body": {
			response:  &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(iotest.ErrReader(readErr)), Header: make(http.Header)},
			operation: "read",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				if tc.response == nil {
					t.Fatalf("unexpected request")
				}
				return tc.response, nil
			}))

			_, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, tc.body)

			dataErr, ok := errors.AsType[*DataError](err)
			if !ok {
				t.Fatalf("expected DataError, got: %T (%v)", err, err)
			}
			if dataErr.Operation != tc.operation || !strings.Contains(err.Error(), tc.operation) {
				t.Fatalf("expected a %s error, got: %v", tc.operation, err)
			}
			if dataErr.Unwrap() == nil {
				t.Fatalf("expected the cause to be wrapped, got: %v", err)
			}
		})
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

func TestDoSkipsWritesInDryRun(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected %s %s in dry-run mode", req.Method, req.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"id":"z-1"}`, nil), nil
	}))
	client.DryRun = true

	host, err := client.do[Host](context.Background(), http.MethodPost, dnscasterHostPath, nil, HostEnvelope{Host: Host{ZoneID: "z-1"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if host.ID != "" {
		t.Fatalf("expected an empty host, got: %+v", host)
	}

	if _, err := client.do[noContent](context.Background(), http.MethodDelete, dnscasterHostPath+"h-1", nil, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Reads still reach the API so the plan reflects the real state.
	zone, err := client.do[Zone](context.Background(), http.MethodGet, dnscasterZonePath, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if zone.ID != "z-1" {
		t.Fatalf("expected zone z-1, got: %+v", zone)
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

func TestListAllFailsWhenLastRecordHasNoID(t *testing.T) {
	t.Parallel()

	client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"collection":[{"domain":"example.com"}],"more_results":true}`, nil), nil
	}))

	_, err := client.listAll[Zone](context.Background(), dnscasterZonePath, nil)
	if err == nil || !strings.Contains(err.Error(), "no id") {
		t.Fatalf("expected a missing id error, got: %v", err)
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

func TestDoDoesNotRetryFailedRequests(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			t.Run(fmt.Sprintf("%s %d", method, status), func(t *testing.T) {
				t.Parallel()

				calls := 0
				client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
					calls++
					return jsonResponse(status, "", http.Header{"Retry-After": []string{"30"}}), nil
				}))

				// Waiting out Retry-After would run past the external-dns webhook
				// timeout, so the error goes back to external-dns to retry on its
				// next reconcile.
				_, err := client.do[Host](context.Background(), method, dnscasterHostPath, nil, nil)

				apiErr, ok := errors.AsType[*APIError](err)
				if !ok {
					t.Fatalf("expected APIError, got: %T (%v)", err, err)
				}
				if apiErr.StatusCode != status {
					t.Fatalf("unexpected status code: %d", apiErr.StatusCode)
				}
				if calls != 1 {
					t.Fatalf("expected 1 call, got %d", calls)
				}
			})
		}
	}
}

func TestClientMethodsSendExpectedRequests(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		call     func(context.Context, *DNScasterApiClient) (any, error)
		method   string
		path     string
		query    url.Values
		body     string
		status   int
		response string
		want     any
	}{
		"ListZones": {
			call:     func(ctx context.Context, c *DNScasterApiClient) (any, error) { return c.ListZones(ctx) },
			method:   http.MethodGet,
			path:     "/v1/zones/",
			query:    url.Values{"max_results": {"100"}},
			status:   http.StatusOK,
			response: `{"collection":[{"id":"z-1","domain":"example.com"}],"more_results":false}`,
			want:     []Zone{{ID: "z-1", Domain: "example.com"}},
		},
		"ListHosts": {
			call:     func(ctx context.Context, c *DNScasterApiClient) (any, error) { return c.ListHosts(ctx) },
			method:   http.MethodGet,
			path:     "/v1/hosts/",
			query:    url.Values{"max_results": {"100"}, "properties[owner-id]": {"controller-1"}},
			status:   http.StatusOK,
			response: `{"collection":[{"id":"h-1","fqdn":"app.example.com"}],"more_results":false}`,
			want:     []Host{{ID: "h-1", FQDN: "app.example.com"}},
		},
		"CreateHost": {
			call: func(ctx context.Context, c *DNScasterApiClient) (any, error) {
				return c.CreateHost(ctx, Host{ZoneID: "z-1", Hostname: "app"})
			},
			method:   http.MethodPost,
			path:     "/v1/hosts/",
			body:     `{"host":{"zone_id":"z-1","hostname":"app"}}`,
			status:   http.StatusCreated,
			response: `{"id":"h-1","zone_id":"z-1","hostname":"app"}`,
			want:     Host{ID: "h-1", ZoneID: "z-1", Hostname: "app"},
		},
		"DeleteHost": {
			call: func(ctx context.Context, c *DNScasterApiClient) (any, error) {
				return nil, c.DeleteHost(ctx, "h-1")
			},
			method: http.MethodDelete,
			path:   "/v1/hosts/h-1",
			status: http.StatusAccepted,
		},
		"GetMonitor": {
			call: func(ctx context.Context, c *DNScasterApiClient) (any, error) {
				return c.GetMonitor(ctx, "m-1")
			},
			method:   http.MethodGet,
			path:     "/v1/ip_monitors/m-1",
			status:   http.StatusOK,
			response: `{"id":"m-1","uri":"ping://1.2.3.4"}`,
			want:     Monitor{ID: "m-1", URI: "ping://1.2.3.4"},
		},
		"CreateMonitor": {
			call: func(ctx context.Context, c *DNScasterApiClient) (any, error) {
				return c.CreateMonitor(ctx, Monitor{Name: "app.example.com", NameserverSetID: "ns-1", URI: "ping://1.2.3.4"})
			},
			method:   http.MethodPost,
			path:     "/v1/ip_monitors/",
			body:     `{"ip_monitor":{"name":"app.example.com","nameserver_set_id":"ns-1","uri":"ping://1.2.3.4"}}`,
			status:   http.StatusCreated,
			response: `{"id":"m-1","name":"app.example.com","nameserver_set_id":"ns-1","uri":"ping://1.2.3.4"}`,
			want:     Monitor{ID: "m-1", Name: "app.example.com", NameserverSetID: "ns-1", URI: "ping://1.2.3.4"},
		},
		"DeleteMonitor": {
			call: func(ctx context.Context, c *DNScasterApiClient) (any, error) {
				return nil, c.DeleteMonitor(ctx, "m-1")
			},
			method: http.MethodDelete,
			path:   "/v1/ip_monitors/m-1",
			status: http.StatusAccepted,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := testClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != tc.method || req.URL.Path != tc.path {
					t.Fatalf("expected %s %s, got: %s %s", tc.method, tc.path, req.Method, req.URL.Path)
				}
				if got := req.URL.Query(); !maps.EqualFunc(got, tc.query, slices.Equal) {
					t.Fatalf("expected query %v, got: %v", tc.query, got)
				}
				var body []byte
				if req.Body != nil {
					body, _ = io.ReadAll(req.Body)
				}
				if string(body) != tc.body {
					t.Fatalf("expected body %s, got: %s", tc.body, body)
				}
				return jsonResponse(tc.status, tc.response, nil), nil
			}))
			client.OwnerID = "controller-1"

			got, err := tc.call(context.Background(), client)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("expected %+v, got: %+v", tc.want, got)
			}
		})

		t.Run(name+" returns API errors", func(t *testing.T) {
			t.Parallel()

			client := testClient(roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusInternalServerError, `{"message":"boom"}`, nil), nil
			}))

			_, err := tc.call(context.Background(), client)
			if _, ok := errors.AsType[*APIError](err); !ok {
				t.Fatalf("expected APIError, got: %T (%v)", err, err)
			}
		})
	}
}

func TestNewDNScasterClientConfiguresTLS(t *testing.T) {
	t.Parallel()

	for _, skipVerify := range []bool{false, true} {
		client, err := NewDNScasterClient(&DNScasterConnectionConfig{SkipTLSVerify: skipVerify}, &DNScasterDefaults{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("expected an *http.Transport, got: %T", client.Transport)
		}
		if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
			t.Fatalf("expected TLS 1.2 or newer, got min version: %x", transport.TLSClientConfig.MinVersion)
		}
		if transport.TLSClientConfig.InsecureSkipVerify != skipVerify {
			t.Fatalf("expected InsecureSkipVerify=%t, got: %t", skipVerify, transport.TLSClientConfig.InsecureSkipVerify)
		}
		if client.Jar == nil {
			t.Fatalf("expected a cookie jar")
		}
	}
}

func TestSetVersionUpdatesUserAgent(t *testing.T) {
	// Not parallel: clientVersion is package state read by every request.
	previous := clientVersion
	t.Cleanup(func() { clientVersion = previous })

	SetVersion("v1.2.3")
	if got := userAgent(); !strings.HasPrefix(got, "external-dns-dnscaster-webhook/v1.2.3 ") {
		t.Fatalf("expected the version in the User-Agent, got: %q", got)
	}

	// An empty version keeps the one already set.
	SetVersion("")
	if got := userAgent(); !strings.HasPrefix(got, "external-dns-dnscaster-webhook/v1.2.3 ") {
		t.Fatalf("expected the version to be kept, got: %q", got)
	}
}

func TestNormalizeOperation(t *testing.T) {
	t.Parallel()

	for path, want := range map[string]string{
		dnscasterZonePath:            "zones",
		dnscasterHostPath:            "hosts",
		dnscasterHostPath + "h-1":    "hosts",
		dnscasterMonitorPath:         "monitors",
		dnscasterMonitorPath + "m-1": "monitors",
		"v1/other/":                  "v1/other/",
	} {
		if got := normalizeOperation(path); got != want {
			t.Fatalf("normalizeOperation(%q): expected %q, got: %q", path, want, got)
		}
	}
}
