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
					runDotnetCase(t, ctx, tg)
				})
			}
		})
	}
}

func rpmArch() string {
	if testutil.TargetArch() == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

func runDotnetCase(t *testing.T, ctx context.Context, tg target) {
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
	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  fmt.Sprintf("packaging/tests/dotnet/Dockerfile.%s", tg.format),
		BuildArgs:       buildArgs,
		ExposedPorts:    []string{"5000/tcp"},
		WaitPort:        "5000/tcp",
		WaitPath:        "/",
		Env:             sink.Env(),
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

	// Metrics: the .NET agent exports runtime and HTTP metrics periodically.
	metrics := sink.WaitForMetrics(t, exportTimeout, otelsink.NonEmpty)
	assert.NotEmpty(t, metrics.Names(), "expected at least one exported metric")
}
