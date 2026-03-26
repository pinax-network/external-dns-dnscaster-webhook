# ExternalDNS Webhook Provider for DNScaster

ExternalDNS is a Kubernetes add-on for automatically managing DNS records for
Kubernetes ingresses and services by using different DNS providers. This
webhook provider allows you to automate DNS records from your Kubernetes clusters
to your DNScaster service provider.

## Requirements

- ExternalDNS >= `v0.15.0`

> [!Note]
> `v0.15.0` of ExternalDNS added support for `providerSpecific` annotations in Ingress/Service objects for webhook providers.
>
> While older versions of ExternalDNS may work, support for this feature will not be present.

## Limitations

- DNScaster doesn't support multiple targets for a single record.
  When records are parsed, only the first target will be added by this provider.
  If an endpoint needs multiple target, you must create multiple records.

- Targeting multiple Nameserver Set is not supported. A single NameserverSet ID
  must be given through an ENV var at runtime.

- IP Monitors can only monitor against their own target. For this reason, IP
  monitors are only supported for A/AAAA records.

## Configuration Options

### DNScaster Connection Configuration

| Environment Variable          | Description                                           | Default Value |
| ----------------------------- | ----------------------------------------------------- | ------------- |
| `DNSCASTER_API_KEY`           | API key needed to communicate with DNScaster API      | N/A           |
| `DNSCASTER_NAMESERVER_SET_ID` | DNScaster Nameserver Set tied to this provider        | N/A           |
| `DNSCASTER_SKIP_TLS_VERIFY`   | Whether to skip TLS verification (`true` or `false`). | `false`       |

### Default Values Configuration

| Environment Variable    | Description                                                        | Default Value |
| ----------------------- | ------------------------------------------------------------------ | ------------- |
| `DNSCASTER_DEFAULT_TTL` | Default TTL value to be set for DNS records with no specified TTL. | `300`         |

### Server Configuration

| Environment Variable             | Description                                                      | Default Value |
| -------------------------------- | ---------------------------------------------------------------- | ------------- |
| `SERVER_HOST`                    | The host address where the server listens.                       | `localhost`   |
| `SERVER_PORT`                    | The port where the server listens.                               | `8888`        |
| `SERVER_READ_TIMEOUT`            | Duration the server waits before timing out on read operations.  | N/A           |
| `SERVER_WRITE_TIMEOUT`           | Duration the server waits before timing out on write operations. | N/A           |
| `DOMAIN_FILTER`                  | List of domains to include in the filter.                        | Empty         |
| `EXCLUDE_DOMAIN_FILTER`          | List of domains to exclude from filtering.                       | Empty         |
| `REGEXP_DOMAIN_FILTER`           | Regular expression for filtering domains.                        | Empty         |
| `REGEXP_DOMAIN_FILTER_EXCLUSION` | Regular expression for excluding domains from the filter.        | Empty         |

### Logging Configuration

| Environment Variable | Description                                                       | Default Value |
| -------------------- | ----------------------------------------------------------------- | ------------- |
| `LOG_LEVEL`          | Change the verbosity of logs (`debug`, `info`, `warn` or `error`) | `info`        |
| `LOG_FORMAT`         | The format in which logs will be printed. (`text` or `json`)      | `text`        |

## Provider Specific Annotations

All provider annotations are prefixed with: `external-dns.alpha.kubernetes.io/webhook-dnscaster-`

Currently, this provider supports the following annotations:

| Annotation                                              | Description                             | Allowed values                 |
| ------------------------------------------------------- | --------------------------------------- | ------------------------------ |
| dnscaster-webhook-ip-monitor-uri-scheme                 | URI scheme for the IP monitor           | "ping", "http", "https", "tcp" |
| dnscaster-webhook-ip-monitor-uri-path                   | URI path for the IP monitor             | Any string with length > 0     |
| dnscaster-webhook-ip-monitor-treat-redirects-as-offline | Monitor will treat redirects as offline | "true"                         |
