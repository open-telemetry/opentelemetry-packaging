# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the .NET auto-instrumentation DEB packages.
# Installs packages from a local APT repository, configures the .NET agent to
# export traces to stdout (console exporter), then starts a minimal ASP.NET
# HTTP server. The Go test sends HTTP traffic and asserts on the trace output.

FROM mcr.microsoft.com/dotnet/sdk:9.0-bookworm-slim AS build

# Build the test app
COPY packaging/tests/shared/dotnet/ /src/
RUN dotnet publish /src/testapp.csproj -c Release -o /app/published --self-contained false

FROM mcr.microsoft.com/dotnet/aspnet:9.0-bookworm-slim

ARG ARCH=amd64

# Install utilities needed for local repo setup
RUN apt-get update && apt-get install -y \
    dpkg-dev \
    gzip \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy the published app
COPY --from=build /app/published /app/

# Copy pre-built packages and set up a local APT repository
COPY build/local-repo/apt /local-repo
RUN echo 'deb [trusted=yes] file:///local-repo stable main' \
    > /etc/apt/sources.list.d/opentelemetry-local.list

# Install the injector and .NET auto-instrumentation packages from the local repo.
RUN apt-get update && apt-get install -y \
    opentelemetry-injector \
    opentelemetry-dotnet-autoinstrumentation

# Configure the .NET agent to log spans to stdout.
RUN echo 'OTEL_TRACES_EXPORTER=console' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_METRICS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_LOGS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf

EXPOSE 5000

CMD ["/bin/sh", "-c", "dotnet /app/testapp.dll 2>&1 | tee /tmp/app-output.log"]
