// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package java_test exercises the Java auto-instrumentation package across
// package formats and base images. The telemetry assertions are identical for
// every combination, so the matrix varies only the format (deb/rpm) and base
// image, pushed into the per-format Dockerfile via build args.
package java_test

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

func TestJavaAutoInstrumentation(t *testing.T) {
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
					runJavaCase(t, ctx, tg, false)
				})
			}
		})
	}
}

// TestJavaDeclarativeConfiguration exercises OTEL_CONFIG_FILE end to end: the shipped
// /etc/opentelemetry/java/otel-config.yaml (installed by the package, shared
// across all language packages) drives the agent instead of the OTEL_* env
// vars. One format and base image suffices: the configuration-file mechanism
// does not vary with the packaging format.
func TestJavaDeclarativeConfiguration(t *testing.T) {
	ctx := context.Background()
	tg := target{format: "deb", baseImage: "debian:12"}
	t.Run(tg.format, func(t *testing.T) {
		t.Run(imageSlug(tg.baseImage), func(t *testing.T) {
			t.Parallel()
			runJavaCase(t, ctx, tg, true)
		})
	})
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

func runJavaCase(t *testing.T, ctx context.Context, tg target, declarative bool) {
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
		env["OTEL_CONFIG_FILE"] = "/etc/opentelemetry/java/otel-config.yaml"
	}
	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  fmt.Sprintf("packaging/tests/java/Dockerfile.%s", tg.format),
		BuildArgs:       buildArgs,
		ExposedPorts:    []string{"8080/tcp"},
		WaitPort:        "8080/tcp",
		WaitPath:        "/",
		Env:             env,
		HostAccessPorts: sink.HostAccessPorts(),
	})

	// Drive traffic to Tomcat; each request produces an HTTP server span.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "8080/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Traces: Tomcat's HTTP handler is instrumented into a SERVER span.
	traces := sink.WaitForTraces(t, exportTimeout, func(tr *otelsink.Traces) bool {
		return tr.WithKind(tracepb.Span_SPAN_KIND_SERVER).Len() > 0
	})
	assert.NotEmpty(t, traces.WithSpanAttributeValue("http.request.method", "GET").Spans(),
		"expected an HTTP GET server span")
	assert.NotEmpty(t, traces.WithResourceAttribute("service.name", "java-testapp").Spans(),
		"spans should carry the configured service.name resource")

	// Declarative runs assert traces only: under OTEL_CONFIG_FILE the SDK
	// ignores the env-var schedule tuning the image sets, so metrics follow
	// the default 60s cadence and logs the default batch schedule.
	if declarative {
		return
	}

	// Metrics: the Java agent exports JVM runtime metrics periodically.
	metrics := sink.WaitForMetrics(t, exportTimeout, otelsink.NonEmpty)
	assert.NotEmpty(t, metrics.Names(), "expected at least one exported metric")

	// Logs: Tomcat logs through JUL, and the agent's appender instrumentation
	// bridges those records to OTLP logs.
	logs := sink.WaitForLogs(t, exportTimeout, otelsink.NonEmpty)
	assert.Greater(t, logs.Len(), 0, "expected Tomcat JUL records bridged to OTLP logs")
}
