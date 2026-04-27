package dnscaster

import (
	"net/url"
	"strings"

	"sigs.k8s.io/external-dns/endpoint"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
)

const (
	ProviderSpecificLabelPrefix             = "webhook/dnscaster-label-"
	ProviderSpecificIPMonitorURI            = "webhook/dnscaster-ip-monitor-uri"
	ProviderSpecificIPMonitorHostname       = "webhook/dnscaster-ip-monitor-hostname"
	ProviderSpecificIPMonitorTreatRedirects = "webhook/dnscaster-ip-monitor-treat_redirects"
)

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
		if key == "set-identifier" {
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
	s = strings.ReplaceAll(s, "~0", "~")
	s = strings.ReplaceAll(s, "~1", "/")
	return s
}

func formatURI(uri string, host string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	// Default to scheme instead of path
	if u.Scheme == "" {
		u.Scheme = u.Path
		u.Path = ""
	}
	if u.Host == "" {
		u.Host = host
	}
	return u.String(), nil
}
