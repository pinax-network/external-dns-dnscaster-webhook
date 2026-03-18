package dnscaster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"

	"github.com/pkg/errors"
	"golang.org/x/net/publicsuffix"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

const (
	dnscasterBaseUrl  = "api.dnscaster.com"
	dnscasterZonePath = "v1/zones"
	dnscasterHostPath = "v1/hosts"

	errorBodyBufferSize = 512
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
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: dnscasterZonePath}

	resp, err := c.doRequest(
		ctx,
		http.MethodGet,
		u,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch DNS zones from DNScaster")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	collection, err := decodeResponse[ListZonesResponse](resp, dnscasterZonePath)
	if err != nil {
		return nil, err
	}
	log.Debug("fetched zones", "count", len(collection.Zones))

	return collection.Zones, nil
}

func (c *DnscasterApiClient) GetZoneByDomain(ctx context.Context, domain string) (Zone, error) {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: path.Join(dnscasterZonePath, domain)}

	resp, err := c.doRequest(
		ctx,
		http.MethodGet,
		u,
		nil,
	)
	if err != nil {
		return Zone{}, errors.Wrap(err, "failed to fetch DNS zones from DNScaster")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	zone, err := decodeResponse[Zone](resp, dnscasterZonePath)
	if err != nil {
		return Zone{}, err
	}
	log.Debug("fetched zone", "domain", domain)

	return zone, nil
}

func (c *DnscasterApiClient) ListHosts(ctx context.Context, zoneID string) ([]Host, error) {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: dnscasterHostPath}
	q := u.Query()
	q.Set("zone_id", zoneID)
	u.RawQuery = q.Encode()

	resp, err := c.doRequest(
		ctx,
		http.MethodGet,
		u,
		nil,
	)
	if err != nil {
		return nil, errors.Wrap(err, "failed to fetch DNS hosts from DNScaster")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	collection, err := decodeResponse[ListHostsResponse](resp, dnscasterHostPath)
	if err != nil {
		return nil, err
	}
	log.Debug("fetched hosts", "count", len(collection.Hosts))

	return collection.Hosts, nil
}

func (c *DnscasterApiClient) GetHost(ctx context.Context, hostID string) (Host, error) {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: path.Join(dnscasterHostPath, hostID)}

	resp, err := c.doRequest(
		ctx,
		http.MethodGet,
		u,
		nil,
	)
	if err != nil {
		return Host{}, errors.Wrap(err, "failed to fetch DNS hosts from DNScaster")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	host, err := decodeResponse[Host](resp, dnscasterHostPath)
	if err != nil {
		return Host{}, err
	}
	log.Debug("fetched host", "id", hostID)

	return host, nil
}

func (c *DnscasterApiClient) CreateHost(ctx context.Context, host Host) (Host, error) {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: dnscasterHostPath}

	reqBody := UpsertHostRequest{Host: host}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return Host{}, NewDataError("marshal", "createHost body error", err)
	}

	resp, err := c.doRequest(
		ctx,
		http.MethodPost,
		u,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return Host{}, errors.Wrap(err, "failed to create DNS hosts from DNScaster")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	h, err := decodeResponse[Host](resp, dnscasterHostPath)
	if err != nil {
		return Host{}, err
	}
	log.Debug("created host", "id", h.ID)

	return h, nil
}

func (c *DnscasterApiClient) UpdateHost(ctx context.Context, host Host) (Host, error) {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: path.Join(dnscasterHostPath, host.ID)}

	reqBody := UpsertHostRequest{Host: host}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return Host{}, NewDataError("marshal", "updateHost body error", err)
	}

	resp, err := c.doRequest(
		ctx,
		http.MethodPut,
		u,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return Host{}, errors.Wrap(err, "failed to create DNS hosts from DNScaster")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	h, err := decodeResponse[Host](resp, dnscasterHostPath)
	if err != nil {
		return Host{}, err
	}
	log.Debug("updated host", "id", host.ID)

	return h, nil
}

func (c *DnscasterApiClient) DeleteHost(ctx context.Context, hostID string) error {
	u := url.URL{Scheme: "https", Host: dnscasterBaseUrl, Path: path.Join(dnscasterHostPath, hostID)}

	_, err := c.doRequest(
		ctx,
		http.MethodDelete,
		u,
		nil,
	)
	if err != nil {
		return errors.Wrap(err, "failed to delete DNS hosts from DNScaster")
	}
	log.Debug("deleted host", "id", hostID)

	return nil
}

func (c *DnscasterApiClient) doRequest(ctx context.Context, method string, url url.URL, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url.String(), body)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create HTTP request")
	}

	req.Header.Set("Authorization", "Bearer "+c.ApiKey)
	req.Header.Set("Accept", "application/json")
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, NewNetworkError(method, url.String(), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, c.handleErrorResponse(resp, method, url.String())
	}

	return resp, nil
}

// handleErrorResponse processes non-200 status codes and returns appropriate errors.
func (c *DnscasterApiClient) handleErrorResponse(resp *http.Response, method, path string) error {
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, errorBodyBufferSize))
	if err != nil {
		return NewDataError("read", "error response body", err)
	}

	var apiError DnscasterErrorResponse
	err = json.Unmarshal(bodyBytes, &apiError)
	if err != nil {
		return NewDataError("unmarshal", "API error response", err)
	}

	return NewAPIError(method, path, resp.StatusCode, apiError.Message, apiError.Errors)
}

func decodeResponse[T any](resp *http.Response, path string) (T, error) {
	var out T

	if resp.StatusCode == http.StatusNoContent {
		return out, nil
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, NewDataError("decode", path, err)
	}
	return out, nil
}
