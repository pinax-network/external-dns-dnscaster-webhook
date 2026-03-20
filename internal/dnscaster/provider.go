package dnscaster

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

const (
	providerSpecificIPMonitorURI            = "webhook/dnscaster-ip-monitor-uri"
	providerSpecificIPMonitorTreatRedirects = "webhook/dnscaster-ip-monitor-treat-redirects"
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
			endpoint, err := p.endpointFromHost(ctx, host)
			if err != nil {
				return nil, err
			}
			records = append(records, endpoint)
		}
	}
	return records, nil
}

func (p *DnscasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	nameserverSets, err := p.client.ListNameserverSets(ctx)
	if err != nil {
		return err
	}

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

	hostsMap := make(map[string]Host)
	for _, id := range zonesMap {
		hosts, err := p.client.ListHosts(ctx, id)
		if err != nil {
			return err
		}
		for _, host := range hosts {
			hostsMap[host.FQDN] = host
		}
	}

	// Process deletions (records to update will be deleted and recreated later)
	for _, record := range append(changes.UpdateOld, changes.Delete...) {
		log.Debug("ApplyChanges - Delete", "record", record)

		host, ok := hostsMap[record.DNSName]
		if !ok {
			// Sanity check, should not happen
			return fmt.Errorf("tried to delete host that doesn't exist in DNScaster: %w", err)
		}

		if err := p.client.DeleteHost(ctx, host.ID); err != nil {
			return err
		}

		// Deleting needs to happen after the host using it has been removed
		if host.IPMonitorID != "" {
			if err := p.client.DeleteMonitor(ctx, host.IPMonitorID); err != nil {
				return err
			}
		}

	}

	// Process creates (updated records are recreated here)
	for _, record := range append(changes.Create, changes.UpdateNew...) {
		log.Debug("ApplyChanges - Create", "record", record)

		host := p.hostsForEndpoint(record)
		hostname, zone := p.trimHostnameFromFQDN(record)
		host.Hostname = hostname
		host.ZoneID = zonesMap[zone]

		switch host.DNSType {
		case "A", "AAAA":
			uri, ok := record.GetProviderSpecificProperty(providerSpecificIPMonitorURI)
			if ok {
				treatRedirects, ok := record.GetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects)
				if ok {
					u := url.URL{Scheme: uri, Host: host.Data}
					hash, err := genRandomHex()
					if err != nil {
						return err
					}
					monitor := Monitor{
						Name:            record.DNSName + "-" + hash,
						URI:             u.String(),
						Hostname:        record.DNSName,
						TreatRedirects:  treatRedirects,
						NameserverSetID: nameserverSets[0].ID, // TODO: fallback to first NS_set if not defined
					}
					mon, err := p.client.CreateMonitor(ctx, monitor)
					if err != nil {
						return err
					}
					host.IPMonitorID = mon.ID
				}
			}
		}

		_, err := p.client.CreateHost(ctx, host)
		if err != nil {
			return err
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

func (p *DnscasterProvider) hostsForEndpoint(record *endpoint.Endpoint) Host {
	ttl := p.defaultTTL(record)

	if len(record.Targets) == 0 {
		// Should not happen
		return Host{}
	}

	return Host{
		Data:    record.Targets[0],
		DNSType: record.RecordType,
		FQDN:    record.DNSName,
		TTL:     ttl,
	}
}

func (p *DnscasterProvider) endpointFromHost(ctx context.Context, host Host) (*endpoint.Endpoint, error) {
	endpoint := endpoint.NewEndpointWithTTL(host.FQDN, host.DNSType, endpoint.TTL(host.TTL), host.Data)

	if host.IPMonitorID != "" {
		monitor, err := p.client.GetMonitor(ctx, host.IPMonitorID)
		if err != nil {
			return nil, err
		}

		u, err := url.Parse(monitor.URI)
		if err != nil {
			return nil, err
		}

		endpoint.SetProviderSpecificProperty(providerSpecificIPMonitorURI, u.Scheme)
		endpoint.SetProviderSpecificProperty(providerSpecificIPMonitorTreatRedirects, monitor.TreatRedirects)
	}
	log.Debug("endpointFromHost", "endpoint", endpoint)

	return endpoint, nil
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
