package dnscaster

import (
	"context"
	"fmt"
	"maps"
	"slices"
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
	dryRun       bool
}

type hostKey struct {
	FQDN   string
	Type   string
	Target string
}

type hostsMap = map[hostKey]Host

type zonesMap = map[string]string

// NewDNScasterProvider initializes a new DNSProvider, of the Dnscaster variety.
// In dry-run mode the provider still reads from the API, but changes are
// logged instead of being sent.
func NewDNScasterProvider(domainFilter *endpoint.DomainFilter, defaults *DNScasterDefaults, config *DNScasterConnectionConfig, dryRun bool) (provider.Provider, error) {
	// Create the Dnscaster API Client
	client, err := NewDNScasterClient(config, defaults)
	if err != nil {
		return nil, fmt.Errorf("failed to create the Dnscaster client: %w", err)
	}

	if dryRun {
		log.Info("dry run enabled: changes are logged instead of being sent to DNScaster")
	}

	// If the client connects properly, create the DNS Provider
	p := &DNScasterProvider{
		client:       client,
		domainFilter: domainFilter,
		dryRun:       dryRun,
	}

	return p, nil
}

func (p *DNScasterProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	hosts, err := p.client.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	return p.recordsForHosts(hosts), nil
}

// recordsForHosts returns the endpoints of the hosts in managed domains.
func (p *DNScasterProvider) recordsForHosts(hosts []Host) []*endpoint.Endpoint {
	records := make([]*endpoint.Endpoint, 0, len(hosts))
	for _, host := range hosts {
		if p.domainFilter.Match(host.FQDN) {
			records = append(records, p.endpointForHost(host))
		}
	}
	return records
}

// ApplyChanges works out every API call the changes need before sending any
// of them. In dry-run mode those calls are logged instead, so only the reads
// needed to work them out reach the API.
func (p *DNScasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	cs, err := p.planChanges(ctx, changes)
	if err != nil {
		return err
	}

	if p.dryRun {
		cs.logDryRun()
		return nil
	}
	return p.execute(ctx, cs)
}

// changeSet holds the API calls that apply a set of changes. An update is a
// delete of the old host followed by a create of the new one.
type changeSet struct {
	deletes []Host
	creates []hostCreate
}

// hostCreate is a host to create, along with the IP monitor to create before
// it. The monitor's ID only exists once it has been created, so it is set on
// the host when the change set is executed.
type hostCreate struct {
	host    Host
	monitor *Monitor
}

// planChanges reads the hosts and zones the changes refer to and works out the
// API calls that apply them. It only reads from the API.
func (p *DNScasterProvider) planChanges(ctx context.Context, changes *plan.Changes) (changeSet, error) {
	deletes := slices.Concat(changes.UpdateOld, changes.Delete)
	creates := slices.Concat(changes.Create, changes.UpdateNew)

	var hosts []Host
	if len(deletes) > 0 {
		var err error
		if hosts, err = p.client.ListHosts(ctx); err != nil {
			return changeSet{}, err
		}
	}

	var zones []Zone
	if len(creates) > 0 {
		var err error
		if zones, err = p.client.ListZones(ctx); err != nil {
			return changeSet{}, err
		}
	}

	return p.newChangeSet(deletes, creates, zones, hosts)
}

// newChangeSet maps the records to delete and create onto API calls, given the
// existing hosts and the zones.
func (p *DNScasterProvider) newChangeSet(deletes, creates []*endpoint.Endpoint, zones []Zone, hosts []Host) (changeSet, error) {
	var cs changeSet

	existing := make(hostsMap, len(hosts))
	for _, h := range hosts {
		existing[hostKey{FQDN: h.FQDN, Type: h.DNSType, Target: h.Data}] = h
	}

	for _, record := range deletes {
		if len(record.Targets) == 0 {
			return changeSet{}, fmt.Errorf("no target set on record: %v", record)
		}
		log.Debug("planDelete", "record", record)

		hk := hostKey{FQDN: record.DNSName, Type: record.RecordType, Target: strings.Trim(record.Targets[0], `\"`)}
		host, ok := existing[hk]
		if !ok {
			// There is nothing to delete, and without a host ID the request
			// would target the hosts collection itself.
			log.Warn("no host found for record to delete, skipping", "record", record)
			continue
		}
		cs.deletes = append(cs.deletes, host)
	}

	managedZones, err := p.filterManagedZones(zones)
	if err != nil {
		return changeSet{}, err
	}

	zoneIDs := make(zonesMap, len(managedZones))
	for _, zone := range managedZones {
		zoneIDs[zone.Domain] = zone.ID
	}

	for _, record := range creates {
		create, err := p.hostToCreate(record, zoneIDs)
		if err != nil {
			return changeSet{}, err
		}
		cs.creates = append(cs.creates, create)
	}

	return cs, nil
}

