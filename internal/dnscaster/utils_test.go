package dnscaster_test

import (
	"testing"

	"sigs.k8s.io/external-dns/provider"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnscaster"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnsprovider"
)

func newTestProvider(t *testing.T) (provider.Provider, *dnscaster.FakeDNScasterClient) {
	t.Helper()

	config := baseConfig()
	connConfig := baseConnConfig()
	defaults := baseDefaults()

	client, fake, err := dnscaster.NewFakeDnscasterClient(connConfig, defaults)
	if err != nil {
		t.Fatalf("creating fake dnscaster client failed: %v", err)
	}

	p, err := dnsprovider.InitWithClient(config, client)
	if err != nil {
		t.Fatalf("failed to init provider: %v", err)
	}

	return p, fake
}

func newTestProviderWithConfig(
	t *testing.T,
	config configuration.Config,
	connConfig *dnscaster.DNScasterConnectionConfig,
	defaults *dnscaster.DNScasterDefaults,
) (provider.Provider, *dnscaster.FakeDNScasterClient) {
	t.Helper()

	client, fake, err := dnscaster.NewFakeDnscasterClient(connConfig, defaults)
	if err != nil {
		t.Fatalf("creating fake dnscaster client failed: %v", err)
	}

	p, err := dnsprovider.InitWithClient(config, client)
	if err != nil {
		t.Fatalf("failed to init provider: %v", err)
	}

	return p, fake
}

func baseConfig() configuration.Config {
	cfg := configuration.Init()
	cfg.DomainFilter = []string{"example.com"}
	return cfg
}

func baseConnConfig() *dnscaster.DNScasterConnectionConfig {
	return &dnscaster.DNScasterConnectionConfig{
		ApiKey:          "k",
		NameserverSetID: "ns-1",
	}
}

func baseDefaults() *dnscaster.DNScasterDefaults {
	return &dnscaster.DNScasterDefaults{
		DefaultTTL: 600,
	}
}
