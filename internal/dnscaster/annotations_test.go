package dnscaster_test

import (
	"context"
	"maps"
	"strings"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster"
)

func TestAnnotationsCreateMonitorWithDefaults(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")

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

	fake.OnCreateMonitor = func(req dnscaster.Monitor) error {
		if req.NameserverSetID != "ns-1" {
			t.Fatalf("expected nameserver_set_id=ns-1, got %q", req.NameserverSetID)
		}
		if req.URI != "ping://5.6.7.8" {
			t.Fatalf("expected uri=ping://5.6.7.8, got %q", req.URI)
		}
		if req.Hostname != "new.example.com" {
			t.Fatalf("expected hostname=new.example.com, got %q", req.Hostname)
		}
		if req.TreatRedirects != "" {
			t.Fatalf("expected treat_redirects='', got %q", req.TreatRedirects)
		}
		return nil
	}

	create := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "ping")

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{create},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnnotationsCreateMonitorWithDifferentURIFromRecord(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")

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

	fake.OnCreateMonitor = func(req dnscaster.Monitor) error {
		if req.NameserverSetID != "ns-1" {
			t.Fatalf("expected nameserver_set_id=ns-1, got %q", req.NameserverSetID)
		}
		if req.URI != "https://1.1.1.1/health" {
			t.Fatalf("expected uri=https://1.1.1.1/health, got %q", req.URI)
		}
		if req.TreatRedirects != "offline" {
			t.Fatalf("expected treat_redirects='offline', got %q", req.TreatRedirects)
		}
		return nil
	}

	create := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https://1.1.1.1/health")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects, "offline")

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{create},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnnotationsCreateMonitorWithDifferentHostname(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")

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

	fake.OnCreateMonitor = func(req dnscaster.Monitor) error {
		if req.NameserverSetID != "ns-1" {
			t.Fatalf("expected nameserver_set_id=ns-1, got %q", req.NameserverSetID)
		}
		if req.URI != "https://1.1.1.1/health" {
			t.Fatalf("expected uri=https://1.1.1.1/health, got %q", req.URI)
		}
		if req.Hostname != "api.other.com" {
			t.Fatalf("expected hostname=api.other.com, got %q", req.Hostname)
		}
		if req.TreatRedirects != "offline" {
			t.Fatalf("expected treat_redirects='offline', got %q", req.TreatRedirects)
		}
		return nil
	}

	create := endpoint.NewEndpoint("new.example.com", "A", "5.6.7.8")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "https://1.1.1.1/health")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorHostname, "api.other.com")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorTreatRedirects, "offline")

	changes := &plan.Changes{
		Create: []*endpoint.Endpoint{create},
	}

	if err := p.ApplyChanges(context.Background(), changes); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnnotationsLabelsRoundTrip(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")

	// Label names escape "~" as "~0" and "/" as "~1", as in a JSON Pointer.
	labels := map[string]string{
		dnscaster.ProviderSpecificLabelPrefix + "network":        "ethereum",
		dnscaster.ProviderSpecificLabelPrefix + "registry~1name": "mainnet",
		dnscaster.ProviderSpecificLabelPrefix + "literal~01":     "tilde-one",
	}
	wantProperties := map[string]string{
		"network":                         "ethereum",
		"registry/name":                   "mainnet",
		"literal~1":                       "tilde-one",
		dnscaster.ProviderMetadataOwnerID: "controller-1",
	}

	create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	for name, value := range labels {
		create.SetProviderSpecificProperty(name, value)
	}
	// Provider-specific properties meant for other providers are ignored.
	create.SetProviderSpecificProperty("aws/weight", "10")

	fake.OnCreateHost = func(host dnscaster.Host) error {
		if !maps.Equal(host.Properties, wantProperties) {
			t.Fatalf("expected properties %v, got: %v", wantProperties, host.Properties)
		}
		return nil
	}

	if err := p.ApplyChanges(context.Background(), &plan.Changes{Create: []*endpoint.Endpoint{create}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records, err := p.Records(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got: %v", records)
	}
	got := make(map[string]string)
	for _, property := range records[0].ProviderSpecific {
		got[property.Name] = property.Value
	}
	if !maps.Equal(got, labels) {
		t.Fatalf("expected provider-specific properties %v, got: %v", labels, got)
	}
}

func TestAnnotationsRejectInvalidMonitorURI(t *testing.T) {
	t.Parallel()

	p, fake := newTestProvider(t)
	fake.WithZone("z-1", "example.com")

	create := endpoint.NewEndpoint("app.example.com", "A", "1.2.3.4")
	create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, "://bad")

	if err := p.ApplyChanges(context.Background(), &plan.Changes{Create: []*endpoint.Endpoint{create}}); err == nil {
		t.Fatal("expected an error for an invalid monitor uri")
	}
	if len(fake.Hosts["z-1"]) != 0 || len(fake.Monitors) != 0 {
		t.Fatalf("expected nothing to be created, got hosts: %v, monitors: %v", fake.Hosts, fake.Monitors)
	}
}

func TestAnnotationsMonitorURITemplating(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		uri, target, want string
	}{
		{"https:///v1/chain/get_info", "1.2.3.4", "https://1.2.3.4/v1/chain/get_info"},
		{"https:///v1/chain/get_info", "2001:db8::1", "https://[2001:db8::1]/v1/chain/get_info"},
		{"https:/health", "1.2.3.4", "https://1.2.3.4/health"},
		{"tcp://:9876", "1.2.3.4", "tcp://1.2.3.4:9876"},
		{"tcp://:9876", "2001:db8::1", "tcp://[2001:db8::1]:9876"},
		{"ping", "1.2.3.4", "ping://1.2.3.4"},
		{"ping", "2001:db8::1", "ping://[2001:db8::1]"},
		{"https://5.6.7.8/health", "1.2.3.4", "https://5.6.7.8/health"},
		{"https://5.6.7.8/health", "2001:db8::1", "https://5.6.7.8/health"},
	} {
		t.Run(tc.uri+" "+tc.target, func(t *testing.T) {
			t.Parallel()

			p, fake := newTestProvider(t)
			fake.WithZone("z-1", "example.com")

			var got string
			fake.OnCreateMonitor = func(monitor dnscaster.Monitor) error {
				got = monitor.URI
				return nil
			}

			recordType := endpoint.RecordTypeA
			if strings.Contains(tc.target, ":") {
				recordType = endpoint.RecordTypeAAAA
			}
			create := endpoint.NewEndpoint("app.example.com", recordType, tc.target)
			create.SetProviderSpecificProperty(dnscaster.ProviderSpecificIPMonitorURI, tc.uri)

			if err := p.ApplyChanges(context.Background(), &plan.Changes{Create: []*endpoint.Endpoint{create}}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected monitor uri %s, got: %s", tc.want, got)
			}
		})
	}
}
