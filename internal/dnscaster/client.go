package dnscaster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/metrics"
)

const (
	dnscasterBaseUrl     = "api.dnscaster.com"
	dnscasterZonePath    = "v1/zones/"
	dnscasterHostPath    = "v1/hosts/"
	dnscasterMonitorPath = "v1/ip_monitors/"

	// dnscasterOperation is the operation label used for API health metrics.
	dnscasterOperation = "dnscaster_api"

	// projectURL is the contact URL sent in the User-Agent header.
	projectURL = "https://github.com/pinax-network/external-dns-dnscaster-webhook"
)

// Paging limits, per https://dnscaster.com/docs/api/basics
const (
	defaultPageSize = 100
	maxPageSize     = 1000
)

// Retry behaviour for the transient statuses documented at
// https://dnscaster.com/docs/api/status-codes
const (
	maxRetries     = 4
	retryBaseDelay = 500 * time.Millisecond
	maxRetryDelay  = 30 * time.Second
)

// clientVersion identifies this build in the User-Agent header. SetVersion is
// called from main once the version is known.
var clientVersion = "dev"

// SetVersion records the build version reported in the User-Agent header.
func SetVersion(version string) {
	if version != "" {
		clientVersion = version
	}
}

type DNScasterDefaults struct {
	DefaultTTL      int64 `env:"DNSCASTER_DEFAULT_TTL" envDefault:"300"`
	DefaultPageSize int   `env:"DNSCASTER_DEFAULT_PAGE_SIZE" envDefault:"100"`
}

// DNScasterConnectionConfig holds the connection details for the API client
type DNScasterConnectionConfig struct {
	OwnerID         string `env:"DNSCASTER_OWNER_ID,notEmpty"`
	ApiKey          string `env:"DNSCASTER_API_KEY,notEmpty"`
	NameserverSetID string `env:"DNSCASTER_NAMESERVER_SET_ID,notEmpty"`
	SkipTLSVerify   bool   `env:"DNSCASTER_SKIP_TLS_VERIFY" envDefault:"false"`
}

// DNScasterApiClient encapsulates the client configuration and HTTP client
type DNScasterApiClient struct {
	*DNScasterDefaults
	*DNScasterConnectionConfig
	*http.Client
}

// apiResponse is the outcome of a single HTTP attempt against the API.
type apiResponse struct {
	statusCode int
	requestID  string
	header     http.Header
	body       []byte
}

// NewDNScasterClient creates a new instance of DnscasterApiClient
func NewDNScasterClient(config *DNScasterConnectionConfig, defaults *DNScasterDefaults) (*DNScasterApiClient, error) {
	log.Info("creating a new Dnscaster API Client")

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		log.Error("failed to create cookie jar: %v", err)
		return nil, err
	}

	client := &DNScasterApiClient{
		DNScasterDefaults:         defaults,
		DNScasterConnectionConfig: config,
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					// The API requires TLS 1.2 or newer.
					MinVersion:         tls.VersionTLS12,
					InsecureSkipVerify: config.SkipTLSVerify,
				},
			},
			Jar: jar,
		},
	}

	return client, nil
}

func (c *DNScasterApiClient) ListZones(ctx context.Context) ([]Zone, error) {
	zones, err := c.listAll[Zone](ctx, dnscasterZonePath, nil)
	if err != nil {
		return nil, err
	}

	log.Debug("ListZones", "count", len(zones))
	return zones, nil
}

func (c *DNScasterApiClient) ListHosts(ctx context.Context) ([]Host, error) {
	q := url.Values{}
	q.Set("properties["+ProviderMetadataOwnerID+"]", c.OwnerID)

	hosts, err := c.listAll[Host](ctx, dnscasterHostPath, q)
	if err != nil {
		return nil, fmt.Errorf("failed to list hosts: %w", err)
	}

	log.Debug("ListHosts", "count", len(hosts))
	return hosts, nil
}

func (c *DNScasterApiClient) CreateHost(ctx context.Context, host Host) (Host, error) {
	out, err := c.do[Host](ctx, http.MethodPost, dnscasterHostPath, nil, HostEnvelope{Host: host})
	if err != nil {
		return out, fmt.Errorf("failed to create host: %w", err)
	}

	log.Debug("CreateHost", "host", out)
	return out, nil
}

