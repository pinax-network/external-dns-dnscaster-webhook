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

// hostKey identifies a host the way external-dns identifies a record.
type hostKey struct {
	FQDN   string
	Type   string
	Target string
}

// hostIndex holds the owner's hosts by hostKey.
type hostIndex map[hostKey]Host

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

// ApplyChanges applies deletions, then updates, then creations, so a target
// freed by a deletion is available to the changes after it. Updates change
// the existing host and its monitor in place: UpdateOld[i] and UpdateNew[i]
// describe the same record, as external-dns' plan builds them.
func (p *DNScasterProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	if len(changes.UpdateOld) != len(changes.UpdateNew) {
		return fmt.Errorf("malformed changes: %d records to update but %d desired states", len(changes.UpdateOld), len(changes.UpdateNew))
	}

	// Deletions and updates both need the ID of the host behind a record, so
	// the owner's hosts are listed once for the whole batch.
	var hosts hostIndex
	if len(changes.Delete)+len(changes.UpdateOld) > 0 {
		var err error
		if hosts, err = p.indexHosts(ctx); err != nil {
			return err
		}
	}

	for _, record := range changes.Delete {
		log.Debug("applyDelete", "record", record)

		host, err := hosts.find(record)
		if err != nil {
			return err
		}
		if err := p.deleteHost(ctx, host); err != nil {
			return err
		}
	}

	creates := changes.Create
	for i, record := range changes.UpdateOld {
		host, err := hosts.find(record)
		if err != nil {
			return err
		}
		desired, err := p.hostForEndpoint(changes.UpdateNew[i])
		if err != nil {
			return err
		}

		// An update leaves out empty fields, so it cannot detach a monitor
		// from its host, and a monitor in use cannot be deleted. A host
		// losing its monitor is replaced instead.
		if host.IPMonitorID != "" && !wantsMonitor(desired) {
			log.Debug("applyUpdate", "replacing host losing its monitor", host.ID, "record", changes.UpdateNew[i])
			if err := p.deleteHost(ctx, host); err != nil {
				return err
			}
			creates = append(creates, changes.UpdateNew[i])
			continue
		}

		if err := p.applyUpdate(ctx, host, desired); err != nil {
			return err
		}
	}

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
	log.Debug("applyCreate", "record", record)

	host, err := p.hostForEndpoint(record)
	if err != nil {
		return err
	}

	zoneID, zoneName := zones.FindZone(record.DNSName)
	host.Hostname = hostnameInZone(record.DNSName, zoneName)
	host.ZoneID = zoneID

	monitorID, err := p.applyMonitor(ctx, host, "")
	if err != nil {
		return err
	}
	host.IPMonitorID = monitorID

	_, err = p.client.CreateHost(ctx, host)
	if err != nil && monitorID != "" {
		_ = p.client.DeleteMonitor(ctx, monitorID)
	}
	return err
}

// applyUpdate changes host in place so it matches desired, along with its
// monitor. The host keeps its ID, zone and hostname: an update is only ever
// sent for the record's own FQDN.
func (p *DNScasterProvider) applyUpdate(ctx context.Context, host Host, desired Host) error {
	log.Debug("applyUpdate", "host.id", host.ID, "desired", desired)

	// The monitor goes first. Should the host update then fail, external-dns
	// sends the whole update again on its next sync, whereas a host updated
	// ahead of a failed monitor would look up to date and leave the monitor
	// stale.
	monitorID, err := p.applyMonitor(ctx, desired, host.IPMonitorID)
	if err != nil {
		return err
	}

	// The FQDN is read-only and the zone does not change, so only the fields
	// that can differ are sent. Properties are replaced as a whole.
	_, err = p.client.UpdateHost(ctx, host.ID, Host{
		Data:        desired.Data,
		TTL:         desired.TTL,
		Properties:  desired.Properties,
		IPMonitorID: monitorID,
	})
	if err != nil && monitorID != "" && host.IPMonitorID == "" {
		// The host never got its new monitor, so it is not left orphaned.
		_ = p.client.DeleteMonitor(ctx, monitorID)
	}
	return err
}

// deleteHost removes host, then the monitor it used: the API refuses to
// delete a monitor that is in use.
func (p *DNScasterProvider) deleteHost(ctx context.Context, host Host) error {
	if err := p.client.DeleteHost(ctx, host.ID); err != nil {
		return err
	}

	if host.IPMonitorID != "" {
		return p.client.DeleteMonitor(ctx, host.IPMonitorID)
	}
	return nil
}

