package dnscaster

type DNSType string

const (
	DNSTypeA     DNSType = "A"
	DNSTypeAAAA  DNSType = "AAAA"
	DNSTypeCNAME DNSType = "CNAME"
	DNSTypeTXT   DNSType = "TXT"
)

type Zone struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
}

type Host struct {
	ID       string `json:"id"`
	ZoneID   string `json:"zone_id"`
	DNSName  string `json:"fqdn"`
	Hostname string `json:"hostname"`
	DNSType  string `json:"dns_type"`
	Data     string `json:"data"`
	TTL      *int64 `json:"ttl"`
	State    string `json:"state"`
}

type CreateHostRequest struct {
	Host CreateHostPayload `json:"host"`
}

type CreateHostPayload struct {
	Data    string  `json:"data"`
	DNSType DNSType `json:"dns_type"`
	ZoneID  string  `json:"zone_id"`
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
