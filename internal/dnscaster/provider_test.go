package dnscaster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Fake DNScaster provider used to do integration tests
type FakeDNScaster struct {
	t  *testing.T
	mu sync.Mutex

	calls []string

	NameserverSets []NameserverSet
	Zones          []Zone
	Hosts          map[string][]Host // key: zone_id
	Monitors       map[string]Monitor

	// Tracking IDs
	nextHostID    int
	nextMonitorID int

	// Optional hooks for test-specific assertions or failures.
	OnCreateMonitor func(mon Monitor) error
	OnCreateHost    func(host Host) error
	OnDeleteHost    func(hostID string) error
	OnDeleteMonitor func(monitorID string) error
}

// Create a FakeDNScaster provider
func NewFakeDNScaster(t *testing.T) *FakeDNScaster {
	t.Helper()

	return &FakeDNScaster{
		t:             t,
		Hosts:         make(map[string][]Host),  // key: zone_id
		Monitors:      make(map[string]Monitor), // key: monitor_id
		nextHostID:    1,
		nextMonitorID: 1,
	}
}

func (f *FakeDNScaster) HTTPClient() *http.Client {
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return f.roundTrip(req)
		}),
	}
}

// Get the list of calls made by the provider
func (f *FakeDNScaster) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *FakeDNScaster) WithNameserverSet(id, name string) *FakeDNScaster {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.NameserverSets = append(f.NameserverSets, NameserverSet{
		ID:   id,
		Name: name,
	})
	return f
}

func (f *FakeDNScaster) WithZone(id, domain string) *FakeDNScaster {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Zones = append(f.Zones, Zone{
		ID:     id,
		Domain: domain,
	})
	return f
}

func (f *FakeDNScaster) WithHost(host Host) *FakeDNScaster {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Hosts[host.ZoneID] = append(f.Hosts[host.ZoneID], host)
	return f
}

func (f *FakeDNScaster) WithMonitor(m Monitor) *FakeDNScaster {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Monitors[m.ID] = m
	return f
}

func (f *FakeDNScaster) roundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, req.Method+" "+req.URL.Path)

	switch {

	// List Nameserver Sets
	case req.Method == http.MethodGet && req.URL.Path == "/"+dnscasterNameserverSetsPath:
		return f.json(http.StatusOK, map[string]any{
			"collection": f.NameserverSets,
		})

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
		f.t.Fatalf("unexpected request: %s %s?%s", req.Method, req.URL.Path, req.URL.RawQuery)
		return nil, nil
	}
}

