package dnscaster

type ListZonesResponse struct {
	Zones []Zone `json:"collection"`
}

type Zone struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
}

type ListHostsResponse struct {
	Hosts []Host `json:"collection"`
}

type UpsertHostRequest struct {
	Host Host `json:"host"`
}

type Host struct {
	ZoneID      string `json:"zone_id,omitempty"`
	Data        string `json:"data,omitempty"`
	DNSType     string `json:"dns_type,omitempty"`
	ID          string `json:"id,omitempty"`
	DNSName     string `json:"fqdn,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	TTL         int64  `json:"ttl,omitempty"`
	State       string `json:"state,omitempty"`
	IPMonitorID string `json:"ip_monitor_id,omitempty"`
}

type ListDNScasterResponse[T any] struct {
	Collection []T `json:"collection"`
}

type UpdateHostRequest struct {
	Host UpdateHostPayload `json:"host"`
}

type UpdateHostPayload struct {
	Data string `json:"data"`
}

type ListMonitorsResponse struct {
	Monitors []Monitor `json:"collection"`
}

type UpsertMonitorRequest struct {
	Monitor Monitor `json:"ip_monitor,omitempty"`
}

type Monitor struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	NameserverSetID string `json:"nameserver_set_id,omitempty"`
	TreatRedirects  string `json:"treat_redirects,omitempty"`
	URI             string `json:"uri,omitempty"`
}

type NameserverSet struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type DnscasterErrorResponse struct {
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}