func (c *DNScasterApiClient) DeleteHost(ctx context.Context, hostID string) error {
	if _, err := c.do[noContent](ctx, http.MethodDelete, dnscasterHostPath+hostID, nil, nil); err != nil {
		return fmt.Errorf("failed to delete host: %w", err)
	}

	log.Debug("DeleteHost", "host.id", hostID)
	return nil
}

func (c *DNScasterApiClient) GetMonitor(ctx context.Context, monitorID string) (Monitor, error) {
	out, err := c.do[Monitor](ctx, http.MethodGet, dnscasterMonitorPath+monitorID, nil, nil)
	if err != nil {
		return out, fmt.Errorf("failed to get monitor: %w", err)
	}

	log.Debug("GetMonitor", "monitor", out)
	return out, nil
}

func (c *DNScasterApiClient) CreateMonitor(ctx context.Context, monitor Monitor) (Monitor, error) {
	out, err := c.do[Monitor](ctx, http.MethodPost, dnscasterMonitorPath, nil, MonitorEnvelope{Monitor: monitor})
	if err != nil {
		return out, fmt.Errorf("failed to create monitor: %w", err)
	}

	log.Debug("CreateMonitor", "monitor", out)
	return out, nil
}

func (c *DNScasterApiClient) DeleteMonitor(ctx context.Context, monitorID string) error {
	if _, err := c.do[noContent](ctx, http.MethodDelete, dnscasterMonitorPath+monitorID, nil, nil); err != nil {
		return fmt.Errorf("failed to delete monitor: %w", err)
	}

	log.Debug("DeleteMonitor", "monitor.id", monitorID)
	return nil
}

// listAll fetches every page of a paged list endpoint. The API returns at most
// max_results records per call and sets more_results when further pages exist;
// each following page starts after the ID of the last record seen.
//
// Paging is driven by resource ID, so callers must not override the sort or
// direction query parameters.
func (c *DNScasterApiClient) listAll[T pageable](ctx context.Context, path string, query url.Values) ([]T, error) {
	// maxPages bounds the paging loop so a misbehaving API cannot spin
	// forever. At maxPageSize this still covers a million resources.
	const maxPages int = 1000

	var all []T
	var after string

	for page := 1; page <= maxPages; page++ {
		q := url.Values{}
		maps.Copy(q, query)
		q.Set("max_results", strconv.Itoa(c.pageSize()))
		if after != "" {
			q.Set("after", after)
		}

		out, err := c.do[ListResponse[T]](ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return nil, err
		}

		all = append(all, out.Collection...)
		log.Debug("listAll", "path", path, "page", page, "page.count", len(out.Collection), "total", len(all), "more_results", out.MoreResults)

		if !out.MoreResults {
			return all, nil
		}

		// A silently truncated list is worse than a failed one: external-dns
		// would treat the missing records as absent and recreate them. So every
		// case where paging cannot continue is an error, not a short result.
		if len(out.Collection) == 0 {
			return nil, fmt.Errorf("paging %s: API reported more results but returned an empty page %d", path, page)
		}

		last := out.Collection[len(out.Collection)-1].GetID()
		switch last {
		case "":
			return nil, fmt.Errorf("paging %s: last record of page %d has no id to continue from", path, page)
		case after:
			return nil, fmt.Errorf("paging %s: cursor did not advance past %q", path, after)
		}
		after = last
	}

	return nil, fmt.Errorf("paging %s: exceeded the %d page limit", path, maxPages)
}

// pageSize is the max_results value sent with list requests, clamped to the
// range the API accepts.
func (c *DNScasterApiClient) pageSize() int {
	switch {
	case c.DefaultPageSize <= 0:
		return defaultPageSize
	case c.DefaultPageSize > maxPageSize:
		return maxPageSize
	default:
		return c.DefaultPageSize
	}
}

