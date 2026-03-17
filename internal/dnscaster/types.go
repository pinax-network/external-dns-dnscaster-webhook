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
	ZoneID   string `json:"zone_id,omitempty"`
	Data     string `json:"data,omitempty"`
	DNSType  string `json:"dns_type,omitempty"`
	ID       string `json:"id,omitempty"`
	DNSName  string `json:"fqdn,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	TTL      int64  `json:"ttl,omitempty"`
	State    string `json:"state,omitempty"`
}

type UpdateHostRequest struct {
	Host UpdateHostPayload `json:"host"`
}

type UpdateHostPayload struct {
	Data string `json:"data"`
}

type DnscasterErrorResponse struct {
	Message string   `json:"message"`
	Errors  []string `json:"errors"`
}
