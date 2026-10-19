package dnscaster

import (
	"net"
	"net/url"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
)

const (
	ProviderMetadataOwnerID       = "owner-id"
	ProviderMetadataSetIdentifier = "set-identifier"
)

const (
	ProviderSpecificLabelPrefix             = "webhook/dnscaster-label-"
	ProviderSpecificIPMonitorURI            = "webhook/dnscaster-ip-monitor-uri"
	ProviderSpecificIPMonitorHostname       = "webhook/dnscaster-ip-monitor-hostname"
	ProviderSpecificIPMonitorTreatRedirects = "webhook/dnscaster-ip-monitor-treat_redirects"
)

var reservedProviderMetadataKeys = map[string]struct{}{
	ProviderMetadataOwnerID:       {},
	ProviderMetadataSetIdentifier: {},
}

var reservedProviderSpecificKeys = map[string]struct{}{
	ProviderSpecificIPMonitorURI:            {},
	ProviderSpecificIPMonitorHostname:       {},
	ProviderSpecificIPMonitorTreatRedirects: {},
}

func getProviderSpecific(props map[string]string) endpoint.ProviderSpecific {
	if len(props) == 0 {
		log.Debug("no properties found")
		return nil
	}

	ps := make(endpoint.ProviderSpecific, 0, len(props))
	for key, value := range props {
		if isReservedProviderMetadataKey(key) {
			// Ignoring metadata key used for implementation logic
			continue
		}
		if isReservedProviderSpecificKey(key) {
			ps = append(ps, endpoint.ProviderSpecificProperty{Name: key, Value: value})
			continue
		}
		ps = append(ps, endpoint.ProviderSpecificProperty{
			Name:  ProviderSpecificLabelPrefix + escapeLabelKey(key),
			Value: value,
		})
	}
	return ps
}

func extractProperties(ps endpoint.ProviderSpecific) map[string]string {
	properties := make(map[string]string)
	for _, p := range ps {
		if isReservedProviderSpecificKey(p.Name) {
			properties[p.Name] = p.Value
			continue
		}
		label, ok := strings.CutPrefix(p.Name, ProviderSpecificLabelPrefix)
		if !ok {
			log.Debug("ignoring provider-specific", "name", p.Name, "value", p.Value)
			continue
		}
		properties[unescapeLabelKey(label)] = p.Value
	}
	return properties
}

func isReservedProviderMetadataKey(k string) bool {
	_, ok := reservedProviderMetadataKeys[k]
	return ok
}

func isReservedProviderSpecificKey(k string) bool {
	_, ok := reservedProviderSpecificKeys[k]
	return ok
}

func escapeLabelKey(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

func unescapeLabelKey(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	s = strings.ReplaceAll(s, "~0", "~")
	return s
}

// monitorURL parses a monitor URI. A bare scheme such as "ping" is read as
// the scheme rather than as a path.
func monitorURL(uri string) (*url.URL, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" {
		u.Scheme = u.Path
		u.Path = ""
	}
	return u, nil
}

// formatURI fills in the host of a monitor URI that has none with target,
// keeping any port. IPv6 targets are bracketed, as DNScaster expects.
func formatURI(uri string, target string) (string, error) {
	u, err := monitorURL(uri)
	if err != nil {
		return "", err
	}
	if u.Hostname() == "" {
		u.Host = joinHostPort(target, u.Port())
	}
	return u.String(), nil
}

// joinHostPort is net.JoinHostPort that also brackets an IPv6 host without a
// port.
func joinHostPort(host, port string) string {
	if port != "" {
		return net.JoinHostPort(host, port)
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}
