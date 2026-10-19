// Package dnscastertest provides an in-memory fake of the DNScaster API for
// testing the provider.
package dnscastertest

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster"
)

var _ dnscaster.Client = (*Client)(nil)

// Client is an in-memory dnscaster.Client. It keeps zones, hosts and monitors
// in maps and assigns IDs h-1, h-2, ... and m-1, m-2, ... in order.
type Client struct {
	mu sync.Mutex

	// ownerID scopes ListHosts, as the owner property filter does on the API.
	ownerID string

	Zones    []dnscaster.Zone
	Hosts    map[string][]dnscaster.Host  // key: zone_id
	Monitors map[string]dnscaster.Monitor // key: mon_id

	nextHostID    int
	nextMonitorID int

	// Optional hooks for test-specific assertions or failures. A non-nil
	// error fails the call.
	OnListZones     func() error
	OnListHosts     func() error
	OnCreateMonitor func(mon dnscaster.Monitor) error
	OnCreateHost    func(host dnscaster.Host) error
	OnUpdateHost    func(hostID string, host dnscaster.Host) error
	OnDeleteHost    func(hostID string) error
	OnUpdateMonitor func(monitorID string, mon dnscaster.Monitor) error
	OnDeleteMonitor func(monitorID string) error
}

// NewClient returns an empty fake. ListHosts only returns hosts whose owner
// property is ownerID, as the API's owner filter does.
func NewClient(ownerID string) *Client {
	return &Client{
		ownerID:       ownerID,
		Hosts:         make(map[string][]dnscaster.Host),
		Monitors:      make(map[string]dnscaster.Monitor),
		nextHostID:    1,
		nextMonitorID: 1,
	}
}

// WithZone adds a zone. It returns c so calls can be chained.
func (c *Client) WithZone(id, domain string) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Zones = append(c.Zones, dnscaster.Zone{ID: id, Domain: domain})
	return c
}

// WithHost adds an existing host, keeping its ID.
func (c *Client) WithHost(host dnscaster.Host) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Hosts[host.ZoneID] = append(c.Hosts[host.ZoneID], host)
	return c
}

// WithMonitor adds an existing monitor, keeping its ID.
func (c *Client) WithMonitor(m dnscaster.Monitor) *Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Monitors[m.ID] = m
	return c
}

func (c *Client) ListZones(context.Context) ([]dnscaster.Zone, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnListZones != nil {
		if err := c.OnListZones(); err != nil {
			return nil, err
		}
	}

	return slices.Clone(c.Zones), nil
}

func (c *Client) ListHosts(context.Context) ([]dnscaster.Host, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnListHosts != nil {
		if err := c.OnListHosts(); err != nil {
			return nil, err
		}
	}

	var hosts []dnscaster.Host
	for _, zoneHosts := range c.Hosts {
		for _, host := range zoneHosts {
			if host.Properties[dnscaster.ProviderMetadataOwnerID] == c.ownerID {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts, nil
}

func (c *Client) CreateHost(_ context.Context, host dnscaster.Host) (dnscaster.Host, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnCreateHost != nil {
		if err := c.OnCreateHost(host); err != nil {
			return dnscaster.Host{}, err
		}
	}

	// The API stores its own copy of the request.
	host.Properties = maps.Clone(host.Properties)
	host.ID = fmt.Sprintf("h-%d", c.nextHostID)
	c.nextHostID++

	c.Hosts[host.ZoneID] = append(c.Hosts[host.ZoneID], host)
	return host, nil
}

// UpdateHost applies the fields set in host to the stored host.
func (c *Client) UpdateHost(_ context.Context, hostID string, host dnscaster.Host) (dnscaster.Host, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnUpdateHost != nil {
		if err := c.OnUpdateHost(hostID, host); err != nil {
			return dnscaster.Host{}, err
		}
	}

	for zoneID, hosts := range c.Hosts {
		for i, stored := range hosts {
			if stored.ID != hostID {
				continue
			}
			if host.Properties != nil {
				stored.Properties = nil
			}
			updated, err := patch(stored, host)
			if err != nil {
				return dnscaster.Host{}, err
			}
			c.Hosts[zoneID][i] = updated
			return updated, nil
		}
	}
	return dnscaster.Host{}, fmt.Errorf("host %q not found", hostID)
}

func (c *Client) DeleteHost(_ context.Context, hostID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnDeleteHost != nil {
		if err := c.OnDeleteHost(hostID); err != nil {
			return err
		}
	}

	for zoneID, hosts := range c.Hosts {
		for i, h := range hosts {
			if h.ID == hostID {
				c.Hosts[zoneID] = slices.Delete(hosts, i, i+1)
				return nil
			}
		}
	}
	return fmt.Errorf("host %q not found", hostID)
}

func (c *Client) CreateMonitor(_ context.Context, mon dnscaster.Monitor) (dnscaster.Monitor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnCreateMonitor != nil {
		if err := c.OnCreateMonitor(mon); err != nil {
			return dnscaster.Monitor{}, err
		}
	}

	mon.Properties = maps.Clone(mon.Properties)
	mon.ID = fmt.Sprintf("m-%d", c.nextMonitorID)
	c.nextMonitorID++

	c.Monitors[mon.ID] = mon
	return mon, nil
}

// UpdateMonitor applies the fields set in mon to the stored monitor.
func (c *Client) UpdateMonitor(_ context.Context, monitorID string, mon dnscaster.Monitor) (dnscaster.Monitor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnUpdateMonitor != nil {
		if err := c.OnUpdateMonitor(monitorID, mon); err != nil {
			return dnscaster.Monitor{}, err
		}
	}

	stored, ok := c.Monitors[monitorID]
	if !ok {
		return dnscaster.Monitor{}, fmt.Errorf("monitor %q not found", monitorID)
	}
	if mon.Properties != nil {
		stored.Properties = nil
	}
	updated, err := patch(stored, mon)
	if err != nil {
		return dnscaster.Monitor{}, err
	}
	c.Monitors[monitorID] = updated
	return updated, nil
}

func (c *Client) DeleteMonitor(_ context.Context, monitorID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.OnDeleteMonitor != nil {
		if err := c.OnDeleteMonitor(monitorID); err != nil {
			return err
		}
	}

	if _, ok := c.Monitors[monitorID]; !ok {
		return fmt.Errorf("monitor %q not found", monitorID)
	}
	delete(c.Monitors, monitorID)
	return nil
}

// patch overlays update onto stored the way the API applies a PUT: fields
// left out of the JSON body keep their value. Properties are replaced as a
// whole on the API, so callers clear them first when update carries some.
func patch[T any](stored, update T) (T, error) {
	body, err := json.Marshal(update)
	if err != nil {
		return stored, err
	}
	if err := json.Unmarshal(body, &stored); err != nil {
		return stored, err
	}
	return stored, nil
}
