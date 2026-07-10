# OpenTelemetry Python Auto-Instrumentation

This package provides OpenTelemetry Python auto-instrumentation for automatic
instrumentation of Python 3.10+ applications.

## Overview

The auto-instrumentation activates popular Python frameworks and libraries to
collect distributed traces, metrics, and logs without requiring code changes.

## Installation

The instrumentation bundle is installed at `/usr/lib/opentelemetry/python/`.

When combined with the `opentelemetry-injector` package, Python applications are
automatically instrumented. The injector prepends `/usr/lib/opentelemetry/python`
to `PYTHONPATH`, causing Python to execute `sitecustomize.py` at interpreter
startup before the application runs.

The agent path is registered via a drop-in configuration file at
`/etc/opentelemetry/injector/conf.d/python.conf`.

## Exporter

This package bundles `opentelemetry-exporter-otlp-pyproto-http`, a pure-Python
protobuf implementation of the OTLP HTTP exporter. Unlike the standard
`opentelemetry-exporter-otlp-proto-http`, it has no dependency on `google-protobuf`
and avoids C-extension version conflicts with application dependencies.

Only HTTP export (`http/protobuf` or `http/json`) is supported. gRPC is not included.

## Configuration

### Environment variables

- `OTEL_SERVICE_NAME`: Service name for telemetry (required)
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP HTTP endpoint (default: http://localhost:4318)
- `OTEL_EXPORTER_OTLP_PROTOCOL`: Must be `http/protobuf` or `http/json`
- `OTEL_TRACES_EXPORTER`: Traces exporter (default: `otlp_pyproto_http`)
- `OTEL_METRICS_EXPORTER`: Metrics exporter (default: `otlp_pyproto_http`)
- `OTEL_LOGS_EXPORTER`: Logs exporter (default: `otlp_pyproto_http`)
- `OTEL_PYTHON_DISABLED_INSTRUMENTATIONS`: Comma-separated list of instrumentations to disable
- `OTEL_INJECTOR_LOG_LEVEL`: Set to `debug` for verbose sitecustomize.py logging

### Declarative configuration

A configuration file template is available at `/etc/opentelemetry/python/otel-config.yaml`.
To use it, set:

```bash
export OTEL_CONFIG_FILE=/etc/opentelemetry/python/otel-config.yaml
```

## Safety guards

`sitecustomize.py` performs the following checks at startup before activating instrumentation:

1. **Python version**: Requires Python ≥ 3.10. Older versions are skipped gracefully.
2. **OTLP protocol / configuration file**: Without `OTEL_CONFIG_FILE`, requires
   `OTEL_EXPORTER_OTLP_PROTOCOL` to be set and not `grpc`. With `OTEL_CONFIG_FILE` set,
   the SDK ignores the `OTEL_*` exporter variables, so instead the bundled
   `otel-config-check` utility validates the configuration file (readable, valid YAML,
   `file_format: "1.0"`, no `otlp_grpc` exporter). Self-deactivates with the
   validation error if the file is not usable.
3. **Double instrumentation**: Detects if the application already carries OTel SDK
   dependencies and self-deactivates to avoid conflicts.
4. **Dependency conflicts**: Compares the bundled package versions against those installed
   in the application. Self-deactivates if conflicts are detected, except for
   general-purpose libraries (PyYAML, jsonschema) where the application's version is
   expected to keep working: those log a warning instead.

## Supported libraries

Instrumentation plug-ins are included for:

- Web frameworks: Django, Flask, FastAPI, Starlette, Tornado, Falcon, Pyramid, ASGI, WSGI
- HTTP clients: requests, httpx, urllib, urllib3, aiohttp
- Databases: SQLAlchemy, psycopg2, psycopg, pymysql, mysql, asyncpg, sqlite3, cassandra
- Message queues: Kafka (kafka-python, confluent-kafka, aiokafka), RabbitMQ (pika, aio-pika), Celery, Remoulade
- Other: Redis, pymemcache, pymongo, boto/botocore/boto3sqs, gRPC, logging, threading, asyncio

## Manual usage

If not using the injector, you can manually activate instrumentation:

```bash
PYTHONPATH=/usr/lib/opentelemetry/python:$PYTHONPATH \
  OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
  python myapp.py
```

## See Also

- `opentelemetry-python(8)` - Man page
- https://opentelemetry.io/docs/zero-code/python/
