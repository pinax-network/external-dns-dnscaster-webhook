package dnscaster_test

import (
	"context"
	"testing"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster/dnscastertest"
)

func newTestProvider(t *testing.T) (provider.Provider, *dnscastertest.Client) {
	t.Helper()

	return newTestProviderWithDomainFilter(t, endpoint.NewDomainFilter([]string{"example.com"}))
}

func newTestProviderWithDomainFilter(t *testing.T, domainFilter *endpoint.DomainFilter) (provider.Provider, *dnscastertest.Client) {
	t.Helper()

	connConfig := baseConnConfig()
	fake := dnscastertest.NewClient(connConfig.OwnerID)
	return dnscaster.NewDNScasterProvider(domainFilter, baseDefaults(), connConfig, fake), fake
}

func baseConnConfig() *dnscaster.DNScasterConnectionConfig {
	return &dnscaster.DNScasterConnectionConfig{
		ApiKey:          "k",
		NameserverSetID: "ns-1",
		OwnerID:         "controller-1",
	}
}

func baseDefaults() *dnscaster.DNScasterDefaults {
	return &dnscaster.DNScasterDefaults{
		DefaultTTL: 600,
	}
}

// syncChanges plans the changes external-dns would send to bring p's records
// in line with desired, as a sync with the noop registry does.
func syncChanges(t *testing.T, p provider.Provider, desired ...*endpoint.Endpoint) *plan.Changes {
	t.Helper()

	current, err := p.Records(context.Background())
	if err != nil {
		t.Fatalf("unexpected error listing records: %v", err)
	}
	return (&plan.Plan{
		Current:        current,
		Desired:        desired,
		Policies:       []plan.Policy{&plan.SyncPolicy{}},
		ManagedRecords: []string{endpoint.RecordTypeA, endpoint.RecordTypeAAAA, endpoint.RecordTypeCNAME},
	}).Calculate().Changes
}
