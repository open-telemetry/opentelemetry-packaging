// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckConfig(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		wantErr string // empty means the config must pass
	}{
		{
			name: "minimal valid config",
			config: `
file_format: "1.0"
`,
		},
		{
			name: "otlp_http exporter passes",
			config: `
file_format: "1.0"
tracer_provider:
  processors:
    - batch:
        exporter:
          otlp_http:
            endpoint: https://otlp.example.com/v1/traces
`,
		},
		{
			name: "env var substitution placeholders are fine",
			config: `
file_format: "1.0"
tracer_provider:
  processors:
    - batch:
        exporter:
          otlp_http:
            endpoint: ${OTEL_EXPORTER_OTLP_ENDPOINT}/v1/traces
            headers_list: ${OTEL_EXPORTER_OTLP_HEADERS}
`,
		},
		{
			name:    "empty file",
			config:  "",
			wantErr: "empty",
		},
		{
			name: "comments-only file",
			config: `
# everything commented out
# file_format: "1.0"
`,
			wantErr: "empty",
		},
		{
			name: "invalid YAML",
			config: `
file_format: "1.0"
tracer_provider: [unclosed
`,
			wantErr: "not valid YAML",
		},
		{
			name: "missing file_format",
			config: `
tracer_provider:
  processors: []
`,
			wantErr: `missing the required top-level "file_format" key`,
		},
		{
			name: "unquoted file_format parses as a float and is rejected",
			config: `
file_format: 1.0
`,
			wantErr: `must be the string "1.0"`,
		},
		{
			name: "unsupported file_format version rejected",
			config: `
file_format: "0.3"
`,
			wantErr: `unsupported file_format "0.3"`,
		},
		{
			name: "otlp_grpc trace exporter rejected",
			config: `
file_format: "1.0"
tracer_provider:
  processors:
    - batch:
        exporter:
          otlp_grpc:
            endpoint: https://otlp.example.com
`,
			wantErr: "otlp_grpc",
		},
		{
			name: "otlp_grpc metric exporter rejected with its path",
			config: `
file_format: "1.0"
meter_provider:
  readers:
    - periodic:
        exporter:
          otlp_grpc: {}
`,
			wantErr: "meter_provider.readers.0.periodic.exporter.otlp_grpc",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkConfig([]byte(tc.config))
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			}
		})
	}
}

func TestCheckFileUnreadable(t *testing.T) {
	err := checkFile(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot read")
}

func TestCheckFileValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "otel-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("file_format: \"1.0\"\n"), 0o644))
	assert.NoError(t, checkFile(path))
}
