# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the Node.js auto-instrumentation RPM packages.
# Installs packages from a local YUM repository, configures the Node.js agent to
# export traces to stdout (console exporter), then starts a minimal HTTP server.
# The Go test sends HTTP traffic and parses OTLP JSON from the container output.

FROM fedora:41

ARG ARCH=amd64
ARG RPM_ARCH=x86_64

ENV LANG=C.UTF-8

# Install Node.js and utilities
RUN dnf -y update \
    && dnf -y install nodejs curl \
    && dnf clean all

# Copy pre-built packages and repository metadata
COPY build/local-repo/rpm /local-repo
RUN printf '[opentelemetry-local]\nname=OpenTelemetry Local\nbaseurl=file:///local-repo/packages\nenabled=1\ngpgcheck=0\n' \
    > /etc/yum.repos.d/opentelemetry-local.repo

# Install the injector and Node.js auto-instrumentation packages from the local repo.
RUN dnf install -y \
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
