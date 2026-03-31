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

Provider annotations are implemented using the `properties` field on DNScaster's API.
As such, the provider annotations have the same limitations. More info can be found
in [DNScaster's docs](https://dnscaster.com/docs/api/properties).

### IP Monitor Annotations

Monitors are created from provider specific annotations. This provider currently
supports the following annotations:

| Annotation                 | Description                      | Default       |
| -------------------------- | -------------------------------- | ------------- |
| ip-monitor-uri             | URI for IP monitor               | N/A           |
| ip-monitor-hostname        | Hostname for IP monitor          | Record's FQDN |
| ip-monitor-treat_redirects | How Monitor will treat redirects | online        |

Example:

IP monitors can be used to provide monitoring for your endpoint. Since LoadBalancer IPs are
usually not known ahead of time, defining a uri without a host is perfectly valid and the provider
will grab the target from the record.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route # Let's assume this route is backed by a LB IP at 1.1.1.1
  annotations:
    external-dns.alpha.kubernetes.io/webhook-dnscaster-ip-monitor-uri: "ping"
```

This would create an IP Monitor with the following URI: `ping://1.1.1.1`

---

If your IP Monitor URI is based on HTTP/HTTPS, the hostname will also get populated from
the underlying resource. Note that again, the IP does not need to be known ahead of time.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route # Let's assume this route is backed by a LB IP at 1.1.1.1
  annotations:
    external-dns.alpha.kubernetes.io/webhook-dnscaster-ip-monitor-uri: "https:/health" # URI parsing is using Go's net/url package

    # Note that this URI would result in the same output
    external-dns.alpha.kubernetes.io/webhook-dnscaster-ip-monitor-uri: "https:///health"
```

This would create an IP Monitor with the following URI: `https://1.1.1.1/health`

---

Of course, it is also possible to directly give a full URI if the IP you wish
to monitor is known ahead of time. This can be useful when the monitor should
target a different IP from the managed endpoint.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route # Let's assume this route is backed by a LB IP at 1.1.1.1
  annotations:
    external-dns.alpha.kubernetes.io/webhook-dnscaster-ip-monitor-uri: "ping://1.2.3.4"
```

This would create an IP Monitor with the following URI: `ping://1.2.3.4`.

---

It is also possible to use a different URI for HTTP/HTTPS monitors. Do not forget
to set the `hostname` through an annotation, otherwise the endpoint's FQDN will
be used instead:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route # Let's assume this route is backed by a LB IP at 1.1.1.1
  annotations:
    external-dns.alpha.kubernetes.io/webhook-dnscaster-ip-monitor-uri: "https://1.2.3.4/health"
    external-dns.alpha.kubernetes.io/webhook-dnscaster-ip-monitor-hostname: "api.other.com"
spec:
  hostnames:
    - "api.example.com"
```

### Label Annotations

It is possible to set arbitrary labels on Hosts/Monitors using a provider specific
annotation. The labels are set with an annotation prefixed with: `external-dns.alpha.kubernetes.io/webhook-dnscaster-label-`

For example, if we want to set these labels:

| Label   | Value     |
| ------- | --------- |
| network | ethereum  |
| service | token-api |

The annotation syntax will be:

```yaml
external-dns.alpha.kubernetes.io/webhook-dnscaster-label-network: ethereum
external-dns.alpha.kubernetes.io/webhook-dnscaster-label-service: token-api
```

Note that since labels are arbitrary and can be anything, the label
prefix will be trimmed on the resulting property. This allows you to
not worry about the length of the label prefix in your implementation.

i.e.: The resulting properties for these labels on a host would be:

```json
{
  "properties": {
    "network": "ethereum",
    "service": "token-api"
  }
}
```

---

Labels with the format `prefix/label: value` require an escape sequence
for the `/` character. The escape sequence for `/` is `~1`. As such, an
annotation for the label `registry/name: mainnet` would be written as:

```yaml
external-dns.alpha.kubernetes.io/webhook-dnscaster-label-registry~1name: mainnet
```

Similarly, the `~` character also needs an escape sequence. The special
replacement character `~0` is used to replace `~` in label name.
