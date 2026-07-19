# OpenTelemetry Packaging

The Packaging SIG delivers a streamlined, product-like experience for monitoring applications on virtual Linux hosts.
It combines the [OpenTelemetry Injector](https://github.com/open-telemetry/opentelemetry-go-instrumentation/tree/main/internal/pkg/inject), [OpenTelemetry eBPF Instrumentation (OBI)](https://github.com/open-telemetry/opentelemetry-go-instrumentation), and the [OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector) into modular system packages.
Users can achieve observability through a single command:

```sh
{apt|yum} install opentelemetry
```

## Installing

Each release publishes APT and YUM repositories on [GitHub Pages](https://open-telemetry.github.io/opentelemetry-packaging/), together with a landing page that carries the complete installation instructions, including selective per-language installs.

> [!NOTE]
> The GitHub Pages hosting is an interim solution, and the repository URLs below will change when the packages move to their permanent distribution infrastructure.

On Debian and Ubuntu, add the APT repository:

```sh
echo "deb [trusted=yes] https://open-telemetry.github.io/opentelemetry-packaging/debian stable main" | sudo tee /etc/apt/sources.list.d/opentelemetry.list
```

Refresh the package index:

```sh
sudo apt update
```

Install the full auto-instrumentation suite:

```sh
sudo apt install opentelemetry
```

On Fedora, RHEL, and derivatives, add the YUM repository:

```sh
cat <<EOF | sudo tee /etc/yum.repos.d/opentelemetry.repo
[opentelemetry]
name=OpenTelemetry Auto-Instrumentation System Packages
baseurl=https://open-telemetry.github.io/opentelemetry-packaging/rpm/packages
enabled=1
gpgcheck=0
EOF
```

Install the full auto-instrumentation suite:

```sh
sudo dnf install opentelemetry
```

## Configuring where telemetry goes

By default, every auto-instrumentation package exports OTLP to `localhost` (`localhost:4317` for OTLP/gRPC, and `localhost:4318` for OTLP/HTTP).
Point it at a real destination with one of the two options below.

### Option 1: Declarative SDK configuration file

Every language package ships the same declarative configuration file at `/etc/opentelemetry/<language>/otel-config.yaml`.
It is valid as shipped: it interpolates the endpoint, headers, and service name from environment variables the injector already injects into every instrumented process.
Pointing it at a remote destination is therefore a matter of editing `/etc/opentelemetry/injector/default_env.conf`, the injector's single source of endpoint and credentials.

Set the destination endpoint and any required headers, for example an API key:

```sh
cat <<'EOF' | sudo tee -a /etc/opentelemetry/injector/default_env.conf
OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp.example.com
OTEL_EXPORTER_OTLP_HEADERS=api-key=REPLACE_ME
EOF
```

Activate the shipped configuration file for the language you installed, for example Java:

```sh
cat <<'EOF' | sudo tee -a /etc/opentelemetry/injector/default_env.conf
OTEL_CONFIG_FILE=/etc/opentelemetry/java/otel-config.yaml
EOF
```

.NET additionally requires:

```sh
cat <<'EOF' | sudo tee -a /etc/opentelemetry/injector/default_env.conf
OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED=true
EOF
```

Restart the instrumented applications for the new environment to take effect.
Every instrumented process on the host now exports directly to the configured endpoint; there is no local relay.

This option is validated by `Test<Language>DeclarativeConfiguration` in `packaging/tests/{java,nodejs,dotnet,python}/`, which points the shipped configuration file at a test sink and asserts telemetry arrives.

### Option 2: A local Collector

Leave the auto-instrumentation packages at their defaults — SDKs export OTLP to `localhost:4317` — and install an [OpenTelemetry Collector](https://opentelemetry.io/docs/collector/) on the same host to receive that traffic and forward it onward.
This is the arrangement to prefer when several instrumented services on the same host should share one egress point, one set of credentials, and one place to apply processors like batching, retries, resource enrichment, or attribute scrubbing before data leaves the host.
Install the upstream release directly from the [OpenTelemetry Collector releases](https://github.com/open-telemetry/opentelemetry-collector-releases/releases) page, substituting the version below.

On Debian and Ubuntu:

```sh
curl -LO https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v0.156.0/otelcol_0.156.0_linux_amd64.deb
```

```sh
sudo dpkg -i otelcol_0.156.0_linux_amd64.deb
```

On Fedora, RHEL, and derivatives:

```sh
curl -LO https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v0.156.0/otelcol_0.156.0_linux_amd64.rpm
```

```sh
sudo rpm -ivh otelcol_0.156.0_linux_amd64.rpm
```

Edit `/etc/otelcol/config.yaml` to keep the `otlp` receiver on `localhost`, matching what the SDKs already talk to, and export outward instead of upstream's default `debug` exporter:

```yaml
exporters:
  otlphttp:
    endpoint: https://otlp.example.com
    headers:
      api-key: REPLACE_ME

service:
  pipelines:
    traces:
      receivers: [otlp]
      exporters: [otlphttp]
    metrics:
      receivers: [otlp]
      exporters: [otlphttp]
    logs:
      receivers: [otlp]
      exporters: [otlphttp]
```

Restart the service for the new configuration to take effect:

```sh
sudo systemctl restart otelcol
```

## Current scope

Scope as defined by the approved [System Packages](https://github.com/open-telemetry/community/blob/main/projects/packaging.md) project.

### Infrastructure and packaging

- Establish APT and RPM repository infrastructure for OpenTelemetry packages
- Publish modular system packages for the Injector, OBI, and language-specific auto-instrumentation (Java, .NET, Node.js, Python)
- Integrate existing OpenTelemetry Collector packages into repositories
- Define versioning policies aligned with Debian, Ubuntu, and Red Hat practices

### Design principles

- Make declarative configuration a foundational element.
- Enable vendor packages to provide alternatives to upstream offerings.
- Ensure OBI and the Injector operate cohesively without double instrumentation.
- Adhere to Filesystem Hierarchy Standard (FHS) and packaging best practices.

### Out of scope

- Operating systems beyond Debian and RHEL derivatives.
- Profilers integration.
- Container image building.

## Contributing

For more details on the project proposal, see the [community project page](https://github.com/open-telemetry/community/blob/main/projects/packaging.md).

[SIG meetings](https://www.google.com/url?q=https://zoom.us/j/93361845299?pwd%3DahGkKCqKCBxKtbWDlALJwcwqZo3GBX.1&sa=D&source=calendar&ust=1779485312363010&usg=AOvVaw0_s1RG9qCfUqT1yNjqgzWj) ([minutes](https://docs.google.com/document/d/1NDY0rpntHeyEvx9xUg9WdiNyWa7Gq8YKUdjfM1a36GI)) are held weekly on Wednesdays at 10 AM PT.

## Maintainers

- [Antoine Toulme](https://github.com/atoulme), [Splunk](https://www.splunk.com/)
- [Damien Mathieu](https://github.com/dmathieu), [Elastic](https://www.elastic.co/)
- [Denys Sedchenko](https://github.com/x1unix), [Grafana Labs](https://grafana.com/)
- [Douglas Camata](https://github.com/douglascamata), [Coralogix](https://coralogix.com/)
- [Michele Mancioppi](https://github.com/mmanciop), [Dash0](https://www.dash0.com/)

For more information about the maintainer role, see the [community repository](https://github.com/open-telemetry/community/blob/main/guides/contributor/membership.md#maintainer).

## Approvers

- TODO

For more information about the approver role, see the [community repository](https://github.com/open-telemetry/community/blob/main/guides/contributor/membership.md#approver).
