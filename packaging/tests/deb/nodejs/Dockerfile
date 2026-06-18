# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the Node.js auto-instrumentation DEB packages.
# Installs packages from a local APT repository, configures the Node.js agent to
# export traces to stdout (console exporter), then starts a minimal HTTP server.
# The Go test sends HTTP traffic and parses OTLP JSON from the container output.

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

# Configure the Node.js agent to use the console exporter (OTLP JSON to stdout).
RUN echo 'OTEL_TRACES_EXPORTER=console' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_METRICS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_LOGS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf

# Copy the test server
COPY packaging/tests/shared/nodejs/server.js /app/server.js

EXPOSE 3000

CMD ["/bin/sh", "-c", "node /app/server.js 2>&1 | tee /tmp/app-output.log"]
