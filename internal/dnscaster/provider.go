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

// Client is the subset of the DNScaster API the provider uses.
// DNScasterApiClient implements it, and dnscastertest provides an in-memory
// fake for tests.
type Client interface {
	ListZones(ctx context.Context) ([]Zone, error)
	ListHosts(ctx context.Context) ([]Host, error)
	CreateHost(ctx context.Context, host Host) (Host, error)
	UpdateHost(ctx context.Context, hostID string, host Host) (Host, error)
	DeleteHost(ctx context.Context, hostID string) error
	CreateMonitor(ctx context.Context, monitor Monitor) (Monitor, error)
	UpdateMonitor(ctx context.Context, monitorID string, monitor Monitor) (Monitor, error)
	DeleteMonitor(ctx context.Context, monitorID string) error
}

// DNScasterProvider is a helper class for working with dnscaster
type DNScasterProvider struct {
	provider.BaseProvider

	client       Client
	config       *DNScasterConnectionConfig
	defaults     *DNScasterDefaults
	domainFilter *endpoint.DomainFilter
}

type hostKey struct {
	FQDN   string
	Type   string
	Target string
}

type hostsMap = map[hostKey]Host

// NewDNScasterProvider initializes a new DNSProvider, of the Dnscaster variety
func NewDNScasterProvider(domainFilter *endpoint.DomainFilter, defaults *DNScasterDefaults, config *DNScasterConnectionConfig, client Client) provider.Provider {
	return &DNScasterProvider{
		client:       client,
		config:       config,
		defaults:     defaults,
		domainFilter: domainFilter,
	}
}

func (p *DNScasterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	hosts, err := p.client.ListHosts(ctx)
	if err != nil {
		return nil, err
	}

	records := make([]*endpoint.Endpoint, 0, len(hosts))
	for _, host := range hosts {
		if p.domainFilter.Match(host.FQDN) {
			records = append(records, p.endpointForHost(host))
		}
	}
	return records, nil
}

func (p *DNScasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	hostsMap := make(hostsMap)

	// Process deletions (records to update will be deleted and recreated later)
	for _, record := range append(changes.UpdateOld, changes.Delete...) {
		if err := p.applyDelete(ctx, record, hostsMap); err != nil {
			return err
		}
	}

	// Process creates (updated records are recreated here)
	creates := append(changes.Create, changes.UpdateNew...)
	if len(creates) == 0 {
		return nil
	}

	zones, err := p.managedZones(ctx)
	if err != nil {
		return err
	}

	for _, record := range creates {
		if err := p.applyCreate(ctx, record, zones); err != nil {
			return err
		}
	}

	return nil
}

// GetDomainFilter returns the domain filter for the provider.
func (p *DNScasterProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
}

func (p *DNScasterProvider) applyCreate(ctx context.Context, record *endpoint.Endpoint, zones provider.ZoneIDName) error {
	if len(record.Targets) == 0 {
		return fmt.Errorf("no target set on record: %v", record)
	}
	log.Debug("applyCreate", "record", record)

	host := p.hostsForEndpoint(record)
	p.applyOwnerProperty(host.Properties)

	zoneID, zoneName := zones.FindZone(record.DNSName)
	host.Hostname = hostnameInZone(record.DNSName, zoneName)
	host.ZoneID = zoneID

	monitor, err := p.createMonitorForHost(ctx, host)
	if err != nil {
		return err
	}
	host.IPMonitorID = monitor.ID

	if record.SetIdentifier != "" {
		host.Properties[ProviderMetadataSetIdentifier] = record.SetIdentifier
	}

	_, err = p.client.CreateHost(ctx, host)
	if err != nil && monitor.ID != "" {
		_ = p.client.DeleteMonitor(ctx, monitor.ID)
	}
	return err
}

func (p *DNScasterProvider) applyDelete(ctx context.Context, record *endpoint.Endpoint, hostsMap hostsMap) error {
	if len(record.Targets) == 0 {
		return fmt.Errorf("no target set on record: %v", record)
	}
	log.Debug("applyDelete", "record", record)

	hk := hostKey{FQDN: record.DNSName, Type: record.RecordType, Target: strings.Trim(record.Targets[0], `\"`)}
	host, ok := hostsMap[hk]
	if !ok {
		hosts, err := p.client.ListHosts(ctx)
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

// managedZones returns the DNScaster zones matching the domain filter.
func (p *DNScasterProvider) managedZones(ctx context.Context) (provider.ZoneIDName, error) {
	zones, err := p.client.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	managed := provider.ZoneIDName{}
	for _, zone := range zones {
		if !p.domainFilter.Match(zone.Domain) {
			log.Debug("managedZones", "skipping zone as it does not match domain filter", zone.Domain)
			continue
		}

		managed.Add(zone.ID, zone.Domain)
	}
	log.Debug("managedZones", "total managed zones", len(managed))
	return managed, nil
}

func (p *DNScasterProvider) hostsForEndpoint(record *endpoint.Endpoint) Host {
	ttl := p.defaultTTL(record)

	if len(record.Targets) == 0 {
		// Should not happen
		return Host{}
	}

	return Host{
		Data:       record.Targets[0],
		DNSType:    record.RecordType,
		FQDN:       record.DNSName,
		TTL:        ttl,
		Properties: extractProperties(record.ProviderSpecific),
	}
}

func (p *DNScasterProvider) endpointForHost(host Host) *endpoint.Endpoint {
	endpoint := endpoint.NewEndpointWithTTL(host.FQDN, host.DNSType, endpoint.TTL(host.TTL), host.Data)
	endpoint.ProviderSpecific = getProviderSpecific(host.Properties)

	setID, ok := host.Properties[ProviderMetadataSetIdentifier]
	if ok {
		endpoint.SetIdentifier = setID
	}

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

	monitor, err := p.client.CreateMonitor(ctx, Monitor{
		Name:            host.FQDN,
		URI:             u,
		Hostname:        hostname,
		TreatRedirects:  host.Properties[ProviderSpecificIPMonitorTreatRedirects],
		NameserverSetID: p.config.NameserverSetID,
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
	return p.defaults.DefaultTTL
}

func (p *DNScasterProvider) applyOwnerProperty(properties map[string]string) {
	if p.config.OwnerID == "" {
		return
	}
	properties[ProviderMetadataOwnerID] = p.config.OwnerID
}

// hostnameInZone returns fqdn relative to zone, or "" for the zone apex.
func hostnameInZone(fqdn, zone string) string {
	if zone == "" || fqdn == zone {
		return ""
	}
	return strings.TrimSuffix(fqdn, "."+zone)
}
