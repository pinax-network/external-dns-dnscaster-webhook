package dnscaster

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
)

func init() {
	log.Init()
}

// testProvider returns a provider for planning changes. Its client has no
// HTTP client, so anything that would reach the API fails.
func testProvider(domainFilter ...string) *DNScasterProvider {
	return &DNScasterProvider{
		client: &DNScasterApiClient{
			DNScasterDefaults: &DNScasterDefaults{DefaultTTL: 600},
			DNScasterConnectionConfig: &DNScasterConnectionConfig{
				OwnerID:         "controller-1",
				NameserverSetID: "ns-1",
			},
		},
		domainFilter: endpoint.NewDomainFilterWithExclusions(domainFilter, nil),
	}
}

var testZones = []Zone{{ID: "z-1", Domain: "example.com"}}

// planCreate plans the creation of a single record.
func planCreate(t *testing.T, p *DNScasterProvider, zones []Zone, record *endpoint.Endpoint) hostCreate {
	t.Helper()

	cs, err := p.newChangeSet(nil, []*endpoint.Endpoint{record}, zones, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cs.deletes) != 0 || len(cs.creates) != 1 {
		t.Fatalf("expected a single create, got %d deletes and %d creates", len(cs.deletes), len(cs.creates))
	}
	return cs.creates[0]
}

func TestPlanCreateTTL(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		record *endpoint.Endpoint
		want   int64
	}{
		"configured on the record": {endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(120), "1.2.3.4"), 120},
		"provider default":         {endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"), 600},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			create := planCreate(t, testProvider("example.com"), testZones, tc.record)
			if create.host.TTL != tc.want {
				t.Fatalf("expected TTL %d, got %d", tc.want, create.host.TTL)
			}
		})
	}
}

func TestPlanCreateUsesFirstTarget(t *testing.T) {
	t.Parallel()

	record := endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(450), "1.2.3.4", "5.6.7.8")

	create := planCreate(t, testProvider("example.com"), testZones, record)
	if create.host.Data != "1.2.3.4" {
		t.Fatalf("expected first target 1.2.3.4, got %s", create.host.Data)
	}
}

func TestPlanCreateResolvesZoneAndHostname(t *testing.T) {
	t.Parallel()

	p := testProvider("example.com", ".deep.example.com", "exact.example.net")
	zones := []Zone{
		{ID: "z-1", Domain: "example.com"},
		{ID: "z-2", Domain: "deep.example.com"},
		{ID: "z-3", Domain: "exact.example.net"},
	}

	for name, tc := range map[string]struct {
		fqdn, hostname, zoneID string
	}{
		"suffix filter":      {"api.example.com", "api", "z-1"},
		"dot-prefixed":       {"www.deep.example.com", "www", "z-2"},
		"exact-zone apex":    {"exact.example.net", "", "z-3"},
		"no matching filter": {"unmanaged.org", "", ""}, // DNScaster rejects a host without a zone
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			create := planCreate(t, p, zones, endpoint.NewEndpoint(tc.fqdn, "A", "1.2.3.4"))
			if create.host.FQDN != tc.fqdn {
				t.Fatalf("expected fqdn %q, got %q", tc.fqdn, create.host.FQDN)
			}
			if create.host.Hostname != tc.hostname {
				t.Fatalf("expected hostname %q, got %q", tc.hostname, create.host.Hostname)
			}
			if create.host.ZoneID != tc.zoneID {
				t.Fatalf("expected zone_id %q, got %q", tc.zoneID, create.host.ZoneID)
			}
		})
	}
}

