// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfig returns a Config pointing at the real packaging/ directory, which the spec generator reads the lifecycle scripts from.
// It performs no downloads.
func testConfig(t *testing.T, version string) Config {
	t.Helper()
	packagingDir, err := filepath.Abs("..")
	require.NoError(t, err)
	return Config{
		Version:      version,
		Arch:         "amd64",
		PackagingDir: packagingDir,
	}
}

func TestEscapePercent(t *testing.T) {
	assert.Equal(t, "%%install", escapePercent("%install"))
	assert.Equal(t, `printf '%%s\n'`, escapePercent(`printf '%s\n'`))
	assert.Equal(t, "no percent here", escapePercent("no percent here"))
	assert.Equal(t, "%%%%", escapePercent("%%"))
}
