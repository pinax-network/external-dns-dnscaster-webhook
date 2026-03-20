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

	"golang.org/x/net/publicsuffix"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

// Note: Methods would be a good fit to be rewritten with generics in mind now that
//       https://github.com/golang/go/issues/77273 is accepted.

const (
	dnscasterBaseUrl            = "api.dnscaster.com"
	dnscasterZonePath           = "v1/zones/"
	dnscasterHostPath           = "v1/hosts/"
	dnscasterMonitorPath        = "v1/ip_monitors/"
	dnscasterNameserverSetsPath = "v1/nameserver_sets/"
)

type DnscasterDefaults struct {
	DefaultTTL     int64  `env:"DNSCASTER_DEFAULT_TTL" envDefault:"300"`
	DefaultComment string `env:"DNSCASTER_DEFAULT_COMMENT" envDefault:""`
}

// DnscasterConnectionConfig holds the connection details for the API client
type DnscasterConnectionConfig struct {
	ApiKey        string `env:"DNSCASTER_API_KEY,notEmpty"`
	SkipTLSVerify bool   `env:"DNSCASTER_SKIP_TLS_VERIFY" envDefault:"false"`
}

// DnscasterApiClient encapsulates the client configuration and HTTP client
type DnscasterApiClient struct {
	*DnscasterDefaults
	*DnscasterConnectionConfig
	*http.Client
}

// NewDnscasterClient creates a new instance of DnscasterApiClient
func NewDnscasterClient(config *DnscasterConnectionConfig, defaults *DnscasterDefaults) (*DnscasterApiClient, error) {
	log.Info("creating a new Dnscaster API Client")

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		log.Error("failed to create cookie jar: %v", err)
		return nil, err
	}

	client := &DnscasterApiClient{
		DnscasterDefaults:         defaults,
		DnscasterConnectionConfig: config,
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

func (c *DnscasterApiClient) ListZones(ctx context.Context) ([]Zone, error) {
	var out ListResponse[Zone]

	if err := c.do(ctx, http.MethodGet, dnscasterZonePath, nil, nil, &out); err != nil {
		return nil, err
	}

	log.Debug("ListZones", "count", len(out.Collection))
	return out.Collection, nil
}

func (c *DnscasterApiClient) GetZone(ctx context.Context, domain string) (Zone, error) {
	var out Zone

	if err := c.do(ctx, http.MethodGet, dnscasterZonePath+domain, nil, nil, &out); err != nil {
		return out, err
	}

	log.Debug("GetZoneByDomain", "zone", out)
	return out, nil
}

func (c *DnscasterApiClient) ListHosts(ctx context.Context, zoneID string) ([]Host, error) {
	var out ListResponse[Host]

	q := url.Values{}
	q.Set("zone_id", zoneID)

	if err := c.do(ctx, http.MethodGet, dnscasterHostPath, q, nil, &out); err != nil {
		return nil, fmt.Errorf("failed to list hosts: %w", err)
	}

	log.Debug("ListHosts", "count", len(out.Collection))
	return out.Collection, nil
}

func (c *DnscasterApiClient) GetHost(ctx context.Context, hostID string) (Host, error) {
	var out Host

	if err := c.do(ctx, http.MethodGet, dnscasterHostPath+hostID, nil, nil, &out); err != nil {
		return out, fmt.Errorf("failed to get host: %w", err)
	}

	log.Debug("GetHost", "host", out)
	return out, nil
}

func (c *DnscasterApiClient) CreateHost(ctx context.Context, host Host) (Host, error) {
	var out Host

	if err := c.do(ctx, http.MethodPost, dnscasterHostPath, nil, HostEnvelope{Host: host}, &out); err != nil {
		return out, fmt.Errorf("failed to create host: %w", err)
	}

	log.Debug("CreateHost", "host", out)
	return out, nil
}

func (c *DnscasterApiClient) DeleteHost(ctx context.Context, hostID string) error {
	if err := c.do(ctx, http.MethodDelete, dnscasterHostPath+hostID, nil, nil, nil); err != nil {
		return fmt.Errorf("failed to delete host: %w", err)
	}
	log.Debug("DeleteHost", "host.id", hostID)
	return nil
}

func (c *DnscasterApiClient) ListMonitors(ctx context.Context) ([]Monitor, error) {
	var out ListResponse[Monitor]

	if err := c.do(ctx, http.MethodGet, dnscasterMonitorPath, nil, nil, &out); err != nil {
		return nil, fmt.Errorf("failed to list monitors: %w", err)
	}

	log.Debug("ListMonitors", "count", len(out.Collection))
	return out.Collection, nil
}

func (c *DnscasterApiClient) GetMonitor(ctx context.Context, monitorID string) (Monitor, error) {
	var out Monitor

	if err := c.do(ctx, http.MethodGet, dnscasterMonitorPath+monitorID, nil, nil, &out); err != nil {
		return out, fmt.Errorf("failed to get monitor: %w", err)
	}

	log.Debug("GetMonitor", "monitor", out)
	return out, nil
}

func (c *DnscasterApiClient) CreateMonitor(ctx context.Context, monitor Monitor) (Monitor, error) {
	var out Monitor

	if err := c.do(ctx, http.MethodPost, dnscasterMonitorPath, nil, MonitorEnvelope{Monitor: monitor}, &out); err != nil {
		return out, fmt.Errorf("failed to create monitor: %w", err)
	}

	log.Debug("CreateMonitor", "monitor", out)
	return out, nil
}

func (c *DnscasterApiClient) DeleteMonitor(ctx context.Context, monitorID string) error {
	if err := c.do(ctx, http.MethodDelete, dnscasterMonitorPath+monitorID, nil, nil, nil); err != nil {
		return fmt.Errorf("failed to delete monitor: %w", err)
	}

	log.Debug("DeleteMonitor", "monitor.id", monitorID)
	return nil
}

func (c *DnscasterApiClient) ListNameserverSets(ctx context.Context) ([]NameserverSet, error) {
	var out ListResponse[NameserverSet]

	if err := c.do(ctx, http.MethodGet, dnscasterNameserverSetsPath, nil, nil, &out); err != nil {
		return nil, fmt.Errorf("failed to list nameserver_sets: %w", err)
	}

	log.Debug("ListNameserverSets", "count", len(out.Collection))
	return out.Collection, nil
}

func (c *DnscasterApiClient) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var bodyReader io.Reader
	var err error

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
		return NewNetworkError(method, url.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr, decodeErr := decodeJSON[DnscasterErrorResponse](resp.Body)
		if decodeErr != nil {
			return NewDataError("unmarshal", "API error response", decodeErr)
		}
		return NewAPIError(method, path, resp.StatusCode, apiErr.Message, apiErr.Errors)
	}

	// DNScaster returns 202 only on successful DELETE without a response body
	if resp.StatusCode == http.StatusAccepted {
		return nil
	}

	return json.NewDecoder(resp.Body).Decode(out)
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