func TestPlanCreateWithoutDomainFilter(t *testing.T) {
	t.Parallel()

	p := testProvider()
	zones := []Zone{
		{ID: "z-1", Domain: "api.example.com"},
		{ID: "z-2", Domain: "api.other.com"},
	}

	cs, err := p.newChangeSet(nil, []*endpoint.Endpoint{
		endpoint.NewEndpoint("api.example.com", "A", "1.2.3.4"),
		endpoint.NewEndpoint("api.other.com", "A", "1.2.3.4"),
	}, zones, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got []string
	for _, create := range cs.creates {
		got = append(got, create.host.ZoneID+" "+create.host.FQDN)
	}
	if want := []string{"z-1 api.example.com", "z-2 api.other.com"}; !slices.Equal(got, want) {
		t.Fatalf("unexpected hosts: got %v, want %v", got, want)
	}
}

func TestPlanCreateSetsProperties(t *testing.T) {
	t.Parallel()

	record := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	record.SetIdentifier = "blue"
	record.SetProviderSpecificProperty(ProviderSpecificLabelPrefix+"registry~1name", "mainnet")

	create := planCreate(t, testProvider("example.com"), testZones, record)

	// The owner property is what ListHosts filters on: a host without it is
	// invisible to Records.
	want := map[string]string{
		ProviderMetadataOwnerID:       "controller-1",
		ProviderMetadataSetIdentifier: "blue",
		"registry/name":               "mainnet",
	}
	for key, value := range want {
		if got := create.host.Properties[key]; got != value {
			t.Fatalf("expected property %s=%q, got %q (all: %v)", key, value, got, create.host.Properties)
		}
	}
}

func TestPlanCreateMonitor(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		annotations map[string]string
		want        Monitor
	}{
		"scheme only": {
			annotations: map[string]string{ProviderSpecificIPMonitorURI: "ping"},
			want:        Monitor{URI: "ping://5.6.7.8", Hostname: "new.example.com"},
		},
		"path without host": {
			annotations: map[string]string{ProviderSpecificIPMonitorURI: "https:/health"},
			want:        Monitor{URI: "https://5.6.7.8/health", Hostname: "new.example.com"},
		},
		"full URI": {
			annotations: map[string]string{
				ProviderSpecificIPMonitorURI:            "https://1.1.1.1/health",
				ProviderSpecificIPMonitorTreatRedirects: "offline",
			},
			want: Monitor{URI: "https://1.1.1.1/health", Hostname: "new.example.com", TreatRedirects: "offline"},
		},
		"different hostname": {
			annotations: map[string]string{
				ProviderSpecificIPMonitorURI:            "https://1.1.1.1/health",
				ProviderSpecificIPMonitorHostname:       "api.other.com",
				ProviderSpecificIPMonitorTreatRedirects: "offline",
			},
			want: Monitor{URI: "https://1.1.1.1/health", Hostname: "api.other.com", TreatRedirects: "offline"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			record := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
			for key, value := range tc.annotations {
				record.SetProviderSpecificProperty(key, value)
			}

			create := planCreate(t, testProvider("example.com"), testZones, record)
			if create.monitor == nil {
				t.Fatal("expected a monitor")
			}

			got := *create.monitor
			if got.URI != tc.want.URI || got.Hostname != tc.want.Hostname || got.TreatRedirects != tc.want.TreatRedirects {
				t.Fatalf("unexpected monitor: got uri=%q hostname=%q treat_redirects=%q, want uri=%q hostname=%q treat_redirects=%q",
					got.URI, got.Hostname, got.TreatRedirects, tc.want.URI, tc.want.Hostname, tc.want.TreatRedirects)
			}
			if got.Name != "new.example.com" || got.NameserverSetID != "ns-1" {
				t.Fatalf("unexpected monitor name=%q nameserver_set_id=%q", got.Name, got.NameserverSetID)
			}
			if create.host.ZoneID != "z-1" || create.host.Hostname != "new" {
				t.Fatalf("unexpected host zone_id=%q hostname=%q", create.host.ZoneID, create.host.Hostname)
			}
		})
	}
}

func TestPlanCreateWithoutMonitor(t *testing.T) {
	t.Parallel()

	cname := endpoint.NewEndpoint("app.example.com", "CNAME", "target.example.com")
	cname.SetProviderSpecificProperty(ProviderSpecificIPMonitorURI, "https")

	for name, record := range map[string]*endpoint.Endpoint{
		"no monitor annotation": endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
		"unsupported type":      cname,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if create := planCreate(t, testProvider("example.com"), testZones, record); create.monitor != nil {
				t.Fatalf("unexpected monitor: %+v", *create.monitor)
			}
		})
	}
}

