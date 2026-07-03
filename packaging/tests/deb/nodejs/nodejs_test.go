// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package nodejs_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
	"github.com/stretchr/testify/assert"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// exportTimeout bounds how long we wait for each signal to reach the sink. The
// agent flushes on ~1s schedules (see the Dockerfile), so this is generous.
const exportTimeout = 90 * time.Second

func TestNodejsAutoInstrumentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	sink := otelsink.Start(t)

	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  "packaging/tests/deb/nodejs/Dockerfile",
		ExposedPorts:    []string{"3000/tcp"},
		WaitPort:        "3000/tcp",
		WaitPath:        "/",
		Env:             sink.Env(),
		HostAccessPorts: sink.HostAccessPorts(),
	})

	// Drive traffic; each request produces an HTTP server span (and metrics).
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

	// Only traces are asserted here: @opentelemetry/auto-instrumentations-node
	// emits traces by default but does not stand up a MeterProvider or a logs
	// pipeline without extra packages, so metrics and logs do not flow from this
	// minimal workload. The sink's metric and log helpers are covered end to end
	// by the otelsink unit tests and (once its package bug is fixed) by Python.
}
