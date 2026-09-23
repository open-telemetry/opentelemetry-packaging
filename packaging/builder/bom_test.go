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

func TestNormalizeBundledComponents(t *testing.T) {
	got := normalizeBundledComponents([]bundledComponent{
		{Name: "zeta", Version: "1.0.0"},
		{Name: "alpha", Version: "2.0.0"},
		{Name: "alpha", Version: "1.0.0"},
		{Name: "alpha", Version: "1.0.0"},
		{Name: "", Version: "1.0.0"},
	})

	assert.Equal(t, []bundledComponent{
		{Name: "alpha", Version: "1.0.0"},
		{Name: "alpha", Version: "2.0.0"},
		{Name: "zeta", Version: "1.0.0"},
	}, got)
}

func TestNodeModulesComponents(t *testing.T) {
	root := t.TempDir()

	writePackageJSON(t, filepath.Join(root, "node_modules", "plain"), "plain", "1.2.3")
	writePackageJSON(t, filepath.Join(root, "node_modules", "@otel", "api"), "@otel/api", "2.0.0")
	writePackageJSON(t, filepath.Join(root, "node_modules", "plain", "node_modules", "nested"), "nested", "3.0.0")

	// A package.json elsewhere inside a dependency is data owned by that package,
	// not another installed npm package, and must not become a BOM component.
	writePackageJSON(t, filepath.Join(root, "node_modules", "plain", "fixtures", "example"), "fixture-only", "9.9.9")

	got, err := nodeModulesComponents(root)
	require.NoError(t, err)

	assert.Equal(t, []bundledComponent{
		{Name: "@otel/api", Version: "2.0.0"},
		{Name: "nested", Version: "3.0.0"},
		{Name: "plain", Version: "1.2.3"},
	}, got)
}

func TestPythonDistInfoComponents(t *testing.T) {
	root := t.TempDir()
	writeDistInfo(t, root, "alpha-1.2.3.dist-info", "alpha", "1.2.3")
	writeDistInfo(t, root, "Beta-2.0.0.dist-info", "Beta", "2.0.0")

	got, err := pythonDistInfoComponents(root)
	require.NoError(t, err)

	assert.Equal(t, []bundledComponent{
		{Name: "Beta", Version: "2.0.0"},
		{Name: "alpha", Version: "1.2.3"},
	}, got)
}

func TestWriteCycloneDXBOMIsDeterministic(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.cdx.json")
	second := filepath.Join(root, "second.cdx.json")

	components := []bundledComponent{
		{Name: "zeta", Version: "2.0.0"},
		{Name: "alpha", Version: "1.0.0"},
		{Name: "alpha", Version: "1.0.0"},
	}
	require.NoError(t, writeCycloneDXBOM(first, "opentelemetry-test", "0.0.0-dev", components))
	require.NoError(t, writeCycloneDXBOM(second, "opentelemetry-test", "0.0.0-dev", components))

	firstData, err := os.ReadFile(first)
	require.NoError(t, err)
	secondData, err := os.ReadFile(second)
	require.NoError(t, err)
	assert.Equal(t, firstData, secondData)

	var bom cycloneDXBOM
	require.NoError(t, json.Unmarshal(firstData, &bom))
	assert.Equal(t, "CycloneDX", bom.BOMFormat)
	assert.Equal(t, "1.7", bom.SpecVersion)
	assert.Equal(t, "opentelemetry-test", bom.Metadata.Component.Name)
	assert.Equal(t, []cycloneDXComponent{
		{Type: "library", Name: "alpha", Version: "1.0.0"},
		{Type: "library", Name: "zeta", Version: "2.0.0"},
	}, bom.Components)
}

func writePackageJSON(t *testing.T, dir, name, version string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	data := []byte(`{"name":"` + name + `","version":"` + version + `"}`)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), data, 0o644))
}

func writeDistInfo(t *testing.T, root, dir, name, version string) {
	t.Helper()
	path := filepath.Join(root, dir)
	require.NoError(t, os.MkdirAll(path, 0o755))
	content := "Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(path, "METADATA"), []byte(content), 0o644))
}
