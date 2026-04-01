package dnscaster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/metrics"
)

// Note: Methods would be a good fit to be rewritten with generics in mind now that
//       https://github.com/golang/go/issues/77273 is accepted.

const (
	dnscasterBaseUrl     = "api.dnscaster.com"
	dnscasterZonePath    = "v1/zones/"
	dnscasterHostPath    = "v1/hosts/"
	dnscasterMonitorPath = "v1/ip_monitors/"
)

type DNScasterDefaults struct {
	DefaultTTL int64 `env:"DNSCASTER_DEFAULT_TTL" envDefault:"300"`
}

// DNScasterConnectionConfig holds the connection details for the API client
type DNScasterConnectionConfig struct {
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
					InsecureSkipVerify: config.SkipTLSVerify,
				},
			},
			Jar: jar,
		},
	}

	return client, nil
}

func (c *DNScasterApiClient) ListZones(ctx context.Context) ([]Zone, error) {
	var out ListResponse[Zone]

	if err := c.do(ctx, http.MethodGet, dnscasterZonePath, nil, nil, &out); err != nil {
		return nil, err
	}

	log.Debug("ListZones", "count", len(out.Collection))
	return out.Collection, nil
}

func (c *DNScasterApiClient) ListHosts(ctx context.Context, zoneID string) ([]Host, error) {
	var out ListResponse[Host]

	q := url.Values{}
	q.Set("zone_id", zoneID)

	if err := c.do(ctx, http.MethodGet, dnscasterHostPath, q, nil, &out); err != nil {
		return nil, fmt.Errorf("failed to list hosts: %w", err)
	}

	log.Debug("ListHosts", "count", len(out.Collection))
	return out.Collection, nil
}

func (c *DNScasterApiClient) CreateHost(ctx context.Context, host Host) (Host, error) {
	var out Host

	if err := c.do(ctx, http.MethodPost, dnscasterHostPath, nil, HostEnvelope{Host: host}, &out); err != nil {
		return out, fmt.Errorf("failed to create host: %w", err)
	}

	log.Debug("CreateHost", "host", out)
	return out, nil
}

func (c *DNScasterApiClient) DeleteHost(ctx context.Context, hostID string) error {
	if err := c.do(ctx, http.MethodDelete, dnscasterHostPath+hostID, nil, nil, nil); err != nil {
		return fmt.Errorf("failed to delete host: %w", err)
	}
	log.Debug("DeleteHost", "host.id", hostID)
	return nil
}

func (c *DNScasterApiClient) GetMonitor(ctx context.Context, monitorID string) (Monitor, error) {
	var out Monitor

	if err := c.do(ctx, http.MethodGet, dnscasterMonitorPath+monitorID, nil, nil, &out); err != nil {
		return out, fmt.Errorf("failed to get monitor: %w", err)
	}

	log.Debug("GetMonitor", "monitor", out)
	return out, nil
}

func (c *DNScasterApiClient) CreateMonitor(ctx context.Context, monitor Monitor) (Monitor, error) {
	var out Monitor

	if err := c.do(ctx, http.MethodPost, dnscasterMonitorPath, nil, MonitorEnvelope{Monitor: monitor}, &out); err != nil {
		return out, fmt.Errorf("failed to create monitor: %w", err)
	}

	log.Debug("CreateMonitor", "monitor", out)
	return out, nil
}

func (c *DNScasterApiClient) DeleteMonitor(ctx context.Context, monitorID string) error {
	if err := c.do(ctx, http.MethodDelete, dnscasterMonitorPath+monitorID, nil, nil, nil); err != nil {
		return fmt.Errorf("failed to delete monitor: %w", err)
	}

	log.Debug("DeleteMonitor", "monitor.id", monitorID)
	return nil
}

func (c *DNScasterApiClient) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var bodyReader io.Reader
	var err error

	m := metrics.Get()
	start := time.Now()
	statusCode := 0
	responseBytes := 0
	operation := normalizeOperation(path)
	defer func() {
		m.ObserveDNScasterCall(method, operation, statusCode, time.Since(start), responseBytes)
	}()

	url := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: path}

	if body != nil {
		bodyReader, err = encodeJSON(body)
		if err != nil {
			return err
		}
	}

	if query != nil {
		q := url.Query()
		for k, vals := range query {
			for _, v := range vals {
				q.Add(k, v)
			}
		}
		url.RawQuery = q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, url.String(), bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if c.ApiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ApiKey)
	}
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.Do(req)
	if err != nil {
		m.MarkOperation("dnscaster_api", false)
		return NewNetworkError(method, url.String(), err)
	}
	defer resp.Body.Close()

	statusCode = resp.StatusCode

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		m.MarkOperation("dnscaster_api", false)
		return NewDataError("read", "API response body", err)
	}
	responseBytes = len(responseBody)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr, decodeErr := decodeJSON[DnscasterErrorResponse](bytes.NewReader(responseBody))
		if decodeErr != nil {
			m.MarkOperation("dnscaster_api", false)
			return NewDataError("unmarshal", "API error response", decodeErr)
		}
		m.MarkOperation("dnscaster_api", false)
		return NewAPIError(method, path, resp.StatusCode, apiErr.Message, apiErr.Errors)
	}

	// DNScaster returns 202 only on successful DELETE without a response body
	if resp.StatusCode == http.StatusAccepted {
		m.MarkOperation("dnscaster_api", true)
		return nil
	}

	if out == nil || len(responseBody) == 0 {
		m.MarkOperation("dnscaster_api", true)
		return nil
	}

	if err := json.Unmarshal(responseBody, out); err != nil {
		m.MarkOperation("dnscaster_api", false)
		return err
	}
	m.MarkOperation("dnscaster_api", true)
	return nil
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

func encodeJSON(v any) (io.Reader, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(b), nil
}

func decodeJSON[T any](r io.Reader) (T, error) {
	var out T
	err := json.NewDecoder(r).Decode(&out)
	return out, err
}