func (f *FakeDNScaster) handleListHosts(req *http.Request) (*http.Response, error) {
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

func (f *FakeDNScaster) handleCreateHost(req *http.Request) (*http.Response, error) {
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

func (f *FakeDNScaster) handleDeleteHost(req *http.Request) (*http.Response, error) {
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

func (f *FakeDNScaster) handleGetMonitor(req *http.Request) (*http.Response, error) {
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

func (f *FakeDNScaster) handleCreateMonitor(req *http.Request) (*http.Response, error) {
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

func (f *FakeDNScaster) handleDeleteMonitor(req *http.Request) (*http.Response, error) {
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

func (f *FakeDNScaster) json(status int, v any) (*http.Response, error) {
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

func (f *FakeDNScaster) apiError(status int, message string) (*http.Response, error) {
	return f.json(status, DnscasterErrorResponse{
		Message: message,
		Errors:  []string{message},
	})
}

func init() {
	log.Init()
}

func TestDefaultTTLUsesRecordValueWhenConfigured(t *testing.T) {
	t.Parallel()

	p := &DnscasterProvider{client: &DnscasterApiClient{DnscasterDefaults: &DnscasterDefaults{DefaultTTL: 300}}}
	record := endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(120), "1.2.3.4")

	if got := p.defaultTTL(record); got != 120 {
		t.Fatalf("expected configured TTL 120, got %d", got)
	}
}

func TestDefaultTTLFallsBackToProviderDefault(t *testing.T) {
	t.Parallel()

	p := &DnscasterProvider{client: &DnscasterApiClient{DnscasterDefaults: &DnscasterDefaults{DefaultTTL: 600}}}
	record := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")

	if got := p.defaultTTL(record); got != 600 {
		t.Fatalf("expected default TTL 600, got %d", got)
	}
}

func TestHostsForEndpointUsesFirstTargetAndComputedTTL(t *testing.T) {
	t.Parallel()

	p := &DnscasterProvider{client: &DnscasterApiClient{DnscasterDefaults: &DnscasterDefaults{DefaultTTL: 450}}}
	record := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4", "5.6.7.8")

	h := p.hostsForEndpoint(record)
	if h.Data != "1.2.3.4" {
		t.Fatalf("expected first target to be used, got %s", h.Data)
	}
	if h.TTL != 450 {
		t.Fatalf("expected default TTL to be used, got %d", h.TTL)
	}
}

func TestTrimHostnameFromFQDN(t *testing.T) {
	t.Parallel()

	p := &DnscasterProvider{domainFilter: endpoint.NewDomainFilter([]string{"example.com", ".deep.example.com", "exact.example.net"})}

	tests := []struct {
		name     string
		dnsName  string
		hostname string
		zone     string
	}{
		{name: "suffix filter", dnsName: "api.example.com", hostname: "api", zone: "example.com"},
		{name: "dot-prefixed filter", dnsName: "www.deep.example.com", hostname: "www", zone: "deep.example.com"},
		{name: "exact zone apex", dnsName: "exact.example.net", hostname: "", zone: "exact.example.net"},
		{name: "no match", dnsName: "unmanaged.org", hostname: "", zone: "unmanaged.org"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			record := endpoint.NewEndpoint(tc.dnsName, "A", "1.2.3.4")
			hostname, zone := p.trimHostnameFromFQDN(record)
			if hostname != tc.hostname || zone != tc.zone {
				t.Fatalf("got (%q,%q), want (%q,%q)", hostname, zone, tc.hostname, tc.zone)
			}
		})
	}
}

func TestCreateMonitorForEndpointSkipsUnsupportedTypesAndMissingFields(t *testing.T) {
	t.Parallel()

	fake := NewFakeDNScaster(t).
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com")

	fake.OnCreateMonitor = func(mon Monitor) error {
		t.Fatalf("CreateMonitor should not call API in this test")
		return nil
	}

	p := &DnscasterProvider{
		client: &DnscasterApiClient{
			DnscasterDefaults:         &DnscasterDefaults{DefaultTTL: 300},
			DnscasterConnectionConfig: &DnscasterConnectionConfig{ApiKey: "k"},
			Client:                    fake.HTTPClient(),
		},
		domainFilter: endpoint.NewDomainFilter([]string{"example.com"}),
	}

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{},
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("app.example.com", "CNAME", "target.example.com"),
			endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
			func() *endpoint.Endpoint {
				e := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
				e.SetProviderSpecificProperty(providerSpecificIPMonitorURI, "https")
				return e
			}(),
		},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateMonitorForEndpointCreatesMonitor(t *testing.T) {
	t.Parallel()

	fake := NewFakeDNScaster(t).
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com")

	fake.OnCreateMonitor = func(mon Monitor) error {
		if mon.URI != "https://1.2.3.4" {
			t.Fatalf("expected monitor.URI=https://1.2.3.4, got: %s", mon.URI)
		}
		if mon.TreatRedirects != "online" {
			t.Fatalf("expected monitor.TreatRedirects=online, got: %s", mon.TreatRedirects)
		}
		return nil
	}

	fake.OnCreateHost = func(host Host) error {
		if host.ZoneID != "z-1" {
			t.Fatalf("expected host.ZoneID=z-1, got: %s", host.ZoneID)
		}
		if host.Hostname != "app" {
			t.Fatalf("expected host.Hostname=app, got: %s", host.Hostname)
		}
		if host.IPMonitorID != "m-1" {
			t.Fatalf("expected host.IPMonitorID=m-1, got: %s", host.IPMonitorID)
		}

		return nil
	}

	p := &DnscasterProvider{
		client: &DnscasterApiClient{
			DnscasterDefaults:         &DnscasterDefaults{DefaultTTL: 300},
			DnscasterConnectionConfig: &DnscasterConnectionConfig{ApiKey: "k"},
			Client:                    fake.HTTPClient(),
		},
		domainFilter: endpoint.NewDomainFilter([]string{"example.com"}),
	}

	create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	create.SetProviderSpecificProperty(providerSpecificIPMonitorURI, "https")
	create.SetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects, "online")

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{},
		Create: []*endpoint.Endpoint{create},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotCalls := fake.Calls()
	wantCalls := []string{
		http.MethodGet + " /" + dnscasterNameserverSetsPath,
		http.MethodGet + " /" + dnscasterZonePath,
		http.MethodPost + " /" + dnscasterMonitorPath,
		http.MethodPost + " /" + dnscasterHostPath,
	}

	if len(gotCalls) != len(wantCalls) {
		t.Fatalf("unexpected number of calls: got=%d want=%d calls=%v", len(gotCalls), len(wantCalls), gotCalls)
	}
	for i := range wantCalls {
		if gotCalls[i] != wantCalls[i] {
			t.Fatalf("unexpected call at %d: got=%q want=%q all=%v", i, gotCalls[i], wantCalls[i], gotCalls)
		}
	}
}

func TestCreateMonitorForEndpointReturnsErrorWhenNoNameserverSets(t *testing.T) {
	t.Parallel()

	fake := NewFakeDNScaster(t)

	p := &DnscasterProvider{
		client: &DnscasterApiClient{
			DnscasterDefaults:         &DnscasterDefaults{DefaultTTL: 300},
			DnscasterConnectionConfig: &DnscasterConnectionConfig{ApiKey: "k"},
			Client:                    fake.HTTPClient(),
		},
		domainFilter: endpoint.NewDomainFilter([]string{"example.com"}),
	}

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{},
		Create: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")},
	}

	err := p.ApplyChanges(context.Background(), changes)
	if err == nil || !strings.Contains(err.Error(), "no nameserver sets available") {
		t.Fatalf("expected no nameserver sets error, got: %v", err)
	}
}

func TestRecordsFiltersManagedZonesAndBuildsEndpoints(t *testing.T) {
	t.Parallel()

	fake := NewFakeDNScaster(t).
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com").
		WithZone("z-2", "other.com").
		WithHost(Host{ID: "h-1", ZoneID: "z-1", FQDN: "api.example.com", Data: "1.2.3.4"}).
		WithHost(Host{ID: "h-2", ZoneID: "z-1", FQDN: "test.example.com", Data: "5.6.7.8", IPMonitorID: "m-1"}).
		WithMonitor(Monitor{NameserverSetID: "ns-1", ID: "m-1", URI: "https://5.6.7.8", TreatRedirects: "offline"}).
		WithHost(Host{ID: "h-3", ZoneID: "z-2", FQDN: "fail.other.com", Data: "1.2.3.4"})

	p := &DnscasterProvider{
		client: &DnscasterApiClient{
			DnscasterDefaults:         &DnscasterDefaults{DefaultTTL: 300},
			DnscasterConnectionConfig: &DnscasterConnectionConfig{ApiKey: "k"},
			Client:                    fake.HTTPClient(),
		},
		domainFilter: endpoint.NewDomainFilter([]string{"example.com"}),
	}

	records, err := p.Records(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []*endpoint.Endpoint{
		endpoint.NewEndpoint("api.example.com", "A", "1.2.3.4"),
		func() *endpoint.Endpoint {
			e := endpoint.NewEndpoint("test.example.com", "A", "5.6.7.8")
			e.SetProviderSpecificProperty(providerSpecificIPMonitorURI, "https")
			e.SetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects, "offline")
			return e
		}(),
	}

	if len(expected) != len(records) {
		t.Fatalf("expected %d records, got %d", len(expected), len(records))
	}
	for i, r := range records {
		if r.DNSName != expected[i].DNSName {
			t.Fatalf("expected DNSName: %+v ,got: %+v", expected[i].DNSName, r.DNSName)
		}

		if r.Targets[0] != expected[i].Targets[0] {
			t.Fatalf("expected target: %+v ,got: %+v", expected[i].Targets[0], r.Targets[0])
		}

		uri, _ := r.GetProviderSpecificProperty(providerSpecificIPMonitorURI)
		expectedURI, _ := expected[i].GetProviderSpecificProperty(providerSpecificIPMonitorURI)

		if uri != expectedURI {
			t.Fatalf("expected monitor URI scheme: %q, got: %q", expectedURI, uri)
		}

		redirects, _ := r.GetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects)
		expectedRedirects, _ := expected[i].GetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects)

		if redirects != expectedRedirects {
			t.Fatalf("expected monitor TreatRedirects: %q, got: %q", expectedRedirects, redirects)
		}

	}
}

func TestApplyChangesDeletesAndCreatesRecords(t *testing.T) {
	t.Parallel()

	fake := NewFakeDNScaster(t).
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com").
		WithHost(Host{
			ID:          "h-old",
			ZoneID:      "z-1",
			FQDN:        "app.example.com",
			DNSType:     "A",
			Data:        "1.2.3.4",
			IPMonitorID: "m-old",
		}).
		WithMonitor(Monitor{ID: "m-old"})

	fake.OnCreateHost = func(req Host) error {
		if req.ZoneID != "z-1" {
			t.Fatalf("expected zone_id=z-1, got %q", req.ZoneID)
		}
		if req.Hostname != "new" {
			t.Fatalf("expected hostname=new, got %q", req.Hostname)
		}
		if req.IPMonitorID != "m-1" {
			t.Fatalf("expected ip_monitor_id=m-1, got %q", req.IPMonitorID)
		}
		return nil
	}

	fake.OnCreateMonitor = func(req Monitor) error {
		if req.NameserverSetID != "ns-1" {
			t.Fatalf("expected nameserver_set_id=ns-1, got %q", req.NameserverSetID)
		}
		if req.URI != "https://5.6.7.8" {
			t.Fatalf("expected uri=https://5.6.7.8, got %q", req.URI)
		}
		if req.TreatRedirects != "online" {
			t.Fatalf("expected treat_redirects=online, got %q", req.TreatRedirects)
		}
		return nil
	}

	client := &DnscasterApiClient{
		DnscasterDefaults:         &DnscasterDefaults{DefaultTTL: 300},
		DnscasterConnectionConfig: &DnscasterConnectionConfig{ApiKey: "k"},
		Client:                    fake.HTTPClient(),
	}

	p := &DnscasterProvider{
		client:       client,
		domainFilter: endpoint.NewDomainFilter([]string{"example.com"}),
	}

	create := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
	create.SetProviderSpecificProperty(providerSpecificIPMonitorURI, "https")
	create.SetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects, "online")

	changes := &plan.Changes{
		Delete: []*endpoint.Endpoint{
			endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
		},
		Create: []*endpoint.Endpoint{create},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotCalls := fake.Calls()
	wantCalls := []string{
		http.MethodGet + " /" + dnscasterNameserverSetsPath,
		http.MethodGet + " /" + dnscasterZonePath,
		http.MethodGet + " /" + dnscasterHostPath,
		http.MethodDelete + " /" + dnscasterHostPath + "h-old",
		http.MethodDelete + " /" + dnscasterMonitorPath + "m-old",
		http.MethodPost + " /" + dnscasterMonitorPath,
		http.MethodPost + " /" + dnscasterHostPath,
	}

	if len(gotCalls) != len(wantCalls) {
		t.Fatalf("unexpected number of calls: got=%d want=%d calls=%v", len(gotCalls), len(wantCalls), gotCalls)
	}
	for i := range wantCalls {
		if gotCalls[i] != wantCalls[i] {
			t.Fatalf("unexpected call at %d: got=%q want=%q all=%v", i, gotCalls[i], wantCalls[i], gotCalls)
		}
	}
}

func TestRecordsReturnsErrorWhenMonitorDoesNotExist(t *testing.T) {
	t.Parallel()

	fake := NewFakeDNScaster(t).
		WithZone("z-1", "example.com").
		WithHost(Host{ID: "h-1", ZoneID: "z-1", FQDN: "api.example.com", Data: "1.2.3.4", IPMonitorID: "m-missing"})

	p := &DnscasterProvider{
		client: &DnscasterApiClient{
			DnscasterDefaults:         &DnscasterDefaults{DefaultTTL: 300},
			DnscasterConnectionConfig: &DnscasterConnectionConfig{ApiKey: "k"},
			Client:                    fake.HTTPClient(),
		},
		domainFilter: endpoint.NewDomainFilter([]string{"example.com"}),
	}

	_, err := p.Records(context.Background())
	if err == nil {
		t.Fatal("expected an error when monitor cannot be fetched")
	}
	if !strings.Contains(err.Error(), "monitor not found") {
		t.Fatalf("expected monitor not found error, got: %v", err)
	}
}
