package dnscaster

import (
	"crypto/tls"
	"net/http"
	"net/http/cookiejar"

	"golang.org/x/net/publicsuffix"

	"github.com/gcleroux/external-dns-dnscaster-webhook/internal/log"
)

type DnscasterDefaults struct {
	DefaultTTL     int64  `env:"DNSCASTER_DEFAULT_TTL" envDefault:"300"`
	DefaultComment string `env:"DNSCASTER_DEFAULT_COMMENT" envDefault:""`
}

// DnscasterConnectionConfig holds the connection details for the API client
type DnscasterConnectionConfig struct {
	BaseUrl       string `env:"DNSCASTER_BASEURL,notEmpty"`
	Username      string `env:"DNSCASTER_USERNAME,notEmpty"`
	Password      string `env:"DNSCASTER_PASSWORD,notEmpty"`
	SkipTLSVerify bool   `env:"DNSCASTER_SKIP_TLS_VERIFY" envDefault:"false"`
}

// DnscasterApiClient encapsulates the client configuration and HTTP client
type DnscasterApiClient struct {
	*DnscasterDefaults
	*DnscasterConnectionConfig
	*http.Client
}

// NewDnscasterClient creates a new instance of DnscasterApiClient
func NewDnscasterClient(config *DnscasterConnectionConfig, defaults *DnscasterDefaults) (*DnscasterApiClient, error) {
	log.Info("creating a new Dnscaster API Client")

	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		log.Error("failed to create cookie jar: %v", err)
		return nil, err
	}

	client := &DnscasterApiClient{
		DnscasterDefaults:         defaults,
		DnscasterConnectionConfig: config,
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: config.SkipTLSVerify,
				},
			},
			Jar: jar,
		},
	}

	return client, nil
}
