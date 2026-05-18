# OpenTelemetry Packaging

The Packaging SIG delivers a streamlined, product-like experience for monitoring applications on virtual Linux hosts.
It combines the [OpenTelemetry Injector](https://github.com/open-telemetry/opentelemetry-go-instrumentation/tree/main/internal/pkg/inject), [OpenTelemetry eBPF Instrumentation (OBI)](https://github.com/open-telemetry/opentelemetry-go-instrumentation), and the [OpenTelemetry Collector](https://github.com/open-telemetry/opentelemetry-collector) into modular system packages, enabling users to achieve observability through a single command:

```sh
{apt|yum} install opentelemetry
```

## Current Scope

Scope as defined by the approved [System Packages](https://github.com/open-telemetry/community/blob/main/projects/packaging.md) project.

### Infrastructure & Packaging

- Establish APT and RPM repository infrastructure for OpenTelemetry packages
- Publish modular system packages for the Injector, OBI, and language-specific auto-instrumentation (Java, .NET, Node.js, Python)
- Integrate existing OpenTelemetry Collector packages into repositories
- Define versioning policies aligned with Debian, Ubuntu, and Red Hat practices

### Design Principles

- Make declarative configuration a foundational element
- Enable vendor packages to provide alternatives to upstream offerings
- Ensure OBI and the Injector operate cohesively without double instrumentation
- Adhere to Filesystem Hierarchy Standard (FHS) and packaging best practices

### Out of Scope

- Operating systems beyond Debian and RHEL derivatives
- Profilers integration
- Container image building

## Contributing

For more details on the project proposal, see the [community project page](https://github.com/open-telemetry/community/blob/main/projects/packaging.md).

[SIG meetings](https://www.google.com/url?q=https://zoom.us/j/93361845299?pwd%3DahGkKCqKCBxKtbWDlALJwcwqZo3GBX.1&sa=D&source=calendar&ust=1779485312363010&usg=AOvVaw0_s1RG9qCfUqT1yNjqgzWj) ([Minutes](https://docs.google.com/document/d/1NDY0rpntHeyEvx9xUg9WdiNyWa7Gq8YKUdjfM1a36GI)) are held weekly on Wednesdays at 10 AM PT.

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
