//go:build e2e

package dnscaster

// The e2e tests run the provider in dry-run mode against the real DNScaster
// API: every read reaches the API, and a test fails if anything else is sent.
// They take the same DNSCASTER_* settings as the webhook, plus
// DNSCASTER_E2E_ZONE, a zone of that account to plan changes in. Hosts the
// owner already has in that zone are used to plan deletes and updates.
//
// Run them with `make e2e`.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	env "github.com/caarlos0/env/v11"
	"sigs.k8s.io/external-dns/endpoint"
	externaldns "sigs.k8s.io/external-dns/pkg/apis/externaldns"
	"sigs.k8s.io/external-dns/plan"
	extwebhook "sigs.k8s.io/external-dns/provider/webhook"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/server"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/webhook"
)

// readOnlyTransport fails the test when anything but a read is sent: that is
// what dry-run mode promises.
type readOnlyTransport struct {
	t    *testing.T
	next http.RoundTripper
}

func (rt readOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		// Errorf rather than Fatalf: the webhook server calls this from its
		// own goroutine.
		rt.t.Errorf("dry run sent %s %s", req.Method, req.URL.Path)
		return nil, errors.New("dry run must not change anything")
	}
	return rt.next.RoundTrip(req)
}

// e2eProvider returns a dry-run provider for the e2e zone, configured from the
// environment like the webhook is.
func e2eProvider(t *testing.T) (*DNScasterProvider, string) {
	t.Helper()

	zone := os.Getenv("DNSCASTER_E2E_ZONE")
	if zone == "" {
		t.Fatal("DNSCASTER_E2E_ZONE must name a zone of the DNScaster account")
	}

	var config DNScasterConnectionConfig
	if err := env.Parse(&config); err != nil {
		t.Fatalf("reading dnscaster configuration failed: %v", err)
	}
	var defaults DNScasterDefaults
	if err := env.Parse(&defaults); err != nil {
		t.Fatalf("reading dnscaster defaults failed: %v", err)
	}

	pp, err := NewDNScasterProvider(endpoint.NewDomainFilter([]string{zone}), &defaults, &config, true)
	if err != nil {
		t.Fatalf("creating provider failed: %v", err)
	}

	p := pp.(*DNScasterProvider)
	p.client.Transport = readOnlyTransport{t: t, next: p.client.Transport}
	return p, zone
}

// zoneID returns the ID of the e2e zone.
func zoneID(t *testing.T, p *DNScasterProvider, zone string) string {
	t.Helper()

	zones, err := p.client.ListZones(t.Context())
	if err != nil {
		t.Fatalf("listing zones failed: %v", err)
	}

	i := slices.IndexFunc(zones, func(z Zone) bool { return z.Domain == zone })
	if i < 0 {
		t.Fatalf("zone %s not found among the %d zones of the account", zone, len(zones))
	}
	return zones[i].ID
}

func TestE2EListHostsPagesThroughOwnedHosts(t *testing.T) {
	p, _ := e2eProvider(t)

	all, err := p.client.ListHosts(t.Context())
	if err != nil {
		t.Fatalf("listing hosts failed: %v", err)
	}
	for _, h := range all {
		if owner := h.Properties[ProviderMetadataOwnerID]; owner != p.client.OwnerID {
			t.Fatalf("listed host %s of owner %q, want only owner %q", h.ID, owner, p.client.OwnerID)
		}
	}
	if len(all) < 2 {
		t.Skipf("owner %q has %d hosts, paging needs at least 2", p.client.OwnerID, len(all))
	}

	// About three pages: enough to follow the cursor, few enough to stay
	// well under the client's page limit.
	p.client.DefaultPageSize = max(1, (len(all)+2)/3)
	paged, err := p.client.ListHosts(t.Context())
	if err != nil {
		t.Fatalf("listing hosts %d per page failed: %v", p.client.DefaultPageSize, err)
	}

	ids := func(hosts []Host) []string {
		out := make([]string, 0, len(hosts))
		for _, h := range hosts {
			out = append(out, h.ID)
		}
		slices.Sort(out)
		return out
	}
	if got, want := ids(paged), ids(all); !slices.Equal(got, want) {
		t.Fatalf("paging %d hosts at a time listed %v, want %v", p.client.DefaultPageSize, got, want)
	}
}