// do sends a request to the DNScaster API and decodes the JSON response into T.
// The zero value of T is returned on error and for responses without a payload.
// Rate-limited and transient server errors are retried with backoff.
func (c *DNScasterApiClient) do[T any](ctx context.Context, method, path string, query url.Values, body any) (T, error) {
	var out T

	success := false
	defer func() { metrics.Get().MarkOperation(dnscasterOperation, success) }()

	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return out, NewDataError("marshal", "API request body", err)
		}
	}

	rawURL := requestURL(path, query)

	for attempt := 0; ; attempt++ {
		resp, err := c.send(ctx, method, path, rawURL, payload)
		if err != nil {
			return out, err
		}

		if resp.statusCode < 200 || resp.statusCode >= 300 {
			if canRetry(method, resp.statusCode) && attempt < maxRetries {
				delay := retryDelay(resp.header, attempt)
				log.Debug("retrying DNScaster API call",
					"method", method, "path", path, "status", resp.statusCode,
					"attempt", attempt+1, "delay", delay, "request.id", resp.requestID,
					"ratelimit.remaining", resp.header.Get("RateLimit-Remaining"),
					"ratelimit.reset", resp.header.Get("RateLimit-Reset"))

				if err := wait(ctx, delay); err != nil {
					return out, err
				}
				continue
			}

			return out, apiErrorFrom(method, path, resp)
		}

		// 202 (accepted) and 204 (no content) carry no payload.
		if len(resp.body) == 0 {
			success = true
			return out, nil
		}

		if err := json.Unmarshal(resp.body, &out); err != nil {
			return out, NewDataError("unmarshal", "API response body", err)
		}

		success = true
		return out, nil
	}
}

// send performs a single attempt and reads the full response body.
func (c *DNScasterApiClient) send(ctx context.Context, method, path, rawURL string, payload []byte) (*apiResponse, error) {
	var bodyReader io.Reader
	if len(payload) > 0 {
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent())
	if c.ApiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ApiKey)
	}
	if len(payload) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	m := metrics.Get()
	start := time.Now()
	statusCode := 0
	responseBytes := 0
	operation := normalizeOperation(path)
	defer func() {
		m.ObserveDNScasterCall(method, operation, statusCode, time.Since(start), responseBytes)
	}()

	resp, err := c.Do(req)
	if err != nil {
		return nil, NewNetworkError(method, rawURL, err)
	}
	defer resp.Body.Close()

	statusCode = resp.StatusCode

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, NewDataError("read", "API response body", err)
	}
	responseBytes = len(responseBody)

	return &apiResponse{
		statusCode: resp.StatusCode,
		requestID:  resp.Header.Get("X-Request-ID"),
		header:     resp.Header,
		body:       responseBody,
	}, nil
}

// apiErrorFrom turns a non-2xx response into an APIError. Error payloads are
// informative but may be absent, so a missing body still yields an APIError
// carrying the status code and request ID.
func apiErrorFrom(method, path string, resp *apiResponse) error {
	var apiErr DnscasterErrorResponse

	if len(resp.body) > 0 {
		decoded, err := decodeJSON[DnscasterErrorResponse](bytes.NewReader(resp.body))
		if err != nil {
			return NewDataError("unmarshal", "API error response", err)
		}
		apiErr = decoded
	}

	return NewAPIError(method, path, resp.statusCode, resp.requestID, apiErr.Message, apiErr.Errors)
}

// canRetry reports whether a failed request may safely be repeated. A 429 is
// always safe: the request was rejected before it took effect. Server errors
// are only retried for GET, since a POST or DELETE may already have been
// applied even though the response never made it back.
func canRetry(method string, statusCode int) bool {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return true
	case statusCode >= 500 && method == http.MethodGet:
		return true
	default:
		return false
	}
}

// retryDelay honours Retry-After when the API sends it and otherwise backs off
// exponentially.
func retryDelay(header http.Header, attempt int) time.Duration {
	if delay, ok := parseRetryAfter(header.Get("Retry-After")); ok {
		return min(delay, maxRetryDelay)
	}
	return min(retryBaseDelay<<attempt, maxRetryDelay)
}

// parseRetryAfter reads a Retry-After value, which the API documents in seconds
// but HTTP also allows as a date.
func parseRetryAfter(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}

	if at, err := http.ParseTime(value); err == nil {
		return max(time.Until(at), 0), true
	}

	return 0, false
}

// wait sleeps for d unless the context is cancelled first.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// userAgent identifies this webhook to the API, which asks for an app
// identifier carrying a version and a contact URL.
func userAgent() string {
	return "external-dns-dnscaster-webhook/" + clientVersion + " (+" + projectURL + ")"
}

func requestURL(path string, query url.Values) string {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: path}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func normalizeOperation(path string) string {
	switch {
	case path == dnscasterZonePath:
		return "zones"
	case strings.HasPrefix(path, dnscasterHostPath):
		return "hosts"
	case strings.HasPrefix(path, dnscasterMonitorPath):
		return "monitors"
	default:
		return path
	}
}

func decodeJSON[T any](r io.Reader) (T, error) {
	var out T
	err := json.NewDecoder(r).Decode(&out)
	return out, err
}
