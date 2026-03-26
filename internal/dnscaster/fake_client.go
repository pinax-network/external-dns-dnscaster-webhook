package dnscaster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Fake DNScasterClient used to do functional tests
type FakeDNScasterClient struct {
	*http.Client
	mu sync.Mutex

	Zones    []Zone
	Hosts    map[string][]Host  // key: zone_id
	Monitors map[string]Monitor // key: mon_id

	// Tracking IDs
	nextHostID    int
	nextMonitorID int

	// Optional hooks for test-specific assertions or failures.
	OnCreateMonitor func(mon Monitor) error
	OnCreateHost    func(host Host) error
	OnDeleteHost    func(hostID string) error
	OnDeleteMonitor func(monitorID string) error
}

func NewFakeDnscasterClient(config *DNScasterConnectionConfig, defaults *DNScasterDefaults) (*DNScasterApiClient, *FakeDNScasterClient, error) {
	log.Info("creating a new Fake Dnscaster API Client")

	fake := &FakeDNScasterClient{
		Hosts:         make(map[string][]Host),
		Monitors:      make(map[string]Monitor),
		nextHostID:    1,
		nextMonitorID: 1,
	}

	client := &DNScasterApiClient{
		DNScasterDefaults:         defaults,
		DNScasterConnectionConfig: config,
		Client:                    fake.HTTPClient(),
	}

	return client, fake, nil
}

func (f *FakeDNScasterClient) HTTPClient() *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return f.roundTrip(req)
		}),
	}
}

func (f *FakeDNScasterClient) WithZone(id, domain string) *FakeDNScasterClient {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Zones = append(f.Zones, Zone{
		ID:     id,
		Domain: domain,
	})
	return f
}

func (f *FakeDNScasterClient) WithHost(host Host) *FakeDNScasterClient {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Hosts[host.ZoneID] = append(f.Hosts[host.ZoneID], host)
	return f
}

func (f *FakeDNScasterClient) WithMonitor(m Monitor) *FakeDNScasterClient {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Monitors[m.ID] = m
	return f
}

func (f *FakeDNScasterClient) roundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch {

	// List Zones
	case req.Method == http.MethodGet && req.URL.Path == "/"+dnscasterZonePath:
		return f.json(http.StatusOK, map[string]any{
			"collection": f.Zones,
		})

	// List Hosts
	case req.Method == http.MethodGet && req.URL.Path == "/"+dnscasterHostPath:
		return f.handleListHosts(req)

	// Create Host
	case req.Method == http.MethodPost && req.URL.Path == "/"+dnscasterHostPath:
		return f.handleCreateHost(req)

	// Delete Host
	case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/"+dnscasterHostPath):
		return f.handleDeleteHost(req)

	// Get Monitor
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/"+dnscasterMonitorPath):
		return f.handleGetMonitor(req)

	// Create Monitor
	case req.Method == http.MethodPost && req.URL.Path == "/"+dnscasterMonitorPath:
		return f.handleCreateMonitor(req)

	// Delete Monitor
	case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, "/"+dnscasterMonitorPath):
		return f.handleDeleteMonitor(req)

	default:
		log.Fatal("unexpected request", "req.Method", req.Method, "req.URL.Path", req.URL.Path, "req.URL.RawQuery", req.URL.RawQuery)
		return nil, nil
	}
}

func (f *FakeDNScasterClient) handleListHosts(req *http.Request) (*http.Response, error) {
	zoneID := req.URL.Query().Get("zone_id")
	if zoneID == "" {
		return f.json(http.StatusBadRequest, map[string]any{
			"error": "missing zone_id",
		})
	}

	return f.json(http.StatusOK, map[string]any{
		"collection": f.Hosts[zoneID],
	})
}

func (f *FakeDNScasterClient) handleCreateHost(req *http.Request) (*http.Response, error) {
	var body HostEnvelope

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return f.json(http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("invalid json: %v", err),
		})
	}

	if f.OnCreateHost != nil {
		if err := f.OnCreateHost(body.Host); err != nil {
			return f.apiError(http.StatusBadRequest, err.Error())
		}
	}

	id := fmt.Sprintf("h-%d", f.nextHostID)
	f.nextHostID++

	host := body.Host
	host.ID = id

	f.Hosts[host.ZoneID] = append(f.Hosts[host.ZoneID], host)

	return f.json(http.StatusCreated, host)
}

func (f *FakeDNScasterClient) handleDeleteHost(req *http.Request) (*http.Response, error) {
	hostID := strings.TrimPrefix(req.URL.Path, "/"+dnscasterHostPath)

	if f.OnDeleteHost != nil {
		if err := f.OnDeleteHost(hostID); err != nil {
			return f.apiError(http.StatusInternalServerError, err.Error())
		}
	}

	for zoneID, hosts := range f.Hosts {
		for i, h := range hosts {
			if h.ID == hostID {
				f.Hosts[zoneID] = append(hosts[:i], hosts[i+1:]...)
				return &http.Response{
					StatusCode: http.StatusAccepted,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     make(http.Header),
				}, nil
			}
		}
	}

	return f.json(http.StatusNotFound, map[string]any{
		"error": "host not found",
	})
}

func (f *FakeDNScasterClient) handleGetMonitor(req *http.Request) (*http.Response, error) {
	monitorID := strings.TrimPrefix(req.URL.Path, "/"+dnscasterMonitorPath)

	if monitorID == "" {
		return f.json(http.StatusBadRequest, map[string]any{
			"error": "missing monitor_id",
		})
	}

	monitor, ok := f.Monitors[monitorID]
	if !ok {
		return f.apiError(http.StatusNotFound, "monitor not found")
	}

	return f.json(http.StatusOK, monitor)
}

func (f *FakeDNScasterClient) handleCreateMonitor(req *http.Request) (*http.Response, error) {
	var body MonitorEnvelope

	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		return f.json(http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("invalid json: %v", err),
		})
	}

	if f.OnCreateMonitor != nil {
		if err := f.OnCreateMonitor(body.Monitor); err != nil {
			return f.apiError(http.StatusBadRequest, err.Error())
		}
	}

	id := fmt.Sprintf("m-%d", f.nextMonitorID)
	f.nextMonitorID++

	mon := body.Monitor
	mon.ID = id

	f.Monitors[id] = mon

	return f.json(http.StatusCreated, mon)
}

func (f *FakeDNScasterClient) handleDeleteMonitor(req *http.Request) (*http.Response, error) {
	monitorID := strings.TrimPrefix(req.URL.Path, "/"+dnscasterMonitorPath)

	if f.OnDeleteMonitor != nil {
		if err := f.OnDeleteMonitor(monitorID); err != nil {
			return f.apiError(http.StatusInternalServerError, err.Error())
		}
	}

	if _, ok := f.Monitors[monitorID]; !ok {
		return f.json(http.StatusNotFound, map[string]any{
			"error": "monitor not found",
		})
	}

	delete(f.Monitors, monitorID)
	return &http.Response{
		StatusCode: http.StatusAccepted,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

func (f *FakeDNScasterClient) json(status int, v any) (*http.Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(b)),
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
	}, nil
}

func (f *FakeDNScasterClient) apiError(status int, message string) (*http.Response, error) {
	return f.json(status, DnscasterErrorResponse{
		Message: message,
		Errors:  []string{message},
	})
}
