// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shipped documentation names the interpreters the package carries, and
// supported_python_versions.json is the only place that set is written down.
// These tests cover the substitution that keeps the two from drifting: a
// README or man page that still named a dropped interpreter would be wrong in
// the package a user installs, where nothing checks it.

func TestExpandDocPlaceholdersNamesTheSupportedPythonVersions(t *testing.T) {
	expanded := expandDocPlaceholders(
		"running CPython @SUPPORTED_PYTHON_VERSIONS@.", "1.2.3")

	assert.Equal(t,
		"running CPython "+joinWithOr(supportedPythonVersions)+".",
		expanded)
	for _, pythonVersion := range supportedPythonVersions {
		assert.Contains(t, expanded, pythonVersion)
	}
}

func TestExpandDocPlaceholdersNamesTheABITagOfEverySupportedPythonVersion(t *testing.T) {
	expanded := expandDocPlaceholders("tags: @SUPPORTED_PYTHON_ABI_TAGS@", "1.2.3")

	for _, pythonVersion := range supportedPythonVersions {
		assert.Contains(t, expanded, "`"+pythonABITag(pythonVersion)+"`")
	}
}

func TestExpandDocPlaceholdersSubstitutesTheVersion(t *testing.T) {
	assert.Equal(t,
		"opentelemetry 1.2.3",
		expandDocPlaceholders("opentelemetry @VERSION@", "1.2.3"))
}

func TestExpandDocPlaceholdersLeavesNoPlaceholderBehind(t *testing.T) {
	// Every template that ships, expanded: an unsubstituted @PLACEHOLDER@
	// would reach a user's /usr/share/doc verbatim.
	for _, templatePath := range []string{
		filepath.Join("..", "common", "python", "README.md.tmpl"),
		filepath.Join("..", "common", "python", "opentelemetry-python.8.tmpl"),
	} {
		t.Run(filepath.Base(templatePath), func(t *testing.T) {
			data, err := os.ReadFile(templatePath)
			require.NoError(t, err)

			expanded := expandDocPlaceholders(string(data), "1.2.3")

			assert.NotContains(t, expanded, "@",
				"an unexpanded placeholder would ship verbatim")
			assert.Contains(t, expanded, joinWithOr(supportedPythonVersions),
				"the template must name the supported Python versions")
		})
	}
}

func TestJoinWithOr(t *testing.T) {
	for _, tc := range []struct {
		items    []string
		expected string
	}{
		{nil, ""},
		{[]string{"3.10"}, "3.10"},
		{[]string{"3.10", "3.11"}, "3.10 or 3.11"},
		{[]string{"3.10", "3.11", "3.12"}, "3.10, 3.11, or 3.12"},
	} {
		t.Run(strings.Join(tc.items, "-"), func(t *testing.T) {
			assert.Equal(t, tc.expected, joinWithOr(tc.items))
		})
	}
}

func TestGenerateDocDropsTheTmplSuffix(t *testing.T) {
	staging := t.TempDir()
	templatePath := filepath.Join(t.TempDir(), "README.md.tmpl")
	require.NoError(t, os.WriteFile(templatePath,
		[]byte("CPython @SUPPORTED_PYTHON_VERSIONS@\n"), 0o644))

	outPath, err := generateDoc(Config{Version: "1.2.3"}, staging, templatePath)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(staging, "README.md"), outPath)
	written, err := os.ReadFile(outPath)
	require.NoError(t, err)
	assert.Equal(t,
		"CPython "+joinWithOr(supportedPythonVersions)+"\n", string(written))
}

func TestGenerateDocRejectsATemplateWithoutTheSuffix(t *testing.T) {
	_, err := generateDoc(Config{}, t.TempDir(), "README.md")

	require.Error(t, err)
	assert.Contains(t, err.Error(), ".tmpl")
}