func (p *DNScasterProvider) hostToCreate(record *endpoint.Endpoint, zoneIDs zonesMap) (hostCreate, error) {
	if len(record.Targets) == 0 {
		return hostCreate{}, fmt.Errorf("no target set on record: %v", record)
	}
	log.Debug("planCreate", "record", record)

	host := p.hostsForEndpoint(record)
	p.applyOwnerProperty(host.Properties)
	hostname, zone := p.trimHostnameFromFQDN(record)
	host.Hostname = hostname
	host.ZoneID = zoneIDs[zone]

	monitor, err := p.monitorForHost(host)
	if err != nil {
		return hostCreate{}, err
	}

	if record.SetIdentifier != "" {
		host.Properties[ProviderMetadataSetIdentifier] = record.SetIdentifier
	}

	return hostCreate{host: host, monitor: monitor}, nil
}

// monitorForHost returns the IP monitor to create for a host, or nil when the
// host does not ask for one.
func (p *DNScasterProvider) monitorForHost(host Host) (*Monitor, error) {
	if host.DNSType != "A" && host.DNSType != "AAAA" {
		return nil, nil
	}

	uri, ok := host.Properties[ProviderSpecificIPMonitorURI]
	if !ok {
		return nil, nil
	}

	hostname := host.Properties[ProviderSpecificIPMonitorHostname]
	if hostname == "" {
		hostname = host.FQDN
	}

	u, err := formatURI(uri, host.Data)
	if err != nil {
		return nil, err
	}

	return &Monitor{
		Name:            host.FQDN,
		URI:             u,
		Hostname:        hostname,
		TreatRedirects:  host.Properties[ProviderSpecificIPMonitorTreatRedirects],
		NameserverSetID: p.client.NameserverSetID,
		// A copy, so properties added to the host afterwards stay off the monitor.
		Properties: maps.Clone(host.Properties),
	}, nil
}

// execute sends the API calls of a change set: every delete, then every create.
func (p *DNScasterProvider) execute(ctx context.Context, cs changeSet) error {
	for _, host := range cs.deletes {
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

	for _, create := range cs.creates {
		if err := p.createHost(ctx, create); err != nil {
			return err
		}
	}
	return nil
}

// createHost creates the host's IP monitor, if it has one, then the host using
// it. The monitor is removed again if the host cannot be created.
func (p *DNScasterProvider) createHost(ctx context.Context, create hostCreate) error {
	host := create.host
	if create.monitor != nil {
		monitor, err := p.client.CreateMonitor(ctx, *create.monitor)
		if err != nil {
			return err
		}
		host.IPMonitorID = monitor.ID
	}

	_, err := p.client.CreateHost(ctx, host)
	if err != nil && host.IPMonitorID != "" {
		_ = p.client.DeleteMonitor(ctx, host.IPMonitorID)
	}
	return err
}

// logDryRun reports the API calls the change set would make.
func (cs changeSet) logDryRun() {
	for _, host := range cs.deletes {
		log.Info("dry run: would delete host",
			"host.id", host.ID, "fqdn", host.FQDN, "type", host.DNSType, "data", host.Data)
		if host.IPMonitorID != "" {
			log.Info("dry run: would delete IP monitor", "monitor.id", host.IPMonitorID, "fqdn", host.FQDN)
		}
	}

	for _, create := range cs.creates {
		if m := create.monitor; m != nil {
			log.Info("dry run: would create IP monitor",
				"name", m.Name, "uri", m.URI, "hostname", m.Hostname,
				"treat_redirects", m.TreatRedirects, "nameserver_set_id", m.NameserverSetID)
		}

		h := create.host
		log.Info("dry run: would create host",
			"fqdn", h.FQDN, "type", h.DNSType, "data", h.Data, "ttl", h.TTL,
			"zone_id", h.ZoneID, "hostname", h.Hostname, "properties", h.Properties)
	}
}

// GetDomainFilter returns the domain filter for the provider.
func (p *DNScasterProvider) GetDomainFilter() endpoint.DomainFilterInterface {
	return p.domainFilter
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

	setID, ok := host.Properties[ProviderMetadataSetIdentifier]
	if ok {
		endpoint.SetIdentifier = setID
	}

	log.Debug("endpointFromHost", "endpoint", endpoint)
	return endpoint
}

func (p *DNScasterProvider) defaultTTL(record *endpoint.Endpoint) int64 {
	if record.RecordTTL.IsConfigured() {
		return int64(record.RecordTTL)
	}
	return p.client.DefaultTTL
}

func (p *DNScasterProvider) applyOwnerProperty(properties map[string]string) {
	if p.client.OwnerID == "" {
		return
	}
	properties[ProviderMetadataOwnerID] = p.client.OwnerID
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
