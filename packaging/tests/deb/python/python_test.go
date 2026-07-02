// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package python_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPythonAutoInstrumentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	container := testutil.StartServiceContainer(t, ctx,
		"packaging/tests/deb/python/Dockerfile",
		nil,
		[]string{"8080/tcp"},
		"8080/tcp",
		"/",
	)

	// Send HTTP requests; each handler runs a sqlite3 query to generate spans.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "8080/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Wait for the console exporter to write a span carrying the service name.
	// Python's ConsoleSpanExporter prints span.to_json() (multi-line JSON).
	output := testutil.WaitForFileContaining(t, ctx, container, "/tmp/app-output.log", "python-testapp", 60*time.Second)

	// The agent activated and did not self-deactivate (version/protocol/conflict guards passed).
	require.NotContains(t, output, "cannot auto-instrument", "agent should not self-deactivate")

	// service.name from OTEL_SERVICE_NAME is present in the exported resource.
	assert.Contains(t, output, "service.name", "expected service.name in resource attributes")

	// The bundled sqlite3 auto-instrumentation produced a database client span.
	assert.Contains(t, output, "sqlite", "expected sqlite db.system from sqlite3 instrumentation")
	assert.Contains(t, output, "SpanKind.CLIENT", "expected CLIENT span kind from a database query")
}
