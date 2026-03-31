package dnscaster_test

import (
	"context"
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
