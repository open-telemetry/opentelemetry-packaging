# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the Java auto-instrumentation DEB packages.
# Installs packages from a local APT repository, configures the Java agent to
# export traces to stdout (console exporter), then starts Tomcat. The Go test
# sends HTTP traffic and parses OTLP JSON from the container logs.

FROM debian:12

ARG ARCH=amd64

# Install JRE and utilities
RUN apt-get update && apt-get install -y \
    default-jre \
    curl \
    procps \
    && rm -rf /var/lib/apt/lists/*

# Download and install Apache Tomcat
# renovate: datasource=github-tags depName=apache/tomcat versioning=semver
ARG TOMCAT_VERSION=10.1.41
ADD https://archive.apache.org/dist/tomcat/tomcat-10/v${TOMCAT_VERSION}/bin/apache-tomcat-${TOMCAT_VERSION}.tar.gz /tmp/apache-tomcat.tar.gz
RUN tar xzf /tmp/apache-tomcat.tar.gz -C / \
    && mv /apache-tomcat-${TOMCAT_VERSION} /apache-tomcat \
    && rm /tmp/apache-tomcat.tar.gz \
    && chmod +x /apache-tomcat/bin/*.sh

# Copy pre-built packages and set up a local APT repository
COPY build/local-repo/apt /local-repo
RUN echo 'deb [trusted=yes] file:///local-repo stable main' \
    > /etc/apt/sources.list.d/opentelemetry-local.list

# Install the injector and Java auto-instrumentation packages from the local repo.
# Using apt-get install (not dpkg -i) validates that the repository metadata
# is correct and dependency resolution works.
RUN apt-get update && apt-get install -y \
    opentelemetry-injector \
    opentelemetry-java-autoinstrumentation

# Configure the Java agent to log spans to stdout.
# "logging" uses LoggingSpanExporter which prints a human-readable line per span.
RUN echo 'OTEL_TRACES_EXPORTER=logging' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_METRICS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_LOGS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf

EXPOSE 8080

# Start Tomcat, teeing stdout to a file so the Go test can read trace exports
# even when container log APIs are unavailable (e.g., Podman on macOS).
CMD ["/bin/sh", "-c", "/apache-tomcat/bin/catalina.sh run 2>&1 | tee /tmp/app-output.log"]
