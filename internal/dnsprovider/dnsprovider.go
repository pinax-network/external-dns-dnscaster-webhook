package dnsprovider

import (
	"fmt"
	"regexp"
	"strings"

	env "github.com/caarlos0/env/v11"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/provider"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/dnscaster"
	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

func Init(config configuration.Config) (provider.Provider, error) {
	var domainFilter *endpoint.DomainFilter

	createMsg := "creating dnscaster provider with "

	if config.RegexDomainFilter != "" {
		createMsg += fmt.Sprintf("regexp domain filter: '%s', ", config.RegexDomainFilter)
		if config.RegexDomainExclusion != "" {
			createMsg += fmt.Sprintf("with exclusion: '%s', ", config.RegexDomainExclusion)
		}
		domainFilter = endpoint.NewRegexDomainFilter(
			regexp.MustCompile(config.RegexDomainFilter),
			regexp.MustCompile(config.RegexDomainExclusion),
		)
	} else {
		if len(config.DomainFilter) > 0 {
			createMsg += fmt.Sprintf("domain filter: '%s', ", strings.Join(config.DomainFilter, ","))
		}
		if len(config.ExcludeDomains) > 0 {
			createMsg += fmt.Sprintf("exclude domain filter: '%s', ", strings.Join(config.ExcludeDomains, ","))
		}
		domainFilter = endpoint.NewDomainFilterWithExclusions(config.DomainFilter, config.ExcludeDomains)
	}

	createMsg = strings.TrimSuffix(createMsg, ", ")
	if strings.HasSuffix(createMsg, "with ") {
		createMsg += "no kind of domain filters"
	}
	log.Info(createMsg)

	dnscasterConfig := dnscaster.DnscasterConnectionConfig{}
	if err := env.Parse(&dnscasterConfig); err != nil {
		return nil, fmt.Errorf("reading dnscaster configuration failed: %v", err)
	}

	dnscasterDefaults := dnscaster.DnscasterDefaults{}
	if err := env.Parse(&dnscasterDefaults); err != nil {
		return nil, fmt.Errorf("reading dnscaster defaults failed: %v", err)
	}

	return dnscaster.NewDnscasterProvider(domainFilter, &dnscasterDefaults, &dnscasterConfig)
}
