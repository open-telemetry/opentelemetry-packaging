# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the Python auto-instrumentation DEB package.
# Installs the package from a local APT repository, activates the agent by
# prepending its directory to PYTHONPATH (the documented manual-activation path),
# then starts a minimal HTTP server that issues sqlite3 queries and emits a log
# record. The workload exports OTLP over HTTP to the otel-sink running on the
# host; the Go test drives traffic and asserts on the exported telemetry.
#
# The OTLP endpoint and the per-test resource attribute (test.id) are injected at
# run time by the test (via the sink's Env), not baked here, because the sink
# listens on an ephemeral host port. The exporter selection and fast flush
# intervals below are static.
#
# NOTE: activation is via PYTHONPATH rather than the opentelemetry-injector,
# because the injector does not yet implement the Python
# (python_auto_instrumentation_agent_path_prefix) conf.d key. Once it does, this
# test should be extended to install opentelemetry-injector and rely on it.
#
# debian:12 ships Python 3.11, which matches the Python version the bundled
# wheels are built for (see targetPythonVersion in packaging/builder/download.go).

FROM debian:12

ARG ARCH=amd64

# Install Python and utilities.
RUN apt-get update && apt-get install -y \
    python3 \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy pre-built packages and set up a local APT repository.
COPY build/local-repo/apt /local-repo
RUN echo 'deb [trusted=yes] file:///local-repo stable main' \
    > /etc/apt/sources.list.d/opentelemetry-local.list

# Install the Python auto-instrumentation package from the local repo.
RUN apt-get update && apt-get install -y \
    opentelemetry-python-autoinstrumentation

# Copy the test server.
COPY packaging/tests/shared/python/app.py /app/app.py

# Activate auto-instrumentation via PYTHONPATH and export all three signals over
# OTLP. The exporter is intentionally left unset: sitecustomize.py defaults all
# three signals to the bundled pure-Python exporter (otlp_pyproto_http), and
# forcing OTEL_*_EXPORTER=otlp here would resolve to the standard proto exporter,
# which this package does not include. OTEL_EXPORTER_OTLP_PROTOCOL is set to
# http/protobuf by the sink's Env (sitecustomize.py self-deactivates if it is
# grpc or unset). The schedule delays are shortened so telemetry reaches the sink
# within the test window, and log auto-instrumentation is enabled so stdlib
# logging records export as OTLP logs.
ENV PYTHONPATH=/usr/lib/opentelemetry/python \
    OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED=true \
    OTEL_SERVICE_NAME=python-testapp \
    OTEL_BSP_SCHEDULE_DELAY=1000 \
    OTEL_BLRP_SCHEDULE_DELAY=1000 \
    OTEL_METRIC_EXPORT_INTERVAL=1000 \
    OTEL_INJECTOR_LOG_LEVEL=debug

EXPOSE 8080

# Tee stdout/stderr to a file so container output is available for debugging even
# when container log APIs are limited (e.g., Podman on macOS).
CMD ["/bin/sh", "-c", "python3 /app/app.py 2>&1 | tee /tmp/app-output.log"]
