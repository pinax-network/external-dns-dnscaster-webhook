package dnscaster

type NameserverSet struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type Zone struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
}

type Host struct {
	ZoneID      string `json:"zone_id,omitempty"`
	Data        string `json:"data,omitempty"`
	DNSType     string `json:"dns_type,omitempty"`
	ID          string `json:"id,omitempty"`
	FQDN        string `json:"fqdn,omitempty"`
	Hostname    string `json:"hostname,omitempty"`
	TTL         int64  `json:"ttl,omitempty"`
	State       string `json:"state,omitempty"`
	IPMonitorID string `json:"ip_monitor_id,omitempty"`
}

type Monitor struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	NameserverSetID string `json:"nameserver_set_id,omitempty"`
	TreatRedirects  string `json:"treat_redirects,omitempty"`
	URI             string `json:"uri,omitempty"`
}

type ListResponse[T any] struct {
	Collection []T `json:"collection"`
}

type HostEnvelope struct {
	Host Host `json:"host"`
}

type MonitorEnvelope struct {
	Monitor Monitor `json:"ip_monitor"`
}

type DnscasterErrorResponse struct {
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}
