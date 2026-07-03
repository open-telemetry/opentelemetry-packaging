# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the Node.js auto-instrumentation DEB packages.
# Installs packages from a local APT repository, configures the injector-activated
# Node.js agent to export OTLP over HTTP, then starts a minimal HTTP server. The
# workload exports to the otel-sink running on the host; the Go test drives
# traffic and asserts on the exported telemetry.
#
# The OTLP endpoint and the per-test resource attribute (test.id) are injected at
# run time by the test (via the sink's Env), not baked here, because the sink
# listens on an ephemeral host port. The exporter selection, service name, and
# fast flush intervals below are static.

FROM debian:12

ARG ARCH=amd64

# Install Node.js and utilities
RUN apt-get update && apt-get install -y \
    nodejs \
    dpkg-dev \
    gzip \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy pre-built packages and set up a local APT repository
COPY build/local-repo/apt /local-repo
RUN echo 'deb [trusted=yes] file:///local-repo stable main' \
    > /etc/apt/sources.list.d/opentelemetry-local.list

# Install the injector and Node.js auto-instrumentation packages from the local repo.
RUN apt-get update && apt-get install -y \
    opentelemetry-injector \
    opentelemetry-nodejs-autoinstrumentation

# Configure the injector-activated Node.js agent to export OTLP for all signals.
# The endpoint, protocol, and test.id come from the sink's Env at run time.
RUN { \
      echo 'OTEL_TRACES_EXPORTER=otlp'; \
      echo 'OTEL_METRICS_EXPORTER=otlp'; \
      echo 'OTEL_LOGS_EXPORTER=otlp'; \
      echo 'OTEL_SERVICE_NAME=nodejs-testapp'; \
      echo 'OTEL_BSP_SCHEDULE_DELAY=1000'; \
      echo 'OTEL_METRIC_EXPORT_INTERVAL=1000'; \
    } >> /etc/opentelemetry/injector/default_env.conf

# Copy the test server
COPY packaging/tests/shared/nodejs/server.js /app/server.js

EXPOSE 3000

CMD ["/bin/sh", "-c", "node /app/server.js 2>&1 | tee /tmp/app-output.log"]
