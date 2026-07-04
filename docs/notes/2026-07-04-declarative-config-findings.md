# Declarative configuration through the packages and the injector

Date: 2026-07-04.
Environment: Debian 12 (arm64) Lima VM, packages from this repository installed, injector active system-wide via `/etc/ld.so.preload`.

Goal: switch a whole host to [declarative configuration](https://opentelemetry.io/docs/languages/sdk-configuration/declarative-configuration/) using only the mechanisms our packages provide.
The injector injects `OTEL_CONFIG_FILE` (from `default_env.conf`) into every process, the packaged agents pick it up, and completely unmodified applications export to Dash0 as configured by a single system-wide YAML file.

## TL;DR

| Language | Works via packages + injector? | Notes |
|----------|-------------------------------|-------|
| Java | Yes | Agent ≥ 2.26.0 required; pin upgraded to 2.29.0 |
| Node.js | Yes | Needs our new `register.js` wrapper; upstream register hook ignores `OTEL_CONFIG_FILE` |
| Python | Yes | Needs the `file-configuration` extra and the standard proto-http exporter in the bundle |
| .NET | Yes | Needs 1.15.0, the extra `OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED=true` flag, and an explicit `instrumentation/development.dotnet` section — instrumentations default to **off** under file config, unlike env vars |

## Package changes required

The packages as previously pinned could not do declarative configuration at all.
The following changes were made in this repository:

- **Java**: agent pin bumped from 2.16.0 to 2.29.0 (declarative configuration shipped in 2.26.0).
- **.NET**: auto-instrumentation pin bumped from 1.11.0 to 1.15.0 (`OTEL_CONFIG_FILE` and `file_format: "1.0"` support shipped in 1.15.0). Two extra activation requirements apply — see the .NET result and TODO below.
- **Node.js**: auto-instrumentations pin bumped from 0.59.0 to 0.78.0, and a new `register.js` wrapper is shipped at `/usr/lib/opentelemetry/nodejs/register.js`, which is now the `--require` target in the injector drop-in.
  The upstream register hook only implements environment-variable configuration and silently ignores `OTEL_CONFIG_FILE`; declarative configuration lives in the experimental `startNodeSDK()` entry point of `@opentelemetry/sdk-node`, which reads `OTEL_CONFIG_FILE` itself (via `@opentelemetry/configuration`).
  The wrapper routes: config file present → `startNodeSDK()` with the auto-instrumentations; otherwise → upstream register hook, unchanged.
- **Python**: `requirements.txt` gained `opentelemetry-sdk[file-configuration]` (pyyaml, jsonschema — without them, setting `OTEL_CONFIG_FILE` crashes the configurator with `ModuleNotFoundError`) and `opentelemetry-exporter-otlp-proto-http` (see the Python TODO below).
- **All languages**: the sample `/etc/opentelemetry/<lang>/otel-config.yaml` files, READMEs, and man pages referenced `OTEL_EXPERIMENTAL_CONFIG_FILE` (an env var no SDK uses) and `file_format: "0.3"`; they now reference the stable `OTEL_CONFIG_FILE` and `file_format: "1.0"`, plus `OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED` for .NET.

## System-wide activation

One shared configuration file at `/etc/opentelemetry/declarative.yaml`:

```yaml
file_format: "1.0"
resource:
  attributes:
    - name: service.name
      value: ${DECL_SERVICE:-declarative-injected}
tracer_provider:
  processors:
    - batch:
        exporter:
          otlp_http:
            endpoint: https://ingress.eu-west-1.aws.dash0-dev.com/v1/traces
            headers:
              - name: Authorization
                value: Bearer <token>
              - name: Dash0-Dataset
                value: system-packages
meter_provider:
  readers:
    - periodic:
        interval: 10000
        timeout: 5000
        exporter:
          otlp_http:
            endpoint: https://ingress.eu-west-1.aws.dash0-dev.com/v1/metrics
            headers: # same as above
logger_provider:
  processors:
    - batch:
        exporter:
          otlp_http:
            endpoint: https://ingress.eu-west-1.aws.dash0-dev.com/v1/logs
            headers: # same as above
```

And two lines appended to `/etc/opentelemetry/injector/default_env.conf`, which the injector injects into every process:

```
OTEL_CONFIG_FILE=/etc/opentelemetry/declarative.yaml
OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED=true
```

Schema details that differ from environment-variable configuration:

- The `otlp_http` endpoint is the **full per-signal URL** (`/v1/traces`, and so on), unlike `OTEL_EXPORTER_OTLP_ENDPOINT`, which is a base URL.
- Header values are plain YAML strings; no URL encoding of the space in `Bearer <token>` (unlike `OTEL_EXPORTER_OTLP_HEADERS`).
- Values in `default_env.conf` must not be quoted; the injector uses them literally ([opentelemetry-injector#373](https://github.com/open-telemetry/opentelemetry-injector/issues/373)).
- `${VAR}` and `${VAR:-default}` substitution in the YAML reads the process environment; this is how per-service names (`DECL_SERVICE`) work with a single shared file.
- The periodic metric reader needs `timeout` ≤ `interval`; the Node.js implementation rejects the file otherwise (Java silently accepts it).

Once `OTEL_CONFIG_FILE` is in effect, the spec requires SDKs to **ignore all other `OTEL_*` environment variables** as direct configuration — but they remain available to `${VAR}` substitution inside the file.
The injected variables from `default_env.conf` therefore lose their direct meaning, yet the config file can deliberately consume them, and the schema is designed for exactly this bridge: `headers_list` accepts the `OTEL_EXPORTER_OTLP_HEADERS` wire format (comma-separated, percent-encoded) as-is.
Verified end to end (spans arrived in Dash0 as `bridge-java`):

```yaml
tracer_provider:
  processors:
    - batch:
        exporter:
          otlp_http:
            endpoint: ${OTEL_EXPORTER_OTLP_ENDPOINT}/v1/traces
            headers_list: ${OTEL_EXPORTER_OTLP_HEADERS}
```

This makes `default_env.conf` the single source of endpoint and credentials for both models during a migration: env-var-configured processes keep consuming the variables directly, declarative ones consume them via interpolation.
What should be avoided is *duplicating* values (endpoint in the file *and* in the variables); interpolation keeps one source of truth.

## Results

Each app makes 3 auto-instrumented HTTPS GET requests and is launched with no OTel-related setup whatsoever; the injector does everything.

- **Java — works.**
  The injected 2.29.0 agent logs `Autoconfiguring from configuration file: …` and exports as `inject-java`; 3 spans confirmed in Dash0.
- **Node.js — works.**
  The injected `register.js` wrapper boots `startNodeSDK()`; 3 spans confirmed as `inject-nodejs`.
- **Python — works** (in a clean virtualenv; see the sitecustomize TODO).
  The agent logs `OTEL_CONFIG_FILE is set; ignoring configurator kwargs` and exports as `inject-python`; 3 spans confirmed.
- **.NET — works, but instrumentation must be enabled explicitly.**
  Unlike the other three languages (and unlike .NET's own env-var mode, where all instrumentations are on by default), file-based configuration enables **no** instrumentations until they are listed; a config without that section produces zero telemetry while the exporters run normally (the SDK logs `Instrument belongs to a Meter not subscribed by the provider`, and tests showed 0 datapoints emitted).
  With the section present, the 3 spans are emitted as `bridge-dotnet`:

```yaml
instrumentation/development:
  dotnet:
    traces:
      httpclient: {}
```

## TODOs

### Java

None.
Declarative configuration works out of the box with agent ≥ 2.26.0, including via `JAVA_TOOL_OPTIONS` injection.

### Node.js (upstream: opentelemetry-js, opentelemetry-js-contrib)

- The `@opentelemetry/auto-instrumentations-node/register` hook should support `OTEL_CONFIG_FILE` natively — today it silently ignores it, with no warning that a config file was skipped.
  Our `register.js` wrapper is a stopgap; drop it once upstream supports the variable in the zero-code path.
- `startNodeSDK()` is experimental and its API may break on any `@opentelemetry/sdk-node` bump; the pin and the wrapper must move together.

### Python (upstream: opentelemetry-python)

- The file configurator should declare its dependencies: without the `file-configuration` extra, setting `OTEL_CONFIG_FILE` crashes with a bare `ModuleNotFoundError: No module named 'yaml'` that does not mention the fix (the follow-up `jsonschema` error does).
  Bundling the extra works around this in our package.
- The file configurator hardcodes `opentelemetry.exporter.otlp.proto.http` for the `otlp_http` exporter type; custom exporters (our pure-Python `pyproto` exporter) cannot be selected declaratively because Python has no declarative plugin resolution yet.
  Until it does, declarative configuration forces `opentelemetry-exporter-otlp-proto-http` — and its protobuf dependency — back into our bundle, undercutting the pyproto swap for declarative-config users.
- Our own `sitecustomize` dependency-conflict guard makes system-Python activation fragile: every dependency added to the bundle is a new potential conflict with distro packages.
  Concretely, adding pyyaml made the guard refuse activation on Debian 12 (`PyYAML: required ==6.0.3, found 6.0` from the distro's `python3-yaml`); clean virtualenvs are unaffected.

### .NET (upstream: opentelemetry-dotnet-instrumentation)

- Instrumentation enablement defaults are inverted between the two modes: env-var mode enables all instrumentations by default, file-based mode enables **none** until they are listed under `instrumentation/development.dotnet`.
  A config file without that section yields a silently telemetry-free process (exporters run, nothing is produced) — no warning points at the missing section.
  Worth raising upstream: either apply the same all-on default, or log a prominent warning when file-based configuration ends up with zero enabled instrumentations.
- The `instrumentation/development.dotnet` section is .NET-specific and experimental (`/development` suffix); a shared multi-language config file needs the per-language subsections for each runtime that requires them (Java, Node.js, and Python subscribe their instrumentations without it).
- File-based configuration additionally requires `OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED=true`; `OTEL_CONFIG_FILE` alone is silently ignored.

## References

- [Declarative configuration](https://opentelemetry.io/docs/languages/sdk-configuration/declarative-configuration/) (opentelemetry.io)
- [Declarative configuration is stable!](https://opentelemetry.io/blog/2026/stable-declarative-config/) (blog, March 2026)
- [Java agent declarative configuration](https://opentelemetry.io/docs/zero-code/java/agent/declarative-configuration/)
- [opentelemetry-configuration language support status](https://github.com/open-telemetry/opentelemetry-configuration/blob/main/language-support-status.md)
- [.NET file-based configuration](https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation/blob/main/docs/file-based-configuration.md)
- [`@opentelemetry/configuration`](https://github.com/open-telemetry/opentelemetry-js/tree/main/experimental/packages/configuration)
