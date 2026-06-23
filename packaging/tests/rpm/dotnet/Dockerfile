# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# End-to-end integration test for the .NET auto-instrumentation RPM packages.
# Installs packages from a local YUM repository, configures the .NET agent to
# export traces to stdout (console exporter), then starts a minimal ASP.NET
# HTTP server. The Go test sends HTTP traffic and asserts on the trace output.

FROM mcr.microsoft.com/dotnet/sdk:9.0 AS build

# Build the test app
COPY packaging/tests/shared/dotnet/ /src/
RUN dotnet publish /src/testapp.csproj -c Release -o /app/published --self-contained false

FROM fedora:41

ARG ARCH=amd64
ARG RPM_ARCH=x86_64

ENV LANG=C.UTF-8

# Install .NET ASP.NET runtime and utilities
RUN dnf -y update \
    && dnf -y install dotnet-runtime-9.0 aspnetcore-runtime-9.0 curl \
    && dnf clean all

# Copy the published app
COPY --from=build /app/published /app/

# Copy pre-built packages and repository metadata
COPY build/local-repo/rpm /local-repo
RUN printf '[opentelemetry-local]\nname=OpenTelemetry Local\nbaseurl=file:///local-repo/packages\nenabled=1\ngpgcheck=0\n' \
    > /etc/yum.repos.d/opentelemetry-local.repo

# Install the injector and .NET auto-instrumentation packages from the local repo.
RUN dnf install -y \
    opentelemetry-injector \
    opentelemetry-dotnet-autoinstrumentation

# Configure the .NET agent to log spans to stdout.
RUN echo 'OTEL_TRACES_EXPORTER=console' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_METRICS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf \
    && echo 'OTEL_LOGS_EXPORTER=none' >> /etc/opentelemetry/injector/default_env.conf

EXPOSE 5000

CMD ["/bin/sh", "-c", "dotnet /app/testapp.dll 2>&1 | tee /tmp/app-output.log"]
