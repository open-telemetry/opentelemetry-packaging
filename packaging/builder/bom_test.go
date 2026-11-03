// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodejsBOMComponents(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, filepath.Join(root, "node_modules", "zeta", "package.json"),
		`{"name":"zeta","version":"2.0.0"}`)
	writeTestFile(t, filepath.Join(root, "node_modules", "@opentelemetry", "api", "package.json"),
		`{"name":"@opentelemetry/api","version":"1.9.0"}`)
	writeTestFile(t, filepath.Join(root, "node_modules", "zeta", "node_modules", "alpha", "package.json"),
		`{"name":"alpha","version":"1.0.0"}`)
	writeTestFile(t, filepath.Join(root, "node_modules", "alpha", "package.json"),
		`{"name":"alpha","version":"1.0.0"}`)

	// A package.json below a package is not itself an installed package root.
	// It must not become a false-positive BOM component.
	writeTestFile(t, filepath.Join(root, "node_modules", "zeta", "examples", "package.json"),
		`{"name":"example-fixture","version":"99.0.0"}`)

	components, err := nodejsBOMComponents(root)
	require.NoError(t, err)

	require.Len(t, components, 3)
	assert.Equal(t, "@opentelemetry/api", components[0].Name)
	assert.Equal(t, "pkg:npm/%40opentelemetry/api@1.9.0", components[0].PURL)
	assert.Equal(t, "alpha", components[1].Name)
	assert.Equal(t, "pkg:npm/alpha@1.0.0", components[1].PURL)
	assert.Equal(t, "zeta", components[2].Name)
}

func TestPythonBOMComponents(t *testing.T) {
	root := t.TempDir()

	writeTestFile(t, filepath.Join(root, "OpenTelemetry_API-1.44.0.dist-info", "METADATA"),
		"Metadata-Version: 2.4\nName: OpenTelemetry_API\nVersion: 1.44.0\n")
	writeTestFile(t, filepath.Join(root, "urllib3-2.8.0.dist-info", "METADATA"),
		"Metadata-Version: 2.4\nName: urllib3\nVersion: 2.8.0\n")

	components, err := pythonBOMComponents(root)
	require.NoError(t, err)

	require.Len(t, components, 2)
	assert.Equal(t, "OpenTelemetry_API", components[0].Name)
	assert.Equal(t, "pkg:pypi/opentelemetry-api@1.44.0", components[0].PURL)
	assert.Equal(t, "urllib3", components[1].Name)
	assert.Equal(t, "pkg:pypi/urllib3@2.8.0", components[1].PURL)
}

func TestPythonBOMComponentsRejectInvalidMetadata(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "broken.dist-info", "METADATA"),
		"Metadata-Version: 2.4\nName: broken\n")

	_, err := pythonBOMComponents(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no name or version")
}

func TestWriteCycloneDXBOMIsDeterministic(t *testing.T) {
	staging := t.TempDir()
	components := []cycloneDXComponent{
		{Type: "library", Name: "zeta", Version: "2.0.0", PURL: "pkg:npm/zeta@2.0.0"},
		{Type: "library", Name: "alpha", Version: "1.0.0", PURL: "pkg:npm/alpha@1.0.0"},
		{Type: "library", Name: "alpha", Version: "1.0.0", PURL: "pkg:npm/alpha@1.0.0"},
	}

	path, err := writeCycloneDXBOM(staging, components)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var bom cycloneDXBOM
	require.NoError(t, json.Unmarshal(data, &bom))
	assert.Equal(t, "CycloneDX", bom.BOMFormat)
	assert.Equal(t, "1.6", bom.SpecVersion)
	assert.Equal(t, 1, bom.Version)
	require.Len(t, bom.Components, 2)
	assert.Equal(t, "alpha", bom.Components[0].Name)
	assert.Equal(t, "zeta", bom.Components[1].Name)

	first := string(data)
	path, err = writeCycloneDXBOM(staging, []cycloneDXComponent{components[1], components[0]})
	require.NoError(t, err)
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, first, string(second))
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
