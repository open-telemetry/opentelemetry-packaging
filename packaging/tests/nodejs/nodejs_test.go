// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package nodejs_test exercises the Node.js auto-instrumentation package across
// package formats and base images. The telemetry assertions are identical for
// every combination, so the matrix varies only the format (deb/rpm) and base
// image, pushed into the per-format Dockerfile via build args.
package nodejs_test

import (
	"context"
	"fmt"
	"strings"
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
	baseImage string // container base image
}

// matrix lists the base images each package format is exercised against. Add a
// row to cover another base image; the assertions do not change.
var matrix = []target{
	{format: "deb", baseImage: "debian:12"},
	{format: "rpm", baseImage: "fedora:41"},
}

func TestNodejsAutoInstrumentation(t *testing.T) {
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
				t.Run(imageSlug(tg.baseImage), func(t *testing.T) {
					t.Parallel()
					runNodejsCase(t, ctx, tg)
				})
			}
		})
	}
}

func imageSlug(image string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(image)
}

func rpmArch() string {
	if testutil.TargetArch() == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

func runNodejsCase(t *testing.T, ctx context.Context, tg target) {
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
		DockerfilePath:  fmt.Sprintf("packaging/tests/nodejs/Dockerfile.%s", tg.format),
		BuildArgs:       buildArgs,
		ExposedPorts:    []string{"3000/tcp"},
		WaitPort:        "3000/tcp",
		WaitPath:        "/",
		Env:             sink.Env(),
		HostAccessPorts: sink.HostAccessPorts(),
	})

	// Drive traffic; each request produces an HTTP server span.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "3000/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Traces: the http instrumentation produces a SERVER span, exported over the
	// real injector-activated OTLP path to the host sink.
	traces := sink.WaitForTraces(t, exportTimeout, func(tr *otelsink.Traces) bool {
		return tr.WithKind(tracepb.Span_SPAN_KIND_SERVER).Len() > 0
	})
	assert.NotEmpty(t, traces.WithResourceAttribute("service.name", "nodejs-testapp").Spans(),
		"spans should carry the configured service.name resource")
	serverSpans := traces.WithKind(tracepb.Span_SPAN_KIND_SERVER).Spans()
	assert.Contains(t, serverSpans[0].Scope.GetName(), "http",
		"expected the HTTP instrumentation scope on the server span")

	// Only traces are asserted: @opentelemetry/auto-instrumentations-node emits
	// traces by default but does not stand up a metrics or logs pipeline for this
	// minimal workload. The sink's metric/log helpers are covered by the otelsink
	// unit tests and by the Python end-to-end test.
}
