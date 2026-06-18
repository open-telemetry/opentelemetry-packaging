// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package nodejs_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodejsAutoInstrumentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	container := testutil.StartServiceContainer(t, ctx,
		"packaging/tests/deb/nodejs/Dockerfile",
		nil,
		[]string{"3000/tcp"},
		"3000/tcp",
		"/",
	)

	// Send HTTP requests to generate traces.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "3000/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Wait for the console exporter to write spans to the output file.
	// Node.js ConsoleSpanExporter outputs JS object notation with traceId, kind, attributes, etc.
	output := testutil.WaitForFileContaining(t, ctx, container, "/tmp/app-output.log", "traceId", 30*time.Second)

	// The Node.js agent was loaded via OpenTelemetry SDK.
	require.Contains(t, output, "opentelemetry", "OpenTelemetry SDK should be loaded")

	// Server spans are present (kind: 2 = SERVER).
	assert.Contains(t, output, "kind: 2", "expected SERVER span kind")

	// service.name is present in the resource.
	assert.Contains(t, output, "service.name", "expected service.name in resource attributes")

	// HTTP instrumentation scope is present.
	assert.Contains(t, output, "@opentelemetry/instrumentation-http", "expected HTTP instrumentation scope")
}
