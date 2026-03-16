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

## Configuration Options

### DNScaster Connection Configuration

| Environment Variable        | Description                                                                   | Default Value |
| --------------------------- | ----------------------------------------------------------------------------- | ------------- |
| `DNSCASTER_BASEURL`         | URL at which the DNScaster API is available. (ex. `https://192.168.88.1:443`) | N/A           |
| `DNSCASTER_USERNAME`        | Username for the DNScaster API authentication.                                | N/A           |
| `DNSCASTER_PASSWORD`        | Password for the DNScaster API authentication.                                | N/A           |
| `DNSCASTER_SKIP_TLS_VERIFY` | Whether to skip TLS verification (`true` or `false`).                         | `false`       |
| `LOG_LEVEL`                 | Change the verbosity of logs (`debug`, `info`, `warn` or `error`)             | `info`        |
| `LOG_FORMAT`                | The format in which logs will be printed. (`text` or `json`)                  | `text`        |

### Default Values Configuration

| Environment Variable        | Description                                                                | Default Value |
| --------------------------- | -------------------------------------------------------------------------- | ------------- |
| `DNSCASTER_DEFAULT_TTL`     | Default TTL value to be set for DNS records with no specified TTL.         | `300`         |
| `DNSCASTER_DEFAULT_COMMENT` | Default Comment value to be set for DNS records with no specified Comment. | N/A           |

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
