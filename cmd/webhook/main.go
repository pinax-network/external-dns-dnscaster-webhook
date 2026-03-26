package main

import (
	"fmt"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/configuration"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/dnsprovider"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/server"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/webhook"
)

const banner = `
external-dns-provider-dnscaster
version: %s (%s)

`

var (
	version = "dev"
	commit  = "none"
)

func main() {
	log.Init()

	log.Info(fmt.Sprintf(banner, version, commit))

	config := configuration.Init()
	provider, err := dnsprovider.Init(config)
	if err != nil {
		log.Fatal("failed to initialize provider: %v", err)
	}

	main, health := server.Init(config, webhook.New(provider))
	server.ShutdownGracefully(main, health)
}
