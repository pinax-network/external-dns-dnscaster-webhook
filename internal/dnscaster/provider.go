package dnscaster

import (
	"context"
	"fmt"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

// DnscasterProvider is a helper class for working with dnscaster
type DnscasterProvider struct {
	provider.BaseProvider

	client       *DnscasterApiClient
	domainFilter *endpoint.DomainFilter
}

// NewDnscasterProvider initializes a new DNSProvider, of the Dnscaster variety
func NewDnscasterProvider(domainFilter *endpoint.DomainFilter, defaults *DnscasterDefaults, config *DnscasterConnectionConfig) (provider.Provider, error) {
	// Create the Dnscaster API Client
	client, err := NewDnscasterClient(config, defaults)
	if err != nil {
		return nil, fmt.Errorf("failed to create the Dnscaster client: %w", err)
	}

	// // Ensure the Client can connect to the API by fetching system info
	// info, err := client.GetSystemInfo()
	// if err != nil {
	// 	log.Error("failed to connect to the Dnscaster RouterOS API Endpoint: %v", err)
	// 	return nil, err
	// }
	// log.Info("connected to board %s running RouterOS version %s (%s)", info.BoardName, info.Version, info.ArchitectureName)

	// If the client connects properly, create the DNS Provider
	p := &DnscasterProvider{
		client:       client,
		domainFilter: domainFilter,
	}

	return p, nil
}

func (p *DnscasterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	return nil, nil
}

func (p *DnscasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	return nil
}

// GetDomainFilter returns the domain filter for the provider.
func (p *DnscasterProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}
