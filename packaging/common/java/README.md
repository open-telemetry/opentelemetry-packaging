# OpenTelemetry Java Auto-Instrumentation

This package provides the OpenTelemetry Java Auto-Instrumentation Agent for automatic instrumentation of Java applications.

## Overview

The Java agent automatically instruments popular Java frameworks and libraries to collect distributed traces, metrics, and logs without requiring code changes.

## Installation

The agent is installed at `/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar`.

When combined with the `opentelemetry-injector` package, Java applications are automatically instrumented. The agent path is registered via a drop-in configuration file at `/etc/opentelemetry/injector/conf.d/java.conf`.

## Configuration

### Environment Variables

- `OTEL_SERVICE_NAME`: Service name for telemetry (required)
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP endpoint (default: http://localhost:4317)
- `OTEL_TRACES_EXPORTER`: Traces exporter (otlp, console, none)
- `OTEL_METRICS_EXPORTER`: Metrics exporter (otlp, console, none)
- `OTEL_LOGS_EXPORTER`: Logs exporter (otlp, console, none)
- `OTEL_JAVAAGENT_ENABLED`: Set to "false" to disable (default: true)

### Declarative Configuration

A configuration file is available at `/etc/opentelemetry/java/otel-config.yaml`. To use it, set:

```bash
export OTEL_EXPERIMENTAL_CONFIG_FILE=/etc/opentelemetry/java/otel-config.yaml
```

## Supported Libraries

The agent supports automatic instrumentation for:

- Spring Boot, Spring MVC, Spring WebFlux
- JAX-RS, Jersey, Restlet
- Servlet API (Tomcat, Jetty, etc.)
- gRPC, Apache HttpClient, OkHttp
- JDBC, Hibernate, JPA
- Kafka, RabbitMQ, JMS
- And many more...

## Manual Usage

If not using the injector, you can manually attach the agent:

```bash
java -javaagent:/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar \
  -Dotel.service.name=myservice \
  -jar myapp.jar
```

## See Also

- `opentelemetry-java(8)` - Man page
- https://opentelemetry.io/docs/zero-code/java/agent/
