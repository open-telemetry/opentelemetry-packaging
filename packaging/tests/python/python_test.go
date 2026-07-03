// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package python_test exercises the Python auto-instrumentation package across
// package formats and base images. The telemetry assertions are identical for
// every combination — the agent, wheels, and workload are the same regardless of
// packaging — so the matrix varies only the format (deb/rpm), the base image, and
// the interpreter, all pushed into the per-format Dockerfile via build args.
package python_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
	"github.com/stretchr/testify/assert"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// exportTimeout bounds how long we wait for each signal to reach the sink. The
// workload flushes on ~1s schedules (see the Dockerfiles), so this is generous.
const exportTimeout = 90 * time.Second

// target is one (package format, base image) combination in the test matrix.
type target struct {
	format    string // "deb" or "rpm" — selects Dockerfile.<format>
	baseImage string // container base image
	pythonBin string // interpreter to install and run (matches the cp311 wheels)
}

// matrix lists the base images each package format is exercised against. To
// cover another base image, add a row: the assertions do not change. Rows are
// grouped into deb/rpm subtests so `go test -run 'TestPythonAutoInstrumentation/deb'`
// selects a single format.
var matrix = []target{
	{format: "deb", baseImage: "debian:12", pythonBin: "python3"},
	{format: "rpm", baseImage: "fedora:41", pythonBin: "python3.11"},
}

func TestPythonAutoInstrumentation(t *testing.T) {
	ctx := context.Background()

	byFormat := map[string][]target{}
	for _, tg := range matrix {
		byFormat[tg.format] = append(byFormat[tg.format], tg)
	}

	// Iterate formats in a stable order so subtest paths are deterministic.
	for _, format := range []string{"deb", "rpm"} {
		targets := byFormat[format]
		if len(targets) == 0 {
			continue
		}
		t.Run(format, func(t *testing.T) {
			for _, tg := range targets {
				t.Run(imageSlug(tg.baseImage), func(t *testing.T) {
					t.Parallel()
					runPythonCase(t, ctx, tg)
				})
			}
		})
	}
}

// imageSlug turns a base image reference into a filesystem/subtest-safe name.
func imageSlug(image string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(image)
}

// rpmArch maps the target Go arch to the RPM architecture string.
func rpmArch() string {
	if testutil.TargetArch() == "arm64" {
		return "aarch64"
	}
	return "x86_64"
}

// runPythonCase builds the workload image for one matrix target, drives traffic,
// and asserts on the traces, logs, and metrics it exports to the sink.
func runPythonCase(t *testing.T, ctx context.Context, tg target) {
	arch := testutil.TargetArch()
	buildArgs := map[string]*string{
		"BASE_IMAGE": &tg.baseImage,
		"ARCH":       &arch,
		"PYTHON_BIN": &tg.pythonBin,
	}
	if tg.format == "rpm" {
		ra := rpmArch()
		buildArgs["RPM_ARCH"] = &ra
	}

	sink := otelsink.Start(t)
	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  fmt.Sprintf("packaging/tests/python/Dockerfile.%s", tg.format),
		BuildArgs:       buildArgs,
		ExposedPorts:    []string{"8080/tcp"},
		WaitPort:        "8080/tcp",
		WaitPath:        "/",
		Env:             sink.Env(),
		HostAccessPorts: sink.HostAccessPorts(),
	})

	// Drive traffic; each request runs a sqlite3 query and emits a log record.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "8080/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Traces: the bundled sqlite3 instrumentation produces a database client span.
	traces := sink.WaitForTraces(t, exportTimeout, func(tr *otelsink.Traces) bool {
		return tr.WithKind(tracepb.Span_SPAN_KIND_CLIENT).Len() > 0
	})
	assert.NotEmpty(t, traces.WithSpanAttributeValue("db.system", "sqlite").Spans(),
		"expected a sqlite db.system client span")
	assert.NotEmpty(t, traces.WithResourceAttribute("service.name", "python-testapp").Spans(),
		"spans should carry the configured service.name resource")

	// Logs: the stdlib logging record is exported via the agent's log handler.
	logs := sink.WaitForLogs(t, exportTimeout, func(l *otelsink.Logs) bool {
		return l.WithBodyContaining("request handled").Len() > 0
	})
	assert.GreaterOrEqual(t,
		logs.WithSeverityAtLeast(logspb.SeverityNumber_SEVERITY_NUMBER_ERROR).Len(), 1,
		"expected an ERROR-severity log record")

	// Metrics: the bundled system-metrics instrumentation exports periodically.
	metrics := sink.WaitForMetrics(t, exportTimeout, otelsink.NonEmpty)
	assert.NotEmpty(t, metrics.Names(), "expected at least one exported metric")
}
