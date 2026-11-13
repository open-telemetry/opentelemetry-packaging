// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeJAR builds a zip holding the given entry names, standing in for a
// scraper JAR without needing the real 5 MB download.
func writeJAR(t *testing.T, entries ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "scraper.jar")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	zw := zip.NewWriter(f)
	for _, entry := range entries {
		_, err := zw.Create(entry)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return path
}

// The scraper resolves a target system by loading "<name>.yaml" or
// "jmx/rules/<name>.yaml", so both locations contribute names and a name
// present in both is listed once.
func TestTargetSystemsInJARUnionsBothLocations(t *testing.T) {
	jar := writeJAR(t,
		"jvm.yaml",
		"tomcat.yaml",
		"jmx/rules/jvm.yaml",
		"jmx/rules/camel.yaml",
	)

	systems, err := targetSystemsInJAR(jar)
	require.NoError(t, err)
	assert.Equal(t, []string{"camel", "jvm", "tomcat"}, systems)
}

// Only the JAR root and jmx/rules/ hold definitions. Anything else that happens
// to be YAML — build metadata, a vendored dependency's resources — is not a
// target system.
func TestTargetSystemsInJARIgnoresOtherPaths(t *testing.T) {
	jar := writeJAR(t,
		"jvm.yaml",
		"META-INF/MANIFEST.MF",
		"META-INF/services/some.Service",
		"io/opentelemetry/contrib/jmxscraper/JmxScraper.class",
		"some/other/place/kafka.yaml",
		"jmx/rules/nested/deeper.yaml",
		"notyaml.txt",
	)

	systems, err := targetSystemsInJAR(jar)
	require.NoError(t, err)
	assert.Equal(t, []string{"jvm"}, systems)
}

// Upstream names these files .yaml today, but a .yml definition would be a
// real target system and must not be dropped silently.
func TestTargetSystemsInJARAcceptsYmlSuffix(t *testing.T) {
	jar := writeJAR(t, "jvm.yaml", "jmx/rules/tomcat.yml")

	systems, err := targetSystemsInJAR(jar)
	require.NoError(t, err)
	assert.Equal(t, []string{"jvm", "tomcat"}, systems)
}

// An empty result means upstream moved the definitions. Returning no names
// would quietly erase the documented list, so this has to be an error.
func TestTargetSystemsInJARRejectsEmptyResult(t *testing.T) {
	jar := writeJAR(t, "META-INF/MANIFEST.MF", "io/opentelemetry/Foo.class")

	_, err := targetSystemsInJAR(jar)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no target system definitions found")
}

func TestTargetSystemsInJARRejectsMissingFile(t *testing.T) {
	_, err := targetSystemsInJAR(filepath.Join(t.TempDir(), "absent.jar"))
	require.Error(t, err)
}
