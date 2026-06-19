# OpenTelemetry Injector

The OpenTelemetry Injector is an LD_PRELOAD-based automatic instrumentation injector.
It enables zero-code instrumentation for applications running on Linux systems.

## How it works

The injector library (`libotelinject.so`) is loaded via `/etc/ld.so.preload` into every process.
It detects the runtime (Java, Node.js, .NET) and injects the appropriate OpenTelemetry auto-instrumentation agent.

## Configuration

The injector is configured via `/etc/opentelemetry/injector/injector.conf`.
Agent paths are registered through drop-in files in `/etc/opentelemetry/injector/conf.d/`, which are installed by the corresponding language auto-instrumentation packages.

### Drop-in configuration

Each language package installs a configuration file in `/etc/opentelemetry/injector/conf.d/`:

- `java.conf`: Sets `jvm_auto_instrumentation_agent_path`
- `nodejs.conf`: Sets `nodejs_auto_instrumentation_agent_path`
- `dotnet.conf`: Sets `dotnet_auto_instrumentation_agent_path_prefix`

To add a custom agent configuration, create a file in `conf.d/` (e.g., `99-custom.conf`).
Files are read in alphabetical order.

### Default environment variables

The file at `/etc/opentelemetry/injector/default_env.conf` contains environment variables that are set for all instrumented applications.
Use this to configure common settings like:

- `OTEL_SERVICE_NAME`: Service name for telemetry
- `OTEL_EXPORTER_OTLP_ENDPOINT`: Endpoint for the OTLP exporter

## Files

- `/usr/lib/opentelemetry/injector/libotelinject.so`: The injector shared library
- `/etc/opentelemetry/injector/injector.conf`: Main configuration file
- `/etc/opentelemetry/injector/default_env.conf`: Default environment variables
- `/etc/opentelemetry/injector/conf.d/`: Drop-in configuration directory

## See Also

- `opentelemetry-injector(8)` - Man page for the injector
- https://opentelemetry.io/docs/
