package dnscaster

import (
	"context"
	"fmt"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
)

// DNScasterProvider is a helper class for working with dnscaster
type DNScasterProvider struct {
	provider.BaseProvider

	client       *DNScasterApiClient
	domainFilter *endpoint.DomainFilter
}

type hostKey struct {
	FQDN   string
	Type   string
	Target string
}

type hostsMap = map[hostKey]Host

type zonesMap = map[string]string

// NewDNScasterProvider initializes a new DNSProvider, of the Dnscaster variety
func NewDNScasterProvider(domainFilter *endpoint.DomainFilter, defaults *DNScasterDefaults, config *DNScasterConnectionConfig) (provider.Provider, error) {
	// Create the Dnscaster API Client
	client, err := NewDNScasterClient(config, defaults)
	if err != nil {
		return nil, fmt.Errorf("failed to create the Dnscaster client: %w", err)
	}

	// If the client connects properly, create the DNS Provider
	p := &DNScasterProvider{
		client:       client,
		domainFilter: domainFilter,
	}

	return p, nil
}

func NewDNScasterProviderWithClient(domainFilter *endpoint.DomainFilter, defaults *DNScasterDefaults, client *DNScasterApiClient) (provider.Provider, error) {
	return &DNScasterProvider{
		domainFilter: domainFilter,
		client:       client,
	}, nil
}

func (p *DNScasterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	managedZones, err := p.filterManagedZones(zones)
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
			records = append(records, p.endpointForHost(host))
		}
	}
	return records, nil
}

func (p *DNScasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return err
	}

	managedZones, err := p.filterManagedZones(zones)
	if err != nil {
		return err
	}

	zonesMap := make(zonesMap, len(managedZones))
	for _, zone := range managedZones {
		zonesMap[zone.Domain] = zone.ID
	}

	hostsMap := make(hostsMap)

	// Process deletions (records to update will be deleted and recreated later)
	for _, record := range append(changes.UpdateOld, changes.Delete...) {
		if err := p.applyDelete(ctx, record, zonesMap, hostsMap); err != nil {
			return err
		}
	}

	// Process creates (updated records are recreated here)
	for _, record := range append(changes.Create, changes.UpdateNew...) {
		if err := p.applyCreate(ctx, record, zonesMap); err != nil {
			return err
		}
	}

	return nil
}

// GetDomainFilter returns the domain filter for the provider.
func (p *DNScasterProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

func (p *DNScasterProvider) applyCreate(ctx context.Context, record *endpoint.Endpoint, zonesMap zonesMap) error {
	if len(record.Targets) == 0 {
		return fmt.Errorf("no target set on record: %v", record)
	}
	log.Debug("applyCreate", "record", record)

	host := p.hostsForEndpoint(record)
	hostname, zone := p.trimHostnameFromFQDN(record)
	host.Hostname = hostname
	host.ZoneID = zonesMap[zone]

	monitor, err := p.createMonitorForHost(ctx, host)
	if err != nil {
		return err
	}
	host.IPMonitorID = monitor.ID

	_, err = p.client.CreateHost(ctx, host)
	if err != nil && monitor.ID != "" {
		_ = p.client.DeleteMonitor(ctx, monitor.ID)
	}
	return err
}

func (p *DNScasterProvider) applyDelete(ctx context.Context, record *endpoint.Endpoint, zonesMap zonesMap, hostsMap hostsMap) error {
	if len(record.Targets) == 0 {
		return fmt.Errorf("no target set on record: %v", record)
	}
	log.Debug("applyDelete", "record", record)

	hk := hostKey{FQDN: record.DNSName, Type: record.RecordType, Target: strings.Trim(record.Targets[0], `\"`)}
	host, ok := hostsMap[hk]
	if !ok {
		// Will need to add the zone's hosts to map
		_, zone := p.trimHostnameFromFQDN(record)
		hosts, err := p.client.ListHosts(ctx, zonesMap[zone])
		if err != nil {
			return err
		}

		for _, h := range hosts {
			hk := hostKey{FQDN: h.FQDN, Type: h.DNSType, Target: h.Data}
			hostsMap[hk] = h
		}

		// Set the host now that the map is populated
		host = hostsMap[hk]
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
	return nil
}

func (p *DNScasterProvider) filterManagedZones(zones []Zone) ([]Zone, error) {
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

func (p *DNScasterProvider) hostsForEndpoint(record *endpoint.Endpoint) Host {
	ttl := p.defaultTTL(record)

	if len(record.Targets) == 0 {
		// Should not happen
		return Host{}
	}

	hostname, _ := p.trimHostnameFromFQDN(record)
	return Host{
		Data:       record.Targets[0],
		DNSType:    record.RecordType,
		FQDN:       record.DNSName,
		TTL:        ttl,
		Hostname:   hostname,
		Properties: extractProperties(record.ProviderSpecific),
	}
}

func (p *DNScasterProvider) endpointForHost(host Host) *endpoint.Endpoint {
	endpoint := endpoint.NewEndpointWithTTL(host.FQDN, host.DNSType, endpoint.TTL(host.TTL), host.Data)
	endpoint.ProviderSpecific = getProviderSpecific(host.Properties)

	log.Debug("endpointFromHost", "endpoint", endpoint)
	return endpoint
}

func (p *DNScasterProvider) createMonitorForHost(ctx context.Context, host Host) (Monitor, error) {
	if host.DNSType != "A" && host.DNSType != "AAAA" {
		return Monitor{}, nil
	}

	uri, ok := host.Properties[ProviderSpecificIPMonitorURI]
	if !ok {
		return Monitor{}, nil
	}

	hostname := host.Properties[ProviderSpecificIPMonitorHostname]
	if hostname == "" {
		hostname = host.FQDN
	}

	u, err := formatURI(uri, host.Data)
	if err != nil {
		return Monitor{}, err
	}

	// u := url.URL{Scheme: uriScheme, Host: host.Data, Path: host.Properties[ProviderSpecificIPMonitorURIPath]}

	hash, err := genRandomHex()
	if err != nil {
		return Monitor{}, err
	}

	monitor, err := p.client.CreateMonitor(ctx, Monitor{
		Name:            host.FQDN + "-" + hash,
		URI:             u,
		Hostname:        hostname,
		TreatRedirects:  host.Properties[ProviderSpecificIPMonitorTreatRedirects],
		NameserverSetID: p.client.NameserverSetID,
		Properties:      host.Properties,
	})
	if err != nil {
		return Monitor{}, err
	}

	return monitor, nil
}

func (p *DNScasterProvider) defaultTTL(record *endpoint.Endpoint) int64 {
	if record.RecordTTL.IsConfigured() {
		return int64(record.RecordTTL)
	}
	return p.client.DefaultTTL
}

func (p *DNScasterProvider) trimHostnameFromFQDN(record *endpoint.Endpoint) (string, string) {
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
