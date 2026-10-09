// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRubyBundlePinsSelectsTargetNativeGem(t *testing.T) {
	pins := []rubyGemPin{
		{Name: "opentelemetry-auto-instrumentation", Version: "0.1.0", Platform: "ruby"},
		{Name: "opentelemetry-sdk", Version: "1.11.0", Platform: "ruby"},
		{Name: "googleapis-common-protos-types", Version: "1.23.0", Platform: "ruby"},
		{Name: "google-protobuf", Version: "4.36.1", Platform: "ruby"},
		{Name: "google-protobuf", Version: "4.36.1", Platform: "x86_64-linux-gnu"},
		{Name: "google-protobuf", Version: "4.36.1", Platform: "aarch64-linux-gnu"},
		{Name: "logger", Version: "1.7.0", Platform: "ruby"},
	}

	amd64, err := rubyBundlePins(pins, "amd64")
	require.NoError(t, err)
	assert.Contains(t, amd64, rubyGemPin{
		Name: "google-protobuf", Version: "4.36.1", Platform: "x86_64-linux-gnu",
	})
	assert.NotContains(t, amd64, rubyGemPin{
		Name: "google-protobuf", Version: "4.36.1", Platform: "aarch64-linux-gnu",
	})
	assert.NotContains(t, amd64, rubyGemPin{
		Name: "google-protobuf", Version: "4.36.1", Platform: "ruby",
	})
	assert.Contains(t, amd64, rubyGemPin{Name: "logger", Version: "1.7.0", Platform: "ruby"})

	arm64, err := rubyBundlePins(pins, "arm64")
	require.NoError(t, err)
	assert.Contains(t, arm64, rubyGemPin{
		Name: "google-protobuf", Version: "4.36.1", Platform: "aarch64-linux-gnu",
	})
}

func TestParseRubyGemLock(t *testing.T) {
	lock := `GEM
  remote: https://rubygems.org/
  specs:
    google-protobuf (4.36.1-x86_64-linux-gnu)
      bigdecimal
    opentelemetry-api (1.9.0)
      logger

PLATFORMS
  x86_64-linux-gnu
`
	path := filepath.Join(t.TempDir(), "Gemfile.lock")
	require.NoError(t, os.WriteFile(path, []byte(lock), 0o644))

	pins, err := parseRubyGemLock(path)
	require.NoError(t, err)
	assert.Equal(t, []rubyGemPin{
		{Name: "google-protobuf", Version: "4.36.1", Platform: "x86_64-linux-gnu"},
		{Name: "opentelemetry-api", Version: "1.9.0", Platform: "ruby"},
	}, pins)
}

func TestFetchRubyGemSHASelectsPlatformArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/versions/google-protobuf.json", r.URL.Path)
		fmt.Fprint(w, `[
  {"number":"4.36.1","platform":"ruby","sha":"generic"},
  {"number":"4.36.1","platform":"x86_64-linux-gnu","sha":"target"}
]`)
	}))
	defer server.Close()

	previous := rubygemsAPIBaseURL
	rubygemsAPIBaseURL = server.URL
	t.Cleanup(func() { rubygemsAPIBaseURL = previous })

	sha, err := fetchRubyGemSHA(rubyGemPin{
		Name: "google-protobuf", Version: "4.36.1", Platform: "x86_64-linux-gnu",
	})
	require.NoError(t, err)
	assert.Equal(t, "target", sha)
}

func writeTestGem(t *testing.T, files map[string]string) string {
	t.Helper()

	var payload bytes.Buffer
	gz := gzip.NewWriter(&payload)
	inner := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, inner.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(body)),
		}))
		_, err := inner.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, inner.Close())
	require.NoError(t, gz.Close())

	path := filepath.Join(t.TempDir(), "fixture.gem")
	f, err := os.Create(path)
	require.NoError(t, err)

	outer := tar.NewWriter(f)
	require.NoError(t, outer.WriteHeader(&tar.Header{
		Name: "data.tar.gz",
		Mode: 0o644,
		Size: int64(payload.Len()),
	}))
	_, err = outer.Write(payload.Bytes())
	require.NoError(t, err)
	require.NoError(t, outer.Close())
	require.NoError(t, f.Close())

	return path
}

func TestExtractRubyGem(t *testing.T) {
	gem := writeTestGem(t, map[string]string{
		"lib/opentelemetry-auto-instrumentation.rb": "puts :ok\n",
	})
	dest := t.TempDir()

	require.NoError(t, extractRubyGem(gem, dest))

	data, err := os.ReadFile(filepath.Join(dest, "lib", "opentelemetry-auto-instrumentation.rb"))
	require.NoError(t, err)
	assert.Equal(t, "puts :ok\n", string(data))
}

func TestExtractRubyGemRejectsTraversal(t *testing.T) {
	gem := writeTestGem(t, map[string]string{"../escape": "nope"})

	err := extractRubyGem(gem, t.TempDir())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "escapes destination"))
}
