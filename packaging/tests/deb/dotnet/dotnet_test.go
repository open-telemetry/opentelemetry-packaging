// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package dotnet_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDotnetAutoInstrumentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	container := testutil.StartServiceContainer(t, ctx,
		"packaging/tests/deb/dotnet/Dockerfile",
		nil,
		[]string{"5000/tcp"},
		"5000/tcp",
		"/",
	)

	// Send HTTP requests to generate traces.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "5000/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Wait for the console exporter to write spans to the output file.
	// .NET ConsoleExporter outputs Activity-based text with TraceId, Kind, Tags, etc.
	output := testutil.WaitForFileContaining(t, ctx, container, "/tmp/app-output.log", "Activity.TraceId", 30*time.Second)

	// Server spans are present.
	require.Contains(t, output, "Activity.Kind", "expected Activity spans in output")
	assert.Contains(t, output, "Server", "expected Server span kind")

	// service.name is present in the resource.
	assert.Contains(t, output, "service.name", "expected service.name in resource")

	// HTTP-related attributes are present.
	assert.Contains(t, output, "http.request.method", "expected HTTP request method attribute")
}
