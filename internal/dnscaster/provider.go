package dnscaster

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
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

	// If the client connects properly, create the DNS Provider
	p := &DnscasterProvider{
		client:       client,
		domainFilter: domainFilter,
	}

	return p, nil
}

func (p *DnscasterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	records := make([]*endpoint.Endpoint, 0)
	for _, zone := range zones {
		if !p.domainFilter.Match(zone.Domain) {
			continue
		}

		hosts, err := p.client.ListHosts(ctx, zone.ID)
		if err != nil {
			return nil, err
		}

		for _, host := range hosts {
			records = append(records, endpoint.NewEndpointWithTTL(host.DNSName, host.DNSType, endpoint.TTL(host.TTL), host.Data))
		}
	}

	return records, nil
}

func (p *DnscasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return err
	}

	zonesMap := make(map[string]string, len(zones))
	for _, zone := range zones {
		if !p.domainFilter.Match(zone.Domain) {
			continue
		}
		zonesMap[zone.Domain] = zone.ID
	}
	log.Debug("ApplyChanges", "zonesMap", zonesMap)

	var hosts []Host
	for _, id := range zonesMap {
		h, err := p.client.ListHosts(ctx, id)
		if err != nil {
			return err
		}
		hosts = append(hosts, h...)
	}
	log.Debug("ApplyChanges", "hosts", hosts)

	for _, record := range changes.Create {
		// TODO: Find a better way to split parent zone from hostname
		stubs := strings.SplitN(record.DNSName, ".", 2)

		zoneID, ok := zonesMap[stubs[1]]
		if !ok {
			continue
		}

		host := Host{
			ZoneID: zoneID,
			// TODO: Shouldn't only take the first target, find a better way
			Data:     record.Targets[0],
			DNSType:  record.RecordType,
			DNSName:  record.DNSName,
			Hostname: stubs[0],
			TTL:      p.client.DefaultTTL,
		}
		if &record.RecordTTL != nil {
			host.TTL = int64(record.RecordTTL)
		}

		_, err := p.client.CreateHost(ctx, host)
		if err != nil {
			return err
		}
	}

	for _, record := range changes.Delete {
		for _, host := range hosts {
			if record.DNSName == host.DNSName {
				if err := p.client.DeleteHost(ctx, host.ID); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// GetDomainFilter returns the domain filter for the provider.
func (p *DnscasterProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}