// applyMonitor brings the monitor of host in line with its annotations and
// returns the monitor's ID: a new monitor is created when monitorID is empty,
// the existing one is updated in place otherwise. The returned ID is empty
// when host wants no monitor.
func (p *DNScasterProvider) applyMonitor(ctx context.Context, host Host, monitorID string) (string, error) {
	if !wantsMonitor(host) {
		return "", nil
	}

	monitor, err := p.monitorForHost(host)
	if err != nil {
		return "", err
	}

	if monitorID == "" {
		created, err := p.client.CreateMonitor(ctx, monitor)
		return created.ID, err
	}

	_, err = p.client.UpdateMonitor(ctx, monitorID, monitor)
	return monitorID, err
}

// indexHosts lists the owner's hosts and indexes them by hostKey.
func (p *DNScasterProvider) indexHosts(ctx context.Context) (hostIndex, error) {
	hosts, err := p.client.ListHosts(ctx)
	if err != nil {
		return nil, err
	}

	index := make(hostIndex, len(hosts))
	for _, h := range hosts {
		index[hostKey{FQDN: h.FQDN, Type: h.DNSType, Target: h.Data}] = h
	}
	return index, nil
}

// find returns the host behind record.
func (index hostIndex) find(record *endpoint.Endpoint) (Host, error) {
	if len(record.Targets) == 0 {
		return Host{}, fmt.Errorf("no target set on record: %v", record)
	}

	key := hostKey{FQDN: record.DNSName, Type: record.RecordType, Target: strings.Trim(record.Targets[0], `\"`)}
	host, ok := index[key]
	if !ok {
		return Host{}, fmt.Errorf("no host found for record: %v", record)
	}
	return host, nil
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

// hostForEndpoint builds the host record asks for, with the properties the
// provider adds. The zone and hostname are left for applyCreate to fill in.
func (p *DNScasterProvider) hostForEndpoint(record *endpoint.Endpoint) (Host, error) {
	if len(record.Targets) == 0 {
		return Host{}, fmt.Errorf("no target set on record: %v", record)
	}

	properties := extractProperties(record.ProviderSpecific)
	if p.config.OwnerID != "" {
		properties[ProviderMetadataOwnerID] = p.config.OwnerID
	}
	if record.SetIdentifier != "" {
		properties[ProviderMetadataSetIdentifier] = record.SetIdentifier
	}

	return Host{
		Data:       record.Targets[0],
		DNSType:    record.RecordType,
		FQDN:       record.DNSName,
		TTL:        p.defaultTTL(record),
		Properties: properties,
	}, nil
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

// wantsMonitor reports whether host asks for a monitor through its
// annotations. Only A and AAAA records can be monitored.
func wantsMonitor(host Host) bool {
	if host.DNSType != "A" && host.DNSType != "AAAA" {
		return false
	}
	_, ok := host.Properties[ProviderSpecificIPMonitorURI]
	return ok
}

// monitorForHost builds the monitor host asks for through its annotations.
func (p *DNScasterProvider) monitorForHost(host Host) (Monitor, error) {
	uri, err := formatURI(host.Properties[ProviderSpecificIPMonitorURI], host.Data)
	if err != nil {
		return Monitor{}, err
	}

	hostname := host.Properties[ProviderSpecificIPMonitorHostname]
	if hostname == "" {
		hostname = host.FQDN
	}

	// The default is sent explicitly: an update leaves omitted fields as they
	// are, so a monitor whose annotation was removed would keep its old value.
	treatRedirects := host.Properties[ProviderSpecificIPMonitorTreatRedirects]
	if treatRedirects == "" {
		treatRedirects = defaultTreatRedirects
	}

	return Monitor{
		Name:            host.FQDN,
		URI:             uri,
		Hostname:        hostname,
		TreatRedirects:  treatRedirects,
		NameserverSetID: p.config.NameserverSetID,
		Properties:      host.Properties,
	}, nil
}

func (p *DNScasterProvider) defaultTTL(record *endpoint.Endpoint) int64 {
	if record.RecordTTL.IsConfigured() {
		return int64(record.RecordTTL)
	}
	return p.defaults.DefaultTTL
}

// hostnameInZone returns fqdn relative to zone, or "" for the zone apex.
func hostnameInZone(fqdn, zone string) string {
	if zone == "" || fqdn == zone {
		return ""
	}
	return strings.TrimSuffix(fqdn, "."+zone)
}
