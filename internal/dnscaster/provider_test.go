package dnscaster_test

import (
	"context"
	"errors"
	"maps"
	"regexp"
	"strings"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster/dnscastertest"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
)

func init() {
	log.Init()
}

func TestDefaultTTLUsesRecordValueWhenConfigured(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
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
		WithZone("z-1", "example.com")

	fake.OnCreateHost = func(host dnscaster.Host) error {
		if host.TTL != 600 {
			t.Fatalf("expected configured TTL: 600, got: %d", host.TTL)
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

func TestApplyCreateZoneMatching(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (provider.Provider, *dnscastertest.Client) {
		t.Helper()

		p, fake := newTestProviderWithDomainFilter(t, endpoint.NewDomainFilter([]string{"example.com", ".deep.example.com", "exact.example.net"}))

		fake.
			WithZone("z-1", "example.com").
			WithZone("z-2", "deep.example.com").
			WithZone("z-3", "exact.example.net").
			// In the account but outside the domain filter, so never used.
			WithZone("z-4", "unmanaged.org")

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

	t.Run("testing nested zones picks the deepest zone", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProviderWithDomainFilter(t, endpoint.NewDomainFilter([]string{"example.com"}))

		fake.
			WithZone("z-1", "example.com").
			WithZone("z-2", "sub.example.com")

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.Hostname != "a" {
				t.Fatalf("expected host.Hostname: a, got: %s", host.Hostname)
			}
			if host.ZoneID != "z-2" {
				t.Fatalf("expected host.ZoneID: z-2, got: %s", host.ZoneID)
			}
			return nil
		}
		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{
				endpoint.NewEndpoint("a.sub.example.com", "A", "1.2.3.4"),
			},
		}
		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("testing without domain filter", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProviderWithDomainFilter(t, endpoint.NewDomainFilter(nil))

		fake.
			WithZone("z-1", "example.com")

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.Hostname != "app" {
				t.Fatalf("expected host.Hostname: app, got: %s", host.Hostname)
			}
			if host.ZoneID != "z-1" {
				t.Fatalf("expected host.ZoneID: z-1, got: %s", host.ZoneID)
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
	})

	t.Run("testing regex filter", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProviderWithDomainFilter(t, endpoint.NewRegexDomainFilter(regexp.MustCompile(`example\.com$`), nil))

		fake.
			WithZone("z-1", "example.com").
			WithZone("z-2", "unmanaged.org")

		fake.OnCreateHost = func(host dnscaster.Host) error {
			if host.Hostname != "app" {
				t.Fatalf("expected host.Hostname: app, got: %s", host.Hostname)
			}
			if host.ZoneID != "z-1" {
				t.Fatalf("expected host.ZoneID: z-1, got: %s", host.ZoneID)
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
	})
}

func TestProviderWithoutDomainFilter(t *testing.T) {
	t.Parallel()

	p, fake := newTestProviderWithDomainFilter(t, endpoint.NewDomainFilter(nil))

	fake.
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
			WithHost(dnscaster.Host{ZoneID: "z-1", ID: "h-1", FQDN: "app.example.com", Properties: map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"}}).
			WithHost(dnscaster.Host{ZoneID: "z-2", ID: "h-2", FQDN: "app.other.com", Properties: map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"}}).
			WithHost(dnscaster.Host{ZoneID: "z-2", ID: "h-3", FQDN: "other.example.com", Properties: map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-2"}})

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
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{
				ZoneID:      "z-1",
				ID:          "h-1",
				IPMonitorID: "m-1",
				Data:        "1.2.3.4",
				Hostname:    "api",
				FQDN:        "api.example.com",
				Properties: map[string]string{
					dnscaster.ProviderMetadataOwnerID:                 "controller-1",
					dnscaster.ProviderSpecificIPMonitorURI:            "https:/health",
					dnscaster.ProviderSpecificIPMonitorTreatRedirects: "offline",
				},
			}).
			WithMonitor(dnscaster.Monitor{NameserverSetID: "ns-1", ID: "m-1", URI: "https://1.2.3.4/health", TreatRedirects: "offline"})

		records, err := p.Records(context.Background())
		if err != nil {
			t.Fatalf("unexpected error, got: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("expected 1 records, got: %d", len(records))
		}

		uri, _ := records[0].GetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI)
		if uri != "https:/health" {
			t.Fatalf("expected provider annotation %s=https, got: %s", dnscaster.ProviderSpecificIPMonitorURI, uri)
		}

		redirects, _ := records[0].GetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects)
		if redirects != "offline" {
			t.Fatalf("expected provider annotation %s=offline, got: %s", dnscaster.ProviderSpecificIPMonitorTreatRedirects, redirects)
		}
	})

	t.Run("should return records with default provider annotations", func(t *testing.T) {
		p, fake := newTestProvider(t)

		fake.
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{
				ZoneID:      "z-1",
				ID:          "h-1",
				IPMonitorID: "m-1",
				Data:        "1.2.3.4",
				Hostname:    "api",
				FQDN:        "api.example.com",
				Properties: map[string]string{
					dnscaster.ProviderMetadataOwnerID:      "controller-1",
					dnscaster.ProviderSpecificIPMonitorURI: "ping",
				},
			}).
			WithMonitor(dnscaster.Monitor{NameserverSetID: "ns-1", ID: "m-1", URI: "ping://1.2.3.4", TreatRedirects: "online"})

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

		redirects, ok := records[0].GetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects)
		if ok {
			t.Fatalf("unexpected provider annotation %s, got: %s", dnscaster.ProviderSpecificIPMonitorTreatRedirects, redirects)
		}
	})

	t.Run("should ignore owned records outside domain filter", func(t *testing.T) {
		p, fake := newTestProvider(t)

		fake.
			WithHost(dnscaster.Host{ZoneID: "z-2", ID: "h-1", FQDN: "app.other.com", Properties: map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"}})

		records, err := p.Records(context.Background())
		if err != nil {
			t.Fatalf("unexpected error, got: %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("expected 0 records, got: %d", len(records))
		}
	})
}

func TestProviderApplyChanges(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithZone("z-1", "example.com").
		WithHost(dnscaster.Host{
			ID:          "h-old",
			ZoneID:      "z-1",
			FQDN:        "app.example.com",
			DNSType:     "A",
			Data:        "1.2.3.4",
			IPMonitorID: "m-old",
			Properties:  map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"},
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

	t.Run("should error when deleting an unknown record", func(t *testing.T) {
		t.Parallel()

		changes := &plan.Changes{
			Delete: []*endpoint.Endpoint{endpoint.NewEndpoint("unknown.example.com", "A", "9.9.9.9")},
		}

		err := p.ApplyChanges(context.Background(), changes)
		if err == nil || !strings.Contains(err.Error(), "no host found") {
			t.Fatalf("expected a not found error, got: %v", err)
		}
	})
}

func TestProviderApplyChangesNoMonitors(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
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

		changes := &plan.Changes{
			Create: []*endpoint.Endpoint{create},
		}

		if err := p.ApplyChanges(context.Background(), changes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestProviderApplyChangesReturnsClientErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	createWithMonitor := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
	createWithMonitor.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")
	deleteExisting := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	updateWithMonitor := endpoint.NewEndpoint("app.example.com", "A", "5.6.7.8")
	updateWithMonitor.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")
	update := &plan.Changes{UpdateOld: []*endpoint.Endpoint{deleteExisting}, UpdateNew: []*endpoint.Endpoint{updateWithMonitor}}

	for name, tc := range map[string]struct {
		fail    func(*dnscastertest.Client)
		changes *plan.Changes
	}{
		"list zones": {
			fail:    func(c *dnscastertest.Client) { c.OnListZones = func() error { return boom } },
			changes: &plan.Changes{Create: []*endpoint.Endpoint{createWithMonitor}},
		},
		"list hosts": {
			fail:    func(c *dnscastertest.Client) { c.OnListHosts = func() error { return boom } },
			changes: &plan.Changes{Delete: []*endpoint.Endpoint{deleteExisting}},
		},
		"create monitor": {
			fail:    func(c *dnscastertest.Client) { c.OnCreateMonitor = func(dnscaster.Monitor) error { return boom } },
			changes: &plan.Changes{Create: []*endpoint.Endpoint{createWithMonitor}},
		},
		"delete host": {
			fail:    func(c *dnscastertest.Client) { c.OnDeleteHost = func(string) error { return boom } },
			changes: &plan.Changes{Delete: []*endpoint.Endpoint{deleteExisting}},
		},
		"delete monitor": {
			fail:    func(c *dnscastertest.Client) { c.OnDeleteMonitor = func(string) error { return boom } },
			changes: &plan.Changes{Delete: []*endpoint.Endpoint{deleteExisting}},
		},
		"update host": {
			fail:    func(c *dnscastertest.Client) { c.OnUpdateHost = func(string, dnscaster.Host) error { return boom } },
			changes: update,
		},
		"update monitor": {
			fail: func(c *dnscastertest.Client) {
				c.OnUpdateMonitor = func(string, dnscaster.Monitor) error { return boom }
			},
			changes: update,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, fake := newTestProvider(t)
			fake.
				WithZone("z-1", "example.com").
				WithHost(dnscaster.Host{
					ID:          "h-old",
					ZoneID:      "z-1",
					FQDN:        "app.example.com",
					DNSType:     "A",
					Data:        "1.2.3.4",
					IPMonitorID: "m-old",
					Properties:  map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"},
				}).
				WithMonitor(dnscaster.Monitor{ID: "m-old"})
			tc.fail(fake)

			if err := p.ApplyChanges(context.Background(), tc.changes); !errors.Is(err, boom) {
				t.Fatalf("expected the %s error, got: %v", name, err)
			}
		})
	}
}

func TestProviderRecordsReturnsClientErrors(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	boom := errors.New("boom")
	fake.OnListHosts = func() error { return boom }

	// Returning no records instead would make external-dns recreate them all.
	if records, err := p.Records(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("expected the list error, got records: %v, error: %v", records, err)
	}
}

func TestProviderApplyChangesDeletesMonitorWhenHostCreationFails(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")
	fake.OnCreateHost = func(dnscaster.Host) error { return errors.New("boom") }

	create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")

	if err := p.ApplyChanges(context.Background(), &plan.Changes{Create: []*endpoint.Endpoint{create}}); err == nil {
		t.Fatal("expected the host creation error")
	}
	if len(fake.Monitors) != 0 {
		t.Fatalf("expected the monitor to be deleted, got: %v", fake.Monitors)
	}
}

func TestProviderSetIdentifierRoundTrip(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")

	create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	create.SetIdentifier = "blue"
	if err := p.ApplyChanges(context.Background(), &plan.Changes{Create: []*endpoint.Endpoint{create}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fake.Hosts["z-1"][0].Properties[dnscaster.ProviderMetadataSetIdentifier]; got != "blue" {
		t.Fatalf("expected set identifier property blue, got: %q", got)
	}

	records, err := p.Records(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 || records[0].SetIdentifier != "blue" {
		t.Fatalf("expected a record with set identifier blue, got: %v", records)
	}
	// Provider metadata stays out of the provider-specific properties.
	if len(records[0].ProviderSpecific) != 0 {
		t.Fatalf("expected no provider-specific properties, got: %v", records[0].ProviderSpecific)
	}
}

func TestProviderGetDomainFilter(t *testing.T) {
	t.Parallel()

	p, _ := newTestProvider(t)

	filter := p.GetDomainFilter()
	if !filter.Match("app.example.com") || filter.Match("app.other.com") {
		t.Fatalf("expected the provider's example.com filter, got: %+v", filter)
	}
}

func TestProviderApplyChangesUpdatesInPlace(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithZone("z-1", "example.com").
		WithHost(dnscaster.Host{
			ID:          "h-old",
			ZoneID:      "z-1",
			FQDN:        "app.example.com",
			Hostname:    "app",
			DNSType:     "A",
			Data:        "1.2.3.4",
			TTL:         300,
			IPMonitorID: "m-old",
			Properties: map[string]string{
				dnscaster.ProviderMetadataOwnerID:                 "controller-1",
				dnscaster.ProviderSpecificIPMonitorURI:            "ping",
				dnscaster.ProviderSpecificIPMonitorTreatRedirects: "offline",
			},
		}).
		WithMonitor(dnscaster.Monitor{
			ID:              "m-old",
			Name:            "app.example.com",
			URI:             "ping://1.2.3.4",
			Hostname:        "app.example.com",
			TreatRedirects:  "offline",
			NameserverSetID: "ns-1",
		})

	// An in-place update replaces nothing and needs no zone lookup.
	fake.OnListZones = func() error { t.Fatal("unexpected call to ListZones()"); return nil }
	fake.OnCreateHost = func(dnscaster.Host) error { t.Fatal("unexpected call to CreateHost()"); return nil }
	fake.OnDeleteHost = func(string) error { t.Fatal("unexpected call to DeleteHost()"); return nil }
	fake.OnCreateMonitor = func(dnscaster.Monitor) error { t.Fatal("unexpected call to CreateMonitor()"); return nil }
	fake.OnDeleteMonitor = func(string) error { t.Fatal("unexpected call to DeleteMonitor()"); return nil }
	fake.OnUpdateHost = func(hostID string, host dnscaster.Host) error {
		if hostID != "h-old" {
			t.Fatalf("expected hostID=h-old, got %q", hostID)
		}
		// Read-only and unchanged fields stay out of the update.
		if host.ID != "" || host.FQDN != "" || host.Hostname != "" || host.ZoneID != "" {
			t.Fatalf("expected only writable fields in the update, got: %+v", host)
		}
		return nil
	}
	fake.OnUpdateMonitor = func(monitorID string, mon dnscaster.Monitor) error {
		if monitorID != "m-old" {
			t.Fatalf("expected monitorID=m-old, got %q", monitorID)
		}
		return nil
	}

	old := endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(300), "1.2.3.4")
	old.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")
	old.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects, "offline")

	desired := endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(120), "5.6.7.8")
	desired.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https:///health")
	desired.SetProviderSpecificProperty(dnscaster.ProviderSpecificLabelPrefix+"network", "eos")

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{old},
		UpdateNew: []*endpoint.Endpoint{desired},
	}
	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.Hosts["z-1"]) != 1 {
		t.Fatalf("expected the host to be kept, got: %v", fake.Hosts["z-1"])
	}
	host := fake.Hosts["z-1"][0]
	wantProperties := map[string]string{
		dnscaster.ProviderMetadataOwnerID:      "controller-1",
		dnscaster.ProviderSpecificIPMonitorURI: "https:///health",
		"network":                              "eos",
	}
	switch {
	case host.ID != "h-old" || host.IPMonitorID != "m-old":
		t.Fatalf("expected host h-old to keep monitor m-old, got: %+v", host)
	case host.Data != "5.6.7.8" || host.TTL != 120:
		t.Fatalf("expected data=5.6.7.8 ttl=120, got: %+v", host)
	case host.FQDN != "app.example.com" || host.Hostname != "app" || host.ZoneID != "z-1":
		t.Fatalf("expected the host to keep its name and zone, got: %+v", host)
	case !maps.Equal(host.Properties, wantProperties):
		// Properties are replaced as a whole, so the removed annotation is gone.
		t.Fatalf("expected properties %v, got: %v", wantProperties, host.Properties)
	}

	monitor := fake.Monitors["m-old"]
	switch {
	case monitor.URI != "https://5.6.7.8/health":
		t.Fatalf("expected the monitor to follow the new target, got: %+v", monitor)
	case monitor.TreatRedirects != "online":
		t.Fatalf("expected treat_redirects reset to online, got: %+v", monitor)
	case monitor.Name != "app.example.com" || monitor.Hostname != "app.example.com" || monitor.NameserverSetID != "ns-1":
		t.Fatalf("expected the monitor to keep its name, hostname and nameserver set, got: %+v", monitor)
	case !maps.Equal(monitor.Properties, wantProperties):
		t.Fatalf("expected monitor properties %v, got: %v", wantProperties, monitor.Properties)
	}
}

func TestProviderApplyChangesUpdateAddsMonitor(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithZone("z-1", "example.com").
		WithHost(dnscaster.Host{
			ID:         "h-old",
			ZoneID:     "z-1",
			FQDN:       "app.example.com",
			Hostname:   "app",
			DNSType:    "A",
			Data:       "1.2.3.4",
			TTL:        300,
			Properties: map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"},
		})

	fake.OnCreateHost = func(dnscaster.Host) error { t.Fatal("unexpected call to CreateHost()"); return nil }
	fake.OnDeleteHost = func(string) error { t.Fatal("unexpected call to DeleteHost()"); return nil }
	fake.OnUpdateMonitor = func(string, dnscaster.Monitor) error { t.Fatal("unexpected call to UpdateMonitor()"); return nil }
	fake.OnCreateMonitor = func(mon dnscaster.Monitor) error {
		if mon.URI != "ping://1.2.3.4" {
			t.Fatalf("expected uri=ping://1.2.3.4, got %q", mon.URI)
		}
		return nil
	}
	fake.OnUpdateHost = func(hostID string, host dnscaster.Host) error {
		if hostID != "h-old" || host.IPMonitorID != "m-1" {
			t.Fatalf("expected host h-old to get monitor m-1, got: %s %+v", hostID, host)
		}
		return nil
	}

	desired := endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(300), "1.2.3.4")
	desired.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(300), "1.2.3.4")},
		UpdateNew: []*endpoint.Endpoint{desired},
	}
	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if host := fake.Hosts["z-1"][0]; host.ID != "h-old" || host.IPMonitorID != "m-1" {
		t.Fatalf("expected host h-old with monitor m-1, got: %+v", host)
	}
	if _, ok := fake.Monitors["m-1"]; !ok || len(fake.Monitors) != 1 {
		t.Fatalf("expected monitor m-1 only, got: %v", fake.Monitors)
	}
}

func TestProviderApplyChangesUpdateRemovingMonitorReplacesHost(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)

	fake.
		WithZone("z-1", "example.com").
		WithHost(dnscaster.Host{
			ID:          "h-old",
			ZoneID:      "z-1",
			FQDN:        "app.example.com",
			Hostname:    "app",
			DNSType:     "A",
			Data:        "1.2.3.4",
			TTL:         300,
			IPMonitorID: "m-old",
			Properties: map[string]string{
				dnscaster.ProviderMetadataOwnerID:      "controller-1",
				dnscaster.ProviderSpecificIPMonitorURI: "ping",
			},
		}).
		WithMonitor(dnscaster.Monitor{ID: "m-old", URI: "ping://1.2.3.4"})

	fake.OnUpdateHost = func(string, dnscaster.Host) error { t.Fatal("unexpected call to UpdateHost()"); return nil }
	fake.OnUpdateMonitor = func(string, dnscaster.Monitor) error { t.Fatal("unexpected call to UpdateMonitor()"); return nil }

	old := endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(300), "1.2.3.4")
	old.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")

	changes := &plan.Changes{
		UpdateOld: []*endpoint.Endpoint{old},
		UpdateNew: []*endpoint.Endpoint{endpoint.NewEndpointWithTTL("app.example.com", "A", endpoint.TTL(300), "5.6.7.8")},
	}
	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.Hosts["z-1"]) != 1 {
		t.Fatalf("expected a single host, got: %v", fake.Hosts["z-1"])
	}
	host := fake.Hosts["z-1"][0]
	if host.ID == "h-old" || host.IPMonitorID != "" || host.Data != "5.6.7.8" || host.Hostname != "app" || host.ZoneID != "z-1" {
		t.Fatalf("expected a new host without a monitor, got: %+v", host)
	}
	if len(fake.Monitors) != 0 {
		t.Fatalf("expected the monitor to be deleted, got: %v", fake.Monitors)
	}
}

func TestProviderApplyChangesUpdateErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	t.Run("should error when the host to update is unknown", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProvider(t)
		fake.WithZone("z-1", "example.com")

		changes := &plan.Changes{
			UpdateOld: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")},
			UpdateNew: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "5.6.7.8")},
		}
		err := p.ApplyChanges(context.Background(), changes)
		if err == nil || !strings.Contains(err.Error(), "no host found") {
			t.Fatalf("expected a not found error, got: %v", err)
		}
	})

	t.Run("should error when old and new records do not pair up", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProvider(t)
		fake.OnListHosts = func() error { t.Fatal("unexpected call to ListHosts()"); return nil }

		changes := &plan.Changes{
			UpdateOld: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")},
		}
		err := p.ApplyChanges(context.Background(), changes)
		if err == nil || !strings.Contains(err.Error(), "malformed changes") {
			t.Fatalf("expected a malformed changes error, got: %v", err)
		}
	})

	t.Run("should leave the host untouched when its monitor update fails", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProvider(t)
		fake.
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{
				ID:          "h-old",
				ZoneID:      "z-1",
				FQDN:        "app.example.com",
				DNSType:     "A",
				Data:        "1.2.3.4",
				IPMonitorID: "m-old",
				Properties:  map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1", dnscaster.ProviderSpecificIPMonitorURI: "ping"},
			}).
			WithMonitor(dnscaster.Monitor{ID: "m-old", URI: "ping://1.2.3.4"})
		fake.OnUpdateMonitor = func(string, dnscaster.Monitor) error { return boom }
		fake.OnUpdateHost = func(string, dnscaster.Host) error { t.Fatal("unexpected call to UpdateHost()"); return nil }

		old := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
		old.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")
		desired := endpoint.NewEndpoint("app.example.com", "A", "5.6.7.8")
		desired.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")

		changes := &plan.Changes{UpdateOld: []*endpoint.Endpoint{old}, UpdateNew: []*endpoint.Endpoint{desired}}
		if err := p.ApplyChanges(context.Background(), changes); !errors.Is(err, boom) {
			t.Fatalf("expected the monitor update error, got: %v", err)
		}
		if host := fake.Hosts["z-1"][0]; host.Data != "1.2.3.4" {
			t.Fatalf("expected the host to be untouched, got: %+v", host)
		}
	})

	t.Run("should delete a new monitor when the host update fails", func(t *testing.T) {
		t.Parallel()

		p, fake := newTestProvider(t)
		fake.
			WithZone("z-1", "example.com").
			WithHost(dnscaster.Host{
				ID:         "h-old",
				ZoneID:     "z-1",
				FQDN:       "app.example.com",
				DNSType:    "A",
				Data:       "1.2.3.4",
				Properties: map[string]string{dnscaster.ProviderMetadataOwnerID: "controller-1"},
			})
		fake.OnUpdateHost = func(string, dnscaster.Host) error { return boom }

		desired := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
		desired.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")

		changes := &plan.Changes{
			UpdateOld: []*endpoint.Endpoint{endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")},
			UpdateNew: []*endpoint.Endpoint{desired},
		}
		if err := p.ApplyChanges(context.Background(), changes); !errors.Is(err, boom) {
			t.Fatalf("expected the host update error, got: %v", err)
		}
		if len(fake.Monitors) != 0 {
			t.Fatalf("expected the monitor to be deleted, got: %v", fake.Monitors)
		}
	})
}
