package dnscaster_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	env "github.com/caarlos0/env/v11"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/dnscaster"
	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/dnsprovider"
	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

func init() {
	os.Setenv("DOMAIN_FILTER", "example.com")
	os.Setenv("DNSCASTER_API_KEY", "k")
	os.Setenv("DNSCASTER_DEFAULT_TTL", "600")
	os.Setenv("LOG_LEVEL", "error")

	log.Init()
}

func TestDefaultTTLUsesRecordValueWhenConfigured(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com")

	fake.OnCreateHost = func(host dnscaster.Host) error {
		if host.TTL != 120 {
			t.Fatalf("expected configured TTL 120, got %d", host.TTL)
		}
		return nil
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(120), "1.2.3.4"),
		},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultTTLFallsBackToProviderDefault(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com")

	fake.OnCreateHost = func(host dnscaster.Host) error {
		ttl, err := strconv.ParseInt(os.Getenv("DNSCASTER_DEFAULT_TTL"), 10, 64)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if host.TTL != ttl {
			t.Fatalf("expected configured TTL: %d, got: %d", ttl, host.TTL)
		}
		return nil
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
		},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHostsForEndpointUsesFirstTargetAndComputedTTL(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com")

	fake.OnCreateHost = func(host dnscaster.Host) error {
		if host.Data != "1.2.3.4" {
			t.Fatalf("expected first target to be 1.2.3.4, got: %s", host.Data)
		}
		if host.TTL != 450 {
			t.Fatalf("expected configured TTL: %d, got: %d", 450, host.TTL)
		}
		return nil
	}

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(450), "1.2.3.4", "5.6.7.8"),
		},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTrimHostnameFromFQDN(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (provider.Provider, *dnscaster.FakeDNScasterClient) {
		t.Helper()

		config := configuration.Init()
		config.DomainFilter = []string{"example.com", ".deep.example.com", "exact.example.net"}

		connConfig := &dnscaster.DNScasterConnectionConfig{}
		if err := env.Parse(connConfig); err != nil {
			t.Fatalf("reading dnscaster connection config failed: %v", err)
		}

		defaults := &dnscaster.DNScasterDefaults{}
		if err := env.Parse(defaults); err != nil {
			t.Fatalf("reading dnscaster defaults failed: %v", err)
		}

		client, fake, err := dnscaster.NewFakeDnscasterClient(connConfig, defaults)
		if err != nil {
			t.Fatalf("creating fake dnscaster client failed: %v", err)
		}

		p, err := dnsprovider.InitWithClient(config, client)
		if err != nil {
			t.Fatalf("failed to init provider: %v", err)
		}

		fake.
			WithNameserverSet("ns-1", "default").
			WithZone("z-1", "example.com").
			WithZone("z-2", "deep.example.com").
			WithZone("z-3", "exact.example.net")

		return p, fake
	}

	t.Run("testing suffix filter", func(t *testing.T) {
		t.Parallel()

		p, fake := setup(t)

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.Hostname != "api" {
				t.Fatalf("expected host.Hostname: api, got: %s", host.Hostname)
			}
			if host.FQDN != "api.example.com" {
				t.Fatalf("expected host.FQDN: api.example.com, got: %s", host.FQDN)
			}
			if host.ZoneID != "z-1" {
				t.Fatalf("expected host.ZoneID: z-1, got: %s", host.ZoneID)
			}
			return nil
		}
		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{
				endpoint.NewEndpoint("api.example.com", "A", "1.2.3.4"),
			},
		}
		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("testing dot-prefixed filter", func(t *testing.T) {
		t.Parallel()

		p, fake := setup(t)

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.Hostname != "www" {
				t.Fatalf("expected host.Hostname: www, got: %s", host.Hostname)
			}
			if host.FQDN != "www.deep.example.com" {
				t.Fatalf("expected host.FQDN: www.deep.example.com, got: %s", host.FQDN)
			}
			if host.ZoneID != "z-2" {
				t.Fatalf("expected host.ZoneID: z-2, got: %s", host.ZoneID)
			}
			return nil
		}
		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{
				endpoint.NewEndpoint("www.deep.example.com", "A", "1.2.3.4"),
			},
		}
		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("testing exact-zone apex filter", func(t *testing.T) {
		t.Parallel()

		p, fake := setup(t)

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.Hostname != "" {
				t.Fatalf("expected host.Hostname: '', got: %s", host.Hostname)
			}
			if host.FQDN != "exact.example.net" {
				t.Fatalf("expected host.FQDN: exact.example.net, got: %s", host.FQDN)
			}
			if host.ZoneID != "z-3" {
				t.Fatalf("expected host.ZoneID: z-3, got: %s", host.ZoneID)
			}
			return nil
		}
		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{
				endpoint.NewEndpoint("exact.example.net", "A", "1.2.3.4"),
			},
		}
		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("testing no match zone filter", func(t *testing.T) {
		t.Parallel()

		p, fake := setup(t)

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.ZoneID != "" {
				// This would actually raise an error on DNScaster's side without a zone_id
				t.Fatalf("unexpected host.ZoneID, got: %v", host.ZoneID)
			}
			return nil
		}
		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{
				endpoint.NewEndpoint("unmanaged.org", "A", "1.2.3.4"),
			},
		}
		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestProviderWithoutDomainFilter(t *testing.T) {
	t.Parallel()

	config := configuration.Init()
	config.DomainFilter = []string{}

	connConfig := &dnscaster.DNScasterConnectionConfig{}
	if err := env.Parse(connConfig); err != nil {
		t.Fatalf("reading dnscaster connection config failed: %v", err)
	}

	defaults := &dnscaster.DNScasterDefaults{}
	if err := env.Parse(defaults); err != nil {
		t.Fatalf("reading dnscaster defaults failed: %v", err)
	}

	client, fake, err := dnscaster.NewFakeDnscasterClient(connConfig, defaults)
	if err != nil {
		t.Fatalf("creating fake dnscaster client failed: %v", err)
	}

	p, err := dnsprovider.InitWithClient(config, client)
	if err != nil {
		t.Fatalf("failed to init provider: %v", err)
	}

	fake.
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "api.example.com").
		WithZone("z-2", "api.other.com")

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{
			endpoint.NewEndpoint("api.example.com", "A", "1.2.3.4"),
			endpoint.NewEndpoint("api.other.com", "A", "1.2.3.4"),
		},
	}
	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fake.Hosts["z-1"][0].FQDN != "api.example.com" {
		t.Fatalf("expected FQDN: api.example.com, got: %s", fake.Hosts["z-1"][0].FQDN)
	}
	if fake.Hosts["z-2"][0].FQDN != "api.other.com" {
		t.Fatalf("expected FQDN: api.other.com, got: %s", fake.Hosts["z-2"][0].FQDN)
	}
}