func TestE2EPlansCreatesInTheZone(t *testing.T) {
	p, zone := e2eProvider(t)
	id := zoneID(t, p, zone)

	a := endpoint.NewEndpoint("e2e-dry-run."+zone, "A", "192.0.2.10")
	a.SetProviderSpecificProperty(ProviderSpecificIPMonitorURI, "https:/health")
	a.SetProviderSpecificProperty(ProviderSpecificLabelPrefix+"suite", "e2e")
	cname := endpoint.NewEndpoint("e2e-dry-run-alias."+zone, "CNAME", "e2e-dry-run."+zone)

	cs, err := p.planChanges(t.Context(), &plan.Changes{Create: []*endpoint.Endpoint{a, cname}})
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if len(cs.deletes) != 0 || len(cs.creates) != 2 {
		t.Fatalf("expected 2 creates, got %d deletes and %d creates", len(cs.deletes), len(cs.creates))
	}

	host, monitor := cs.creates[0].host, cs.creates[0].monitor
	if host.ZoneID != id || host.Hostname != "e2e-dry-run" {
		t.Fatalf("unexpected A host zone_id=%q hostname=%q, want zone_id=%q hostname=e2e-dry-run", host.ZoneID, host.Hostname, id)
	}
	if host.Properties[ProviderMetadataOwnerID] != p.client.OwnerID || host.Properties["suite"] != "e2e" {
		t.Fatalf("unexpected A host properties: %v", host.Properties)
	}
	if monitor == nil || monitor.URI != "https://192.0.2.10/health" || monitor.NameserverSetID != p.client.NameserverSetID {
		t.Fatalf("unexpected monitor: %+v", monitor)
	}

	alias := cs.creates[1]
	if alias.host.ZoneID != id || alias.host.Hostname != "e2e-dry-run-alias" || alias.monitor != nil {
		t.Fatalf("unexpected CNAME create: %+v", alias)
	}
}

func TestE2EPlansDeletesForExistingRecords(t *testing.T) {
	p, _ := e2eProvider(t)

	hosts, err := p.client.ListHosts(t.Context())
	if err != nil {
		t.Fatalf("listing hosts failed: %v", err)
	}
	records := p.recordsForHosts(hosts)
	if len(records) == 0 {
		t.Skipf("owner %q has no hosts in the e2e zone to plan deletes for", p.client.OwnerID)
	}

	// Plan from the same listing, so hosts another controller changes in the
	// meantime cannot make the comparison fail.
	cs, err := p.newChangeSet(records, nil, nil, hosts)
	if err != nil {
		t.Fatalf("planning failed: %v", err)
	}
	if len(cs.deletes) != len(records) {
		t.Fatalf("planned %d deletes for %d records", len(cs.deletes), len(records))
	}
	for _, planned := range cs.deletes {
		listed := slices.ContainsFunc(hosts, func(h Host) bool {
			return h.ID == planned.ID && h.IPMonitorID == planned.IPMonitorID
		})
		if planned.ID == "" || !listed {
			t.Fatalf("planned delete of a host that was not listed: %+v", planned)
		}
	}
}

// TestE2EWebhookDryRun drives the webhook with external-dns's own client, the
// way the external-dns controller does on every sync.
func TestE2EWebhookDryRun(t *testing.T) {
	p, zone := e2eProvider(t)
	ctx := t.Context()

	srv := httptest.NewServer(server.NewRouter(webhook.New(p)))
	t.Cleanup(srv.Close)

	ext, err := extwebhook.New(ctx, &externaldns.Config{
		WebhookProviderURL:          srv.URL,
		WebhookProviderReadTimeout:  30 * time.Second,
		WebhookProviderWriteTimeout: 30 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("connecting to the webhook failed: %v", err)
	}

	current, err := ext.Records(ctx)
	if err != nil {
		t.Fatalf("listing records failed: %v", err)
	}

	// Desire one new record, and drop and change existing ones when there are
	// any, so the change set has creates, deletes and updates.
	desired := []*endpoint.Endpoint{endpoint.NewEndpoint("e2e-dry-run."+zone, "A", "192.0.2.10")}
	for i, record := range current {
		switch i {
		case 0:
			continue
		case 1:
			record = record.DeepCopy()
			record.RecordTTL++
		}
		desired = append(desired, record)
	}

	desired, err = ext.AdjustEndpoints(desired)
	if err != nil {
		t.Fatalf("adjusting endpoints failed: %v", err)
	}

	changes := (&plan.Plan{
		Current:        current,
		Desired:        desired,
		Policies:       []plan.Policy{&plan.SyncPolicy{}},
		DomainFilter:   endpoint.MatchAllDomainFilters{ext.GetDomainFilter()},
		ManagedRecords: []string{endpoint.RecordTypeA, endpoint.RecordTypeAAAA, endpoint.RecordTypeCNAME, endpoint.RecordTypeTXT},
	}).Calculate().Changes
	t.Logf("%d records listed; applying %d creates, %d updates and %d deletes",
		len(current), len(changes.Create), len(changes.UpdateNew), len(changes.Delete))

	if err := ext.ApplyChanges(ctx, changes); err != nil {
		t.Fatalf("applying changes failed: %v", err)
	}
}
