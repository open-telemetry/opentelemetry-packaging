# OpenTelemetry Node.js Auto-Instrumentation

This package provides the OpenTelemetry Node.js Auto-Instrumentation for automatic instrumentation of Node.js applications.

## Overview

The Node.js instrumentation automatically instruments popular Node.js frameworks and libraries to collect distributed traces, metrics, and logs without requiring code changes.

## Installation

The instrumentation packages are installed at `/usr/lib/opentelemetry/nodejs/`.

When combined with the `opentelemetry-injector` package, Node.js applications are automatically instrumented. The agent path is registered via a drop-in configuration file at `/etc/opentelemetry/injector/conf.d/nodejs.conf`.

## Configuration

### Environment Variables

- `OTEL_SERVICE_NAME`: Service name for telemetry (required)
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP endpoint (default: http://localhost:4317)
- `OTEL_TRACES_EXPORTER`: Traces exporter (otlp, console, none)
- `OTEL_METRICS_EXPORTER`: Metrics exporter (otlp, console, none)
- `OTEL_LOGS_EXPORTER`: Logs exporter (otlp, console, none)
- `OTEL_SDK_DISABLED`: Set to "true" to disable (default: false)

### Declarative Configuration

A configuration file is available at `/etc/opentelemetry/nodejs/otel-config.yaml`. To use it, set:

```bash
export OTEL_EXPERIMENTAL_CONFIG_FILE=/etc/opentelemetry/nodejs/otel-config.yaml
```

## Supported Libraries

The instrumentation supports automatic instrumentation for:

- Express, Koa, Fastify, Hapi, Restify
- HTTP, HTTPS, HTTP/2
- gRPC
- MongoDB, MySQL, PostgreSQL, Redis
- AWS SDK, GraphQL
- And many more...

## Manual Usage

If not using the injector, you can manually load the instrumentation:

```bash
OTEL_SERVICE_NAME=myservice node \
  --require /usr/lib/opentelemetry/nodejs/node_modules/@opentelemetry/auto-instrumentations-node/build/src/register.js \
  app.js
```

## See Also

- `opentelemetry-nodejs(8)` - Man page
- https://opentelemetry.io/docs/zero-code/js/