func TestProviderRecords(t *testing.T) {
	t.Parallel()

	t.Run("should return nothing when no zone exist", func(t *testing.T) {
		t.Parallel()
		p, _ := newTestProvider(t)

		records, err := p.Records(context.Background())
		if err != nil {
			t.Fatalf("unexpected error, got: %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("expected no records, got: %d", len(records))
		}
	})

	t.Run("should return only records from managed zones", func(t *testing.T) {
		p, fake := newTestProvider(t)

		fake.
			WithZone("z-1", "example.com").
			WithZone("z-2", "other.com").
			WithHost(dnscaster.Host{ZoneID: "z-1", ID: "h-1"}).
			WithHost(dnscaster.Host{ZoneID: "z-2", ID: "h-2"}).
			WithHost(dnscaster.Host{ZoneID: "z-2", ID: "h-3"})

		records, err := p.Records(context.Background())
		if err != nil {
			t.Fatalf("unexpected error, got: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("expected 1 records, got: %d", len(records))
		}
	})

	t.Run("should return records with provider annotations", func(t *testing.T) {
		p, fake := newTestProvider(t)

		fake.
			WithNameserverSet("ns-1", "default").
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{ZoneID: "z-1", ID: "h-1", IPMonitorID: "m-1", Data: "1.2.3.4", Hostname: "api", FQDN: "api.example.com"}).
			WithMonitor(dnscaster.Monitor{NameserverSetID: "ns-1", ID: "m-1", URI: "ping://1.2.3.4", TreatRedirects: "offline"})

		records, err := p.Records(context.Background())
		if err != nil {
			t.Fatalf("unexpected error, got: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("expected 1 records, got: %d", len(records))
		}

		uri, _ := records[0].GetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI)
		if uri != "ping" {
			t.Fatalf("expected provider annotation %s=ping, got: %s", dnscaster.ProviderSpecificIPMonitorURI, uri)
		}
		redirects, _ := records[0].GetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects)
		if redirects != "offline" {
			t.Fatalf("expected provider annotation %s=offline, got: %s", dnscaster.ProviderSpecificIPMonitorTreatRedirects, redirects)
		}
	})

	t.Run("should return error when zoneID doesn't exist", func(t *testing.T) {
		p, fake := newTestProvider(t)

		fake.
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{ZoneID: "z-fake", ID: "h-1"})

		records, err := p.Records(context.Background())
		if err != nil {
			t.Fatalf("unexpected error, got: %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("expected 0 records, got: %d", len(records))
		}
	})

	t.Run("should return error when monitor doesn't exist", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProvider(t)

		fake.
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{ID: "h-1", ZoneID: "z-1", FQDN: "api.example.com", Data: "1.2.3.4", IPMonitorID: "m-missing"})

		_, err := p.Records(context.Background())
		if err == nil {
			t.Fatal("expected an error when monitor cannot be fetched")
		}
		if !strings.Contains(err.Error(), "monitor not found") {
			t.Fatalf("expected monitor not found error, got: %v", err)
		}
	})
}

