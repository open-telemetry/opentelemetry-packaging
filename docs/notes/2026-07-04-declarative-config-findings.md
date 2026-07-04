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
| .NET | No | 1.15.0 parses the file and builds the pipeline, but never subscribes any instrumentation — no telemetry is produced |

Verified in Dash0: the 3 test requests per app appear as spans for `inject-java`, `inject-nodejs`, and `inject-python`; nothing arrives from .NET.

## Package changes required

The packages as previously pinned could not do declarative configuration at all.
The following changes were made in this repository:

- **Java**: agent pin bumped from 2.16.0 to 2.29.0 (declarative configuration shipped in 2.26.0).
- **.NET**: auto-instrumentation pin bumped from 1.11.0 to 1.15.0 (`OTEL_CONFIG_FILE` and `file_format: "1.0"` support shipped in 1.15.0). Insufficient — see the .NET TODO below.
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

Once `OTEL_CONFIG_FILE` is in effect, the spec requires SDKs to **ignore all other `OTEL_*` environment variables** (except for substitution inside the file).
Everything else the injector injects from `default_env.conf` — `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_HEADERS`, and so on — becomes inert for declaratively configured SDKs.
The two configuration models should therefore not be mixed on one host long-term.

## Results

Each app makes 3 auto-instrumented HTTPS GET requests and is launched with no OTel-related setup whatsoever; the injector does everything.

- **Java — works.**
  The injected 2.29.0 agent logs `Autoconfiguring from configuration file: …` and exports as `inject-java`; 3 spans confirmed in Dash0.
- **Node.js — works.**
  The injected `register.js` wrapper boots `startNodeSDK()`; 3 spans confirmed as `inject-nodejs`.
- **Python — works** (in a clean virtualenv; see the sitecustomize TODO).
  The agent logs `OTEL_CONFIG_FILE is set; ignoring configurator kwargs` and exports as `inject-python`; 3 spans confirmed.
- **.NET — produces nothing.**
  The 1.15.0 agent parses the file (the metric reader visibly runs at the file's 10-second interval, and with an invalid token the exporter gets a 401 from the Dash0 ingress, proving endpoint and headers come from the file), but no instrumentation is ever subscribed: the SDK logs `Instrument belongs to a Meter not subscribed by the provider` for every meter, no trace export is ever attempted, and Dash0's ingestion accounting shows 0 datapoints.

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
  Decide: accept the extra dependency, or push for plugin resolution upstream and revert it.
- Our own `sitecustomize` dependency-conflict guard makes system-Python activation fragile: every dependency added to the bundle is a new potential conflict with distro packages.
  Concretely, adding pyyaml made the guard refuse activation on Debian 12 (`PyYAML: required ==6.0.3, found 6.0` from the distro's `python3-yaml`); clean virtualenvs are unaffected.

### .NET (upstream: opentelemetry-dotnet-instrumentation)

- File-based configuration in 1.15.0 configures exporters and readers but never subscribes the auto-instrumentation activity sources and meters, so zero telemetry is produced.
  Track upstream until instrumentation subscription works under `OTEL_CONFIG_FILE`; until then, .NET hosts must stay on environment-variable configuration.
- Consider not injecting `OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED=true` fleet-wide while this is the case: it silently turns .NET telemetry off wherever `OTEL_CONFIG_FILE` is also set.

### Injector (upstream: opentelemetry-injector)

- `OTEL_INJECTOR_AUTO_INSTRUMENTATION_DISABLED` stops agent injection but not `default_env.conf` env var injection; a process cannot fully opt out via the documented flag (workaround: point `OTEL_INJECTOR_CONFIG_FILE` at a config with an empty `all_auto_instrumentation_agents_env_path`).
- Values in `default_env.conf` are taken literally, quotes included ([#373](https://github.com/open-telemetry/opentelemetry-injector/issues/373)).
- Injecting `OTEL_CONFIG_FILE` via `default_env.conf` is a working forward path for declarative configuration (proven here) and fits the OpAMP direction (`docs/brainstorms/2026-06-19-opamp-integration-requirements.md`); a first-class `all_auto_instrumentation_agents_config_file` option in the injector would make the intent explicit and avoid shipping dead `OTEL_EXPORTER_*` vars alongside.

## References

- [Declarative configuration](https://opentelemetry.io/docs/languages/sdk-configuration/declarative-configuration/) (opentelemetry.io)
- [Declarative configuration is stable!](https://opentelemetry.io/blog/2026/stable-declarative-config/) (blog, March 2026)
- [Java agent declarative configuration](https://opentelemetry.io/docs/zero-code/java/agent/declarative-configuration/)
- [opentelemetry-configuration language support status](https://github.com/open-telemetry/opentelemetry-configuration/blob/main/language-support-status.md)
- [.NET file-based configuration](https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation/blob/main/docs/file-based-configuration.md)
- [`@opentelemetry/configuration`](https://github.com/open-telemetry/opentelemetry-js/tree/main/experimental/packages/configuration)
