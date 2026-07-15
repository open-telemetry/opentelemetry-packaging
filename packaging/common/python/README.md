# OpenTelemetry Python Auto-Instrumentation

This package provides OpenTelemetry Python auto-instrumentation for automatic
instrumentation of Python 3.10+ applications.

## Overview

The auto-instrumentation activates popular Python frameworks and libraries to
collect distributed traces, metrics, and logs without requiring code changes.

## Installation

The instrumentation bundle is installed at `/usr/lib/opentelemetry/python/glibc/`
(the injector resolves the agent path as `<prefix>/<libc>`, the same scheme as .NET).

When combined with the `opentelemetry-injector` package, Python applications are
automatically instrumented. The injector prepends `/usr/lib/opentelemetry/python/glibc`
to `PYTHONPATH`, causing Python to execute `sitecustomize.py` at interpreter
startup before the application runs.

The agent path is registered via a drop-in configuration file at
`/etc/opentelemetry/injector/conf.d/python.conf`.

## Exporter

This package bundles the pure-Python OTLP exporters `opentelemetry-exporter-otlp-pyproto-http`
and `opentelemetry-exporter-otlp-pyproto-grpc`. Unlike the standard exporters,
they have no dependency on `google-protobuf`, and the gRPC exporter transports
over a pure-Python HTTP/2 client instead of `grpcio` — so neither pulls in a
C extension or risks version conflicts with application dependencies.
They are drop-in replacements: they own the standard
`opentelemetry.exporter.otlp.proto.{http,grpc}` module paths and register the
standard `otlp_proto_http` / `otlp_proto_grpc` entry points, so both
environment-variable and declarative configuration select them exactly like the
standard exporters.

Both OTLP over gRPC (`grpc`) and OTLP over HTTP with protobuf encoding
(`http/protobuf`) are supported. `http/json` is not yet supported by the bundled
exporters (they emit protobuf only).

## Configuration

### Environment variables

- `OTEL_SERVICE_NAME`: Service name for telemetry (required)
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP endpoint (default: http://localhost:4318 for http/protobuf, http://localhost:4317 for grpc)
- `OTEL_EXPORTER_OTLP_PROTOCOL`: `grpc` or `http/protobuf` (unset defaults to `grpc`)
- `OTEL_TRACES_EXPORTER`: Traces exporter (default follows the protocol: `otlp_proto_grpc` or `otlp_proto_http`)
- `OTEL_METRICS_EXPORTER`: Metrics exporter (default follows the protocol)
- `OTEL_LOGS_EXPORTER`: Logs exporter (default follows the protocol)
- `OTEL_PYTHON_DISABLED_INSTRUMENTATIONS`: Comma-separated list of instrumentations to disable
- `OTEL_INJECTOR_LOG_LEVEL`: Set to `debug` for verbose sitecustomize.py logging

### Declarative configuration

A working declarative configuration file is installed at `/etc/opentelemetry/python/otel-config.yaml`.
It is valid as shipped, exports via OTLP/HTTP using the endpoint and headers the injector injects, and is the same configuration every packaged language agent ships.
To use it, set:

```bash
export OTEL_CONFIG_FILE=/etc/opentelemetry/python/otel-config.yaml
```

## Safety guards

`sitecustomize.py` performs the following checks at startup before activating instrumentation:

1. **Python version**: Requires Python ≥ 3.10. Older versions are skipped gracefully.
2. **OTLP protocol / configuration file**: Without `OTEL_CONFIG_FILE`, accepts
   `OTEL_EXPORTER_OTLP_PROTOCOL` of `grpc` (the default when unset) or `http/protobuf`;
   `http/json` self-deactivates. With `OTEL_CONFIG_FILE` set,
   the SDK ignores the `OTEL_*` exporter variables, so instead the bundled
   `otel-config-check` utility validates the configuration file (readable, valid YAML,
   `file_format: "1.0"`; both `otlp_http` and `otlp_grpc` exporters are accepted).
   Self-deactivates with the validation error if the file is not usable.
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
PYTHONPATH=/usr/lib/opentelemetry/python/glibc:$PYTHONPATH \
  OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
  python myapp.py
```

## See Also

- `opentelemetry-python(8)` - Man page
- https://opentelemetry.io/docs/zero-code/python/
