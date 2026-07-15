// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package pyprotogrpc tests the pure-Python gRPC transport (_pygrpc) of the
// pyproto OTLP/gRPC exporter against otelsink, which serves the OTLP services
// through grpc-go — the same server stack as the OpenTelemetry Collector's
// OTLP receiver.
package pyprotogrpc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	// Register grpc-go's gzip decompressor in this process, so the in-process
	// otelsink server accepts grpc-encoding: gzip (the Collector does too).
	_ "google.golang.org/grpc/encoding/gzip"

	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
)

const exportTimeout = 15 * time.Second

// pygrpcSrcDir locates the exporter source tree that contains the _pygrpc
// package. It defaults to the vendored copy; during fork development,
// PYGRPC_SRC_DIR points at the work-in-progress checkout
// (<fork>/exporter/opentelemetry-exporter-otlp-pyproto-grpc/src).
func pygrpcSrcDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("PYGRPC_SRC_DIR")
	if dir == "" {
		dir = filepath.Join(
			"..", "..", "common", "python", "vendor",
			"opentelemetry-exporter-otlp-pyproto-grpc", "src",
		)
	}
	marker := filepath.Join(
		dir, "opentelemetry", "exporter", "otlp", "_proto", "grpc", "_pygrpc",
	)
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("_pygrpc not present at %s (pre-sync); set PYGRPC_SRC_DIR to a fork checkout", marker)
	}
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	return abs
}

func pyprotoSrcDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(
		"..", "..", "common", "python", "vendor", "opentelemetry-pyproto", "src",
	))
	require.NoError(t, err)
	return abs
}

func runProbe(t *testing.T, sink *otelsink.Sink, spanName, scenario string) {
	t.Helper()
	cmd := exec.Command(
		"python3", "export_probe.py",
		"--endpoint", sink.GRPCEndpoint(),
		"--span-name", spanName,
		"--test-id", sink.TestID(),
		"--scenario", scenario,
	)
	cmd.Env = append(os.Environ(), fmt.Sprintf(
		"PYTHONPATH=%s%c%s",
		pygrpcSrcDir(t), os.PathListSeparator, pyprotoSrcDir(t),
	))
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "export probe failed:\n%s", output)
	require.Contains(t, string(output), "OK response_bytes=")
}

func TestPyprotoGrpcTransport(t *testing.T) {
	scenarios := []struct {
		name     string
		scenario string
	}{
		{"basic", "basic"},
		{"gzip-compressed", "gzip"},
		// Pushes the request body past the 64 KiB initial flow-control
		// window, forcing the client through WINDOW_UPDATE handling.
		{"large-payload-flow-control", "large"},
	}
	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			sink := otelsink.Start(t)
			spanName := fmt.Sprintf("pygrpc-%s-span", tc.scenario)
			runProbe(t, sink, spanName, tc.scenario)

			traces := sink.WaitForTraces(t, exportTimeout, func(tr *otelsink.Traces) bool {
				return tr.WithName(spanName).Len() > 0
			})
			require.NotEmpty(t,
				traces.WithResourceAttribute("service.name", "pyprotogrpc-probe").Spans(),
				"span should carry the probe's service.name resource")
			require.NotEmpty(t,
				traces.WithSpanAttributeValue("probe.scenario", tc.scenario).Spans(),
				"span should carry the scenario attribute")
		})
	}
}