func TestPlanDeleteResolvesExistingHosts(t *testing.T) {
	t.Parallel()

	hosts := []Host{
		{ID: "h-a", FQDN: "app.example.com", DNSType: "A", Data: "1.2.3.4", IPMonitorID: "m-a"},
		{ID: "h-b", FQDN: "app.example.com", DNSType: "A", Data: "5.6.7.8"},
		{ID: "h-txt", FQDN: "app.example.com", DNSType: "TXT", Data: "heritage=external-dns"},
	}

	cs, err := testProvider("example.com").newChangeSet([]*endpoint.Endpoint{
		endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
		endpoint.NewEndpoint("app.example.com", "TXT", `"heritage=external-dns"`),
		endpoint.NewEndpoint("gone.example.com", "A", "1.2.3.4"),
	}, nil, nil, hosts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The record without a host is skipped: there is nothing to delete.
	if len(cs.deletes) != 2 || cs.deletes[0].ID != "h-a" || cs.deletes[1].ID != "h-txt" {
		t.Fatalf("unexpected deletes: %+v", cs.deletes)
	}
	if cs.deletes[0].IPMonitorID != "m-a" {
		t.Fatalf("expected the host's monitor to be deleted with it, got %q", cs.deletes[0].IPMonitorID)
	}
}

func TestPlanRejectsRecordsWithoutTarget(t *testing.T) {
	t.Parallel()

	noTarget := []*endpoint.Endpoint{endpoint.NewEndpoint("no-target.example.com", "A")}

	for name, changes := range map[string]*plan.Changes{
		"create": {Create: noTarget},
		"delete": {Delete: noTarget},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := testProvider("example.com").newChangeSet(slices.Concat(changes.UpdateOld, changes.Delete), changes.Create, testZones, nil)
			if err == nil || !strings.Contains(err.Error(), "no target") {
				t.Fatalf("expected a no target error, got: %v", err)
			}
		})
	}
}

func TestRecordsForHosts(t *testing.T) {
	t.Parallel()

	hosts := []Host{
		{
			ID: "h-1", FQDN: "api.example.com", DNSType: "A", Data: "1.2.3.4", TTL: 300, IPMonitorID: "m-1",
			Properties: map[string]string{
				ProviderMetadataOwnerID:                 "controller-1",
				ProviderMetadataSetIdentifier:           "blue",
				ProviderSpecificIPMonitorURI:            "https:/health",
				ProviderSpecificIPMonitorTreatRedirects: "offline",
				"registry/name":                         "mainnet",
			},
		},
		{ID: "h-2", FQDN: "app.other.com", DNSType: "A", Data: "1.2.3.4"},
	}

	records := testProvider("example.com").recordsForHosts(hosts)
	if len(records) != 1 {
		t.Fatalf("expected only the record in the managed domain, got %d", len(records))
	}

	record := records[0]
	if record.DNSName != "api.example.com" || record.RecordType != "A" || record.RecordTTL != 300 || !slices.Equal(record.Targets, endpoint.Targets{"1.2.3.4"}) {
		t.Fatalf("unexpected record: %v", record)
	}
	if record.SetIdentifier != "blue" {
		t.Fatalf("expected set identifier blue, got %q", record.SetIdentifier)
	}

	want := map[string]string{
		ProviderSpecificIPMonitorURI:                   "https:/health",
		ProviderSpecificIPMonitorTreatRedirects:        "offline",
		ProviderSpecificLabelPrefix + "registry~1name": "mainnet",
	}
	for name, value := range want {
		if got, ok := record.GetProviderSpecificProperty(name); !ok || got != value {
			t.Fatalf("expected provider-specific %s=%q, got %q (all: %v)", name, value, got, record.ProviderSpecific)
		}
	}
	if len(record.ProviderSpecific) != len(want) {
		t.Fatalf("unexpected provider-specific properties: %v", record.ProviderSpecific)
	}
}

// stubAPI answers each request with a canned response for its method and path,
// and records the requests it gets. It keeps no state between requests.
type stubAPI struct {
	t         *testing.T
	responses map[string]*http.Response
	requests  []string
	bodies    map[string]string
}

func newStubAPI(t *testing.T, responses map[string]*http.Response) *stubAPI {
	return &stubAPI{t: t, responses: responses, bodies: make(map[string]string)}
}

func (s *stubAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	call := req.Method + " " + req.URL.Path
	s.requests = append(s.requests, call)

	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		s.bodies[call] = string(body)
	}

	resp, ok := s.responses[call]
	if !ok {
		s.t.Errorf("unexpected request: %s", call)
		return jsonResponse(http.StatusNotFound, "", nil), nil
	}
	return resp, nil
}

func (s *stubAPI) provider(dryRun bool) *DNScasterProvider {
	p := testProvider("example.com")
	p.client.Client = &http.Client{Transport: s}
	p.dryRun = dryRun
	return p
}

