// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package dotnet_test exercises the .NET auto-instrumentation package across
// package formats and base images. The telemetry assertions are identical for
// every combination, so the matrix varies only the format (deb/rpm) and base
// image, pushed into the per-format Dockerfile via build args.
package dotnet_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
	"github.com/stretchr/testify/assert"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// exportTimeout bounds how long we wait for each signal to reach the sink. The
// agent flushes on ~1s schedules (see the Dockerfiles), so this is generous.
const exportTimeout = 90 * time.Second

// target is one (package format, base image) combination in the test matrix.
type target struct {
	format    string // "deb" or "rpm" — selects Dockerfile.<format>
	name      string // subtest label (the base image reference is verbose)
	baseImage string // runtime-stage base image
}

// matrix lists the base images each package format is exercised against. Add a
// row to cover another base image; the assertions do not change.
var matrix = []target{
	{format: "deb", name: "debian-12", baseImage: "mcr.microsoft.com/dotnet/aspnet:9.0-bookworm-slim"},
	{format: "rpm", name: "fedora-41", baseImage: "fedora:41"},
}

func TestDotnetAutoInstrumentation(t *testing.T) {
	ctx := context.Background()

	byFormat := map[string][]target{}
	for _, tg := range matrix {
		byFormat[tg.format] = append(byFormat[tg.format], tg)
	}

	for _, format := range []string{"deb", "rpm"} {
		targets := byFormat[format]
		if len(targets) == 0 {
			continue
		}
		t.Run(format, func(t *testing.T) {
			for _, tg := range targets {
				t.Run(tg.name, func(t *testing.T) {
					t.Parallel()
					runDotnetCase(t, ctx, tg, false)
				})
			}
		})
	}
}

// TestDotnetDeclarativeConfiguration exercises OTEL_CONFIG_FILE end to end: the shipped
// /etc/opentelemetry/dotnet/otel-config.yaml (installed by the package, shared
// across all language packages) drives the agent instead of the OTEL_* env
// vars. One format and base image suffices: the configuration-file mechanism
// does not vary with the packaging format.
func TestDotnetDeclarativeConfiguration(t *testing.T) {
	ctx := context.Background()
	tg := target{format: "deb", name: "debian-12", baseImage: "mcr.microsoft.com/dotnet/aspnet:9.0-bookworm-slim"}
	t.Run(tg.format, func(t *testing.T) {
		t.Run(tg.name, func(t *testing.T) {
			t.Parallel()
			runDotnetCase(t, ctx, tg, true)
		})
	})
}

func rpmArch() string {
	if testutil.TargetArch() == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

func runDotnetCase(t *testing.T, ctx context.Context, tg target, declarative bool) {
	arch := testutil.TargetArch()
	buildArgs := map[string]*string{
		"BASE_IMAGE": &tg.baseImage,
		"ARCH":       &arch,
	}
	if tg.format == "rpm" {
		ra := rpmArch()
		buildArgs["RPM_ARCH"] = &ra
	}

	sink := otelsink.Start(t)
	env := sink.Env()
	if declarative {
		// With OTEL_CONFIG_FILE in effect the SDK ignores the other OTEL_*
		// variables as direct configuration; the shipped configuration file
		// interpolates the endpoint, service name, and resource attributes
		// (including the sink's test.id) from them instead.
		env["OTEL_CONFIG_FILE"] = "/etc/opentelemetry/dotnet/otel-config.yaml"
		env["OTEL_EXPERIMENTAL_FILE_BASED_CONFIGURATION_ENABLED"] = "true"
	}
	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  fmt.Sprintf("packaging/tests/dotnet/Dockerfile.%s", tg.format),
		BuildArgs:       buildArgs,
		ExposedPorts:    []string{"5000/tcp"},
		WaitPort:        "5000/tcp",
		WaitPath:        "/",
		Env:             env,
		HostAccessPorts: sink.HostAccessPorts(),
	})

	// Drive traffic to the ASP.NET app; each request produces an HTTP server span.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "5000/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Traces: ASP.NET Core is instrumented into a SERVER span.
	traces := sink.WaitForTraces(t, exportTimeout, func(tr *otelsink.Traces) bool {
		return tr.WithKind(tracepb.Span_SPAN_KIND_SERVER).Len() > 0
	})
	assert.NotEmpty(t, traces.WithSpanAttributeValue("http.request.method", "GET").Spans(),
		"expected an HTTP GET server span")
	assert.NotEmpty(t, traces.WithResourceAttribute("service.name", "dotnet-testapp").Spans(),
		"spans should carry the configured service.name resource")

	// Declarative runs assert traces only: under OTEL_CONFIG_FILE the SDK
	// ignores the env-var schedule tuning the image sets, so metrics follow
	// the default 60s cadence.
	if declarative {
		return
	}

	// Metrics: the .NET agent exports runtime and HTTP metrics periodically.
	metrics := sink.WaitForMetrics(t, exportTimeout, otelsink.NonEmpty)
	assert.NotEmpty(t, metrics.Names(), "expected at least one exported metric")

	// No log assertions: the minimal workload writes no ILogger records, so
	// the .NET logs bridge has nothing to export. The sink's log helpers are
	// covered by the Python end-to-end test and the otelsink unit tests.
}
