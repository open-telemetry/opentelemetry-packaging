// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// This file verifies that the packaged sitecustomize.py is harmless on every
// Python the injector may reach: the injector cannot know which interpreter a
// process runs, so the script must parse and self-deactivate gracefully on
// unsupported versions, and pass its version gate on supported ones — without
// ever breaking the application. The logic behind the guards is covered by
// unit tests (test_sitecustomize.py next to the script); this suite covers
// real interpreters.
package python_test

import (
	"context"
	"testing"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
)

// appMarker is printed by the containerized application after sitecustomize.py
// has run; its presence proves the application was not broken.
const appMarker = "APP RAN TO COMPLETION"

func TestSitecustomizePythonVersionCompatibility(t *testing.T) {
	cases := []struct {
		name  string
		image string
		// expectedGuard is the sitecustomize.py warning proving which guard
		// handled this interpreter.
		expectedGuard string
	}{
		{
			name:          "python2.7-deactivates-at-version-gate",
			image:         "python:2.7-slim",
			expectedGuard: "unsupported Python version",
		},
		{
			name:          "python3.9-deactivates-at-version-gate",
			image:         "python:3.9-slim",
			expectedGuard: "unsupported Python version",
		},
		{
			// 3.10 is the minimum supported version: the version gate lets it
			// through, so the protocol guard fires next. The Dockerfile sets an
			// unsupported protocol (http/json) to make that deactivation
			// deterministic — proof the gate passed.
			name:          "python3.10-passes-version-gate",
			image:         "python:3.10-slim",
			expectedGuard: "OTEL_EXPORTER_OTLP_PROTOCOL=http/json is not supported",
		},
		{
			name:          "python3.13-passes-version-gate",
			image:         "python:3.13-slim",
			expectedGuard: "OTEL_EXPORTER_OTLP_PROTOCOL=http/json is not supported",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			output := testutil.RunPackageTest(t, ctx, "packaging/tests/python/Dockerfile.sitecustomize", map[string]*string{
				"BASE_IMAGE": &tc.image,
			})
			assert.Contains(t, output, appMarker,
				"the application must run to completion under %s", tc.image)
			assert.Contains(t, output, tc.expectedGuard,
				"expected the guard message for this interpreter")
		})
	}
}