func TestApplyChangesOnlyReadsInDryRun(t *testing.T) {
	t.Parallel()

	create := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
	create.SetProviderSpecificProperty(ProviderSpecificIPMonitorURI, "https")

	changes := &plan.Changes{
		Create:    []*endpoint.Endpoint{create},
		UpdateOld: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")},
		UpdateNew: []*endpoint.Endpoint{endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(60), "1.2.3.4")},
	}

	reads := func() map[string]*http.Response {
		return map[string]*http.Response{
			"GET /v1/hosts/": jsonResponse(http.StatusOK, `{"collection":[{"id":"h-1","fqdn":"app.example.com","dns_type":"A","data":"1.2.3.4","ip_monitor_id":"m-1"}],"more_results":false}`, nil),
			"GET /v1/zones/": jsonResponse(http.StatusOK, `{"collection":[{"id":"z-1","domain":"example.com"}],"more_results":false}`, nil),
		}
	}

	t.Run("dry run", func(t *testing.T) {
		t.Parallel()

		api := newStubAPI(t, reads())
		if err := api.provider(true).ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if want := []string{"GET /v1/hosts/", "GET /v1/zones/"}; !slices.Equal(api.requests, want) {
			t.Fatalf("unexpected requests: got %v, want %v", api.requests, want)
		}
	})

	t.Run("real run", func(t *testing.T) {
		t.Parallel()

		responses := reads()
		responses["DELETE /v1/hosts/h-1"] = jsonResponse(http.StatusAccepted, "", nil)
		responses["DELETE /v1/ip_monitors/m-1"] = jsonResponse(http.StatusAccepted, "", nil)
		responses["POST /v1/ip_monitors/"] = jsonResponse(http.StatusCreated, `{"id":"m-2"}`, nil)
		responses["POST /v1/hosts/"] = jsonResponse(http.StatusCreated, `{"id":"h-2"}`, nil)

		api := newStubAPI(t, responses)
		if err := api.provider(false).ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []string{
			"GET /v1/hosts/", "GET /v1/zones/",
			// The update's old host goes first, and its monitor after it.
			"DELETE /v1/hosts/h-1", "DELETE /v1/ip_monitors/m-1",
			"POST /v1/ip_monitors/", "POST /v1/hosts/",
			"POST /v1/hosts/",
		}
		if !slices.Equal(api.requests, want) {
			t.Fatalf("unexpected requests: got %v, want %v", api.requests, want)
		}
	})
}

func TestExecuteLinksMonitorToHost(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, map[string]*http.Response{
		"POST /v1/ip_monitors/": jsonResponse(http.StatusCreated, `{"id":"m-new"}`, nil),
		"POST /v1/hosts/":       jsonResponse(http.StatusCreated, `{"id":"h-new"}`, nil),
	})

	cs := changeSet{creates: []hostCreate{{
		host:    Host{FQDN: "new.example.com", DNSType: "A", Data: "5.6.7.8"},
		monitor: &Monitor{Name: "new.example.com"},
	}}}
	if err := api.provider(false).execute(context.Background(), cs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := []string{"POST /v1/ip_monitors/", "POST /v1/hosts/"}; !slices.Equal(api.requests, want) {
		t.Fatalf("unexpected requests: got %v, want %v", api.requests, want)
	}
	if body := api.bodies["POST /v1/hosts/"]; !strings.Contains(body, `"ip_monitor_id":"m-new"`) {
		t.Fatalf("expected the host to use the new monitor, got body: %s", body)
	}
}

func TestExecuteRemovesMonitorWhenHostCreateFails(t *testing.T) {
	t.Parallel()

	api := newStubAPI(t, map[string]*http.Response{
		"POST /v1/ip_monitors/":        jsonResponse(http.StatusCreated, `{"id":"m-new"}`, nil),
		"POST /v1/hosts/":              jsonResponse(http.StatusUnprocessableEntity, `{"message":"Validation failed","errors":["Zone is required."]}`, nil),
		"DELETE /v1/ip_monitors/m-new": jsonResponse(http.StatusAccepted, "", nil),
	})

	cs := changeSet{creates: []hostCreate{{
		host:    Host{FQDN: "new.example.com", DNSType: "A", Data: "5.6.7.8"},
		monitor: &Monitor{Name: "new.example.com"},
	}}}
	if err := api.provider(false).execute(context.Background(), cs); err == nil {
		t.Fatal("expected the host create error")
	}

	if want := []string{"POST /v1/ip_monitors/", "POST /v1/hosts/", "DELETE /v1/ip_monitors/m-new"}; !slices.Equal(api.requests, want) {
		t.Fatalf("unexpected requests: got %v, want %v", api.requests, want)
	}
}