func TestProviderApplyChanges(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com").
		WithHost(dnscaster.Host{
			ID:          "h-old",
			ZoneID:      "z-1",
			FQDN:        "app.example.com",
			DNSType:     "A",
			Data:        "1.2.3.4",
			IPMonitorID: "m-old",
		}).
		WithMonitor(dnscaster.Monitor{ID: "m-old"})

	fake.OnCreateHost = func(req dnscaster.Host) error {
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
	fake.OnDeleteHost = func(hostID string) error {
		if hostID != "h-old" {
			t.Fatalf("expected hostID=h-old, got %s", hostID)
		}
		return nil
	}

	fake.OnCreateMonitor = func(req dnscaster.Monitor) error {
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
	fake.OnDeleteMonitor = func(monitorID string) error {
		if monitorID != "m-old" {
			t.Fatalf("expected monitorID=m-old, got %s", monitorID)
		}
		return nil
	}

	t.Run("reconcile records by deleting old ones and create new", func(t *testing.T) {
		t.Parallel()

		create := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
		create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https")
		create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects, "online")

		changes := &plan.Changes{
			Delete: []*endpoint.Endpoint{
				endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
			},
			Create: []*endpoint.Endpoint{create},
		}

		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("should error when creating record with no target", func(t *testing.T) {
		t.Parallel()

		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{endpoint.NewEndpoint("no-target.example.com", "A")},
		}

		err := p.ApplyChanges(context.Background(), changes)
		if err == nil {
			t.Fatal("expected an error when no targets on record are set")
		}
		if !strings.Contains(err.Error(), "no target") {
			t.Fatalf("expected no target error, got: %v", err)
		}
	})

	t.Run("should error when deleting record with no target", func(t *testing.T) {
		t.Parallel()

		changes := &plan.Changes{
			Delete: []*endpoint.Endpoint{endpoint.NewEndpoint("no-target.example.com", "A")},
		}

		err := p.ApplyChanges(context.Background(), changes)
		if err == nil {
			t.Fatal("expected an error when no targets on record are set")
		}
		if !strings.Contains(err.Error(), "no target") {
			t.Fatalf("expected no target error, got: %v", err)
		}
	})
}

func TestProviderApplyChangesNoMonitors(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithNameserverSet("ns-1", "default").
		WithZone("z-1", "example.com")

	fake.OnCreateMonitor = func(mon dnscaster.Monitor) error {
		t.Fatalf("unexpected call to CreateMonitor()")
		return nil
	}

	t.Run("should not create a monitor by default", func(t *testing.T) {
		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{
				endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4"),
			},
		}

		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("should not create a monitor for unsupported types", func(t *testing.T) {
		create := endpoint.NewEndpoint("app.example.com", "CNAME", "target.example.com")
		create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https")
		create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects, "online")

		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{create},
		}

		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("should not create a monitor with missing annotation", func(t *testing.T) {
		create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
		create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https")

		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{create},
		}

		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("should return error when no nameserver set", func(t *testing.T) {
		create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
		create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https")

		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{create},
		}

		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCreateEndpointReturnsErrorWhenNoNameserverSets(t *testing.T) {
	t.Parallel()

	p, _ := newTestProvider(t)

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")},
	}

	err := p.ApplyChanges(context.Background(), changes)
	if err == nil || !strings.Contains(err.Error(), "no nameserver sets available") {
		t.Fatalf("expected no nameserver sets error, got: %v", err)
	}
}

func TestRecordsReturnsErrorWhenMonitorDoesNotExist(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.
		WithZone("z-1", "example.com").
		WithHost(dnscaster.Host{ID: "h-1", ZoneID: "z-1", FQDN: "api.example.com", Data: "1.2.3.4", IPMonitorID: "m-missing"})

	_, err := p.Records(context.Background())
	if err == nil {
		t.Fatal("expected an error when monitor cannot be fetched")
	}
	if !strings.Contains(err.Error(), "monitor not found") {
		t.Fatalf("expected monitor not found error, got: %v", err)
	}
}

func newTestProvider(t *testing.T) (provider.Provider, *dnscaster.FakeDNScasterClient) {
	t.Helper()

	config := configuration.Init()
	connConfig := &dnscaster.DNScasterConnectionConfig{}
	if err := env.Parse(connConfig); err != nil {
		t.Fatalf("reading dnscaster connection config failed: %v", err)
	}

	defaults := &dnscaster.DNScasterDefaults{}
	if err := env.Parse(defaults); err != nil {
		t.Fatalf("reading dnscaster defaults failed: %v", err)
	}

	client, fake, err := dnscaster.NewFakeDnscasterClient(connConfig, defaults)
	if err != nil {
		t.Fatalf("creating fake dnscaster client failed: %v", err)
	}

	p, err := dnsprovider.InitWithClient(config, client)
	if err != nil {
		t.Fatalf("failed to init provider: %v", err)
	}

	return p, fake
}
