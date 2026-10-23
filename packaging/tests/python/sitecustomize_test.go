// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// This file verifies that the packaged sitecustomize.py is harmless on every
// Python the injector may reach: the injector cannot know which interpreter a
// process runs, so the script must parse and self-deactivate gracefully on
// unsupported versions, and pass its version gate on supported ones — without
// ever breaking the application. The logic behind the guards is covered by
// unit tests (test_sitecustomize.py next to the script); this suite covers
// real interpreters.
//
// "Harmless" includes the application's logging. sitecustomize.py runs during
// site import, before the application's first line, so anything it changed
// about the logging module would be inherited by the application.
package python_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appMarker is printed by the containerized application after sitecustomize.py
// has run; its presence proves the application was not broken.
const appMarker = "APP RAN TO COMPLETION"

// appLogMarker is logged by the containerized application through its own
// logging configuration, at its own level and in its own format. Its presence
// proves that the diagnostics sitecustomize.py emitted first left that
// configuration alone.
const appLogMarker = "APP INFO myapp: application logging is intact"

// appDebugMarker would only appear if something lowered the application's log
// level below the INFO it asked for.
const appDebugMarker = "APP DEBUG"

// supportedPythonVersionsFile holds the supported Python versions the builder
// embeds. The cases below are generated from it rather than listed here, so
// that changing which interpreters the package ships cannot leave this suite
// asserting against a set the package no longer carries.
const supportedPythonVersionsFile = "../../builder/supported_python_versions.json"

// readSupportedPythonVersions returns the supported Python versions in
// ascending order, as "major.minor" strings.
func readSupportedPythonVersions(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(supportedPythonVersionsFile)
	require.NoError(t, err, "reading %s", supportedPythonVersionsFile)

	var versions []string
	require.NoError(t, json.Unmarshal(data, &versions), "parsing %s", supportedPythonVersionsFile)
	require.NotEmpty(t, versions, "%s lists no Python version", supportedPythonVersionsFile)

	sort.Slice(versions, func(i, j int) bool {
		return pythonMinor(t, versions[i]) < pythonMinor(t, versions[j])
	})
	return versions
}

// pythonMinor returns the minor of a "3.N" version. Every supported Python
// version is a 3.x one; the builder's own writeSitecustomize rejects anything
// else, so a 4.x entry is a bug to fail on rather than to interpret.
func pythonMinor(t *testing.T, version string) int {
	t.Helper()
	major, minor, found := strings.Cut(version, ".")
	require.True(t, found, "malformed Python version %q", version)
	require.Equal(t, "3", major, "only 3.x Python versions are supported, found %q", version)
	parsed, err := strconv.Atoi(minor)
	require.NoError(t, err, "malformed Python version %q", version)
	return parsed
}

func TestSitecustomizePythonVersionCompatibility(t *testing.T) {
	supported := readSupportedPythonVersions(t)

	type versionCase struct {
		name  string
		image string
		// expectedGuard is the sitecustomize.py warning proving which guard
		// handled this interpreter.
		expectedGuard string
	}

	// 2.7 is the oldest interpreter the injector may reach at all: the script
	// has to parse there, which is why it is written in the Python 2 subset.
	cases := []versionCase{
		{
			name:          "python2.7-deactivates-at-version-gate",
			image:         "python:2.7-slim",
			expectedGuard: "unsupported Python version",
		},
	}

	// The gate is a membership test, so the interpreters just outside the set
	// at either end are what prove it is one: one minor below the lowest
	// supported version, and one above the highest.
	for _, version := range []string{
		fmt.Sprintf("3.%d", pythonMinor(t, supported[0])-1),
		fmt.Sprintf("3.%d", pythonMinor(t, supported[len(supported)-1])+1),
	} {
		cases = append(cases, versionCase{
			name:          "python" + version + "-deactivates-at-version-gate",
			image:         "python:" + version + "-slim",
			expectedGuard: "unsupported Python version",
		})
	}

	// Every supported interpreter passes the version gate, so the protocol
	// guard fires next. The Dockerfile sets an unsupported protocol
	// (http/json) to make that deactivation deterministic — proof the gate
	// passed.
	for _, version := range supported {
		cases = append(cases, versionCase{
			name:          "python" + version + "-passes-version-gate",
			image:         "python:" + version + "-slim",
			expectedGuard: "OTEL_EXPORTER_OTLP_PROTOCOL=http/json is not supported",
		})
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
			assert.Contains(t, output, appLogMarker,
				"the application's own logging configuration must be the one in effect under %s", tc.image)
			assert.NotContains(t, output, appDebugMarker,
				"sitecustomize.py must not lower the application's log level under %s", tc.image)
		})
	}
}
