// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package java_test

import (
	"context"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJavaAutoInstrumentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	container := testutil.StartServiceContainer(t, ctx,
		"packaging/tests/rpm/java/Dockerfile",
		nil,
		[]string{"8080/tcp"},
		"8080/tcp",
		"/",
	)

	// Send HTTP requests to Tomcat to generate traces.
	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "8080/tcp", "/")
		assert.Equal(t, 200, status)
	}

	// Wait for the LoggingSpanExporter to write spans to the output file.
	// Output format: 'SPAN_NAME' : TRACEID SPANID KIND [tracer: SCOPE] AttributesMap{data={...}}
	output := testutil.WaitForFileContaining(t, ctx, container, "/tmp/app-output.log", "LoggingSpanExporter", 30*time.Second)

	// The Java agent was loaded.
	require.Contains(t, output, "opentelemetry-javaagent", "Java agent should be loaded")

	// Server spans are present (Tomcat HTTP handler instrumented).
	assert.Contains(t, output, "SERVER", "expected SERVER span kind from Tomcat")

	// HTTP-related attributes are present.
	assert.Contains(t, output, "http.request.method=GET", "expected HTTP request method attribute")
	assert.Contains(t, output, "http.response.status_code=200", "expected HTTP response status attribute")

	// URL path attribute is present.
	assert.Contains(t, output, "url.path=/", "expected URL path attribute")
}
