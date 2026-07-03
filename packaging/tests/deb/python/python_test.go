// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package python_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
	"github.com/stretchr/testify/assert"

	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// exportTimeout bounds how long we wait for each signal to reach the sink. The
// workload flushes on ~1s schedules (see the Dockerfile), so this is generous.
const exportTimeout = 90 * time.Second

func TestPythonAutoInstrumentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sink := otelsink.Start(t)

	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  "packaging/tests/deb/python/Dockerfile",
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
