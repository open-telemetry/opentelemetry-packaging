# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the Python auto-instrumentation DEB package.
# Installs the package from a local APT repository, activates the agent by
# prepending its directory to PYTHONPATH (the documented manual-activation path),
# then starts a minimal HTTP server that issues sqlite3 queries. The Go test
# sends HTTP traffic and asserts on the spans the console exporter writes.
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

# Activate auto-instrumentation via PYTHONPATH and configure the console exporter.
# OTEL_EXPORTER_OTLP_PROTOCOL must be set (and not grpc) or sitecustomize.py
# self-deactivates; the console exporter itself ignores it.
ENV PYTHONPATH=/usr/lib/opentelemetry/python \
    OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
    OTEL_TRACES_EXPORTER=console \
    OTEL_METRICS_EXPORTER=none \
    OTEL_LOGS_EXPORTER=none \
    OTEL_SERVICE_NAME=python-testapp \
    OTEL_BSP_SCHEDULE_DELAY=1000 \
    OTEL_INJECTOR_LOG_LEVEL=debug

EXPOSE 8080

# Tee stdout/stderr to a file so the Go test can read trace exports even when
# container log APIs are unavailable (e.g., Podman on macOS).
CMD ["/bin/sh", "-c", "python3 /app/app.py 2>&1 | tee /tmp/app-output.log"]
