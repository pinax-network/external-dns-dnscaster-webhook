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

	managedZones, err := p.filterManagedZones(ctx, zones)
	if err != nil {
		return nil, err
	}

	records := make([]*endpoint.Endpoint, 0)
	for _, zone := range managedZones {
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

	managedZones, err := p.filterManagedZones(ctx, zones)
	if err != nil {
		return err
	}

	zonesMap := make(map[string]string, len(managedZones))
	for _, zone := range managedZones {
		zonesMap[zone.Domain] = zone.ID
	}

	hostsMap := make(map[string]string)
	for _, id := range zonesMap {
		hosts, err := p.client.ListHosts(ctx, id)
		if err != nil {
			return err
		}
		for _, host := range hosts {
			hostsMap[host.DNSName] = host.ID
		}
	}

	// Process deletions (records to update will be deleted and recreated later)
	for _, record := range append(changes.UpdateOld, changes.Delete...) {
		hostID, _ := hostsMap[record.DNSName]
		log.Debug("ApplyChanges - Delete", "record.DNSName", record.DNSName, "hostID", hostID)
		if err := p.client.DeleteHost(ctx, hostID); err != nil {
			return err
		}
	}

	// Process creates (updated records are recreated here)
	for _, record := range append(changes.Create, changes.UpdateNew...) {
		hosts := p.hostsForEndpoint(record)
		hostname, zone := p.trimHostnameFromFQDN(record)
		log.Debug("ApplyChanges - Create", "hostname", hostname, "zone", zone)

		for _, host := range hosts {
			host.Hostname = hostname
			host.ZoneID = zonesMap[zone]

			_, err := p.client.CreateHost(ctx, host)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GetDomainFilter returns the domain filter for the provider.
func (p *DnscasterProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

func (p *DnscasterProvider) filterManagedZones(ctx context.Context, zones []Zone) ([]Zone, error) {
	var filtered []Zone

	for _, zone := range zones {
		if !p.domainFilter.Match(zone.Domain) {
			log.Debug("filterManagedZones", "skipping zone as it does not match domain filter", zone.Domain)
			continue
		}

		filtered = append(filtered, zone)
	}
	log.Debug("filterManagedZones", "total managed zones", len(filtered))
	return filtered, nil
}

func (p *DnscasterProvider) hostsForEndpoint(record *endpoint.Endpoint) []Host {
	ttl := p.defaultTTL(record)
	hosts := make([]Host, 0, len(record.Targets))
	for _, target := range endpoint.NewTargets(record.Targets...) {
		hosts = append(hosts, Host{
			Data:    target,
			DNSType: record.RecordType,
			DNSName: record.DNSName,
			TTL:     ttl,
		})
	}
	return hosts
}

func (p *DnscasterProvider) defaultTTL(record *endpoint.Endpoint) int64 {
	if record.RecordTTL.IsConfigured() {
		return int64(record.RecordTTL)
	}
	return p.client.DefaultTTL
}

func (p *DnscasterProvider) trimHostnameFromFQDN(record *endpoint.Endpoint) (string, string) {
	var bestFilter string

	for _, filter := range p.domainFilter.Filters {
		log.Debug("trimHostnameFromFQDN", "testing filter", filter)
		if filter == "" {
			continue
		}

		switch {
		case strings.HasPrefix(filter, ".") && strings.HasSuffix(record.DNSName, filter):
			if len(filter) > len(bestFilter) {
				bestFilter = filter
			}
		case strings.Count(record.DNSName, ".") == strings.Count(filter, ".") && record.DNSName == filter:
			if len(filter) > len(bestFilter) {
				bestFilter = filter
			}
		case strings.HasSuffix(record.DNSName, "."+filter):
			if len(filter) > len(bestFilter) {
				bestFilter = filter
			}
		}
	}

	switch {
	case bestFilter == "":
		return "", record.DNSName

	case strings.HasPrefix(bestFilter, "."):
		hostname := strings.TrimSuffix(record.DNSName, bestFilter)
		zone := strings.TrimPrefix(bestFilter, ".")
		return hostname, zone

	case record.DNSName == bestFilter:
		return "", bestFilter

	default:
		hostname := strings.TrimSuffix(record.DNSName, "."+bestFilter)
		return hostname, bestFilter
	}
}
