// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"debug/buildinfo"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuiltBinaryEmbedsNoVCSStamps builds the validator the way the Makefile
// does and checks that nothing about the commit is in it.
//
// This binary is the one part of a package's payload that is compiled rather
// than copied, and the Python package's OBS source archive bundles it. go
// build stamps vcs.revision and vcs.time into a main package built from a git
// clone, which would make that archive differ at every commit: OBS would then
// commit a new revision and release an identical package every time the
// workflow uploads it, instead of recognising the sources as unchanged.
//
// The test builds through make, so it covers the flags the packages are
// actually built with rather than a copy of them that can drift. go build
// stamps nothing when it cannot find the repository, as in a git worktree,
// where .git is a file: there this passes without having proven anything. CI
// builds from a clone, where it does.
func TestBuiltBinaryEmbedsNoVCSStamps(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	const arch = "amd64"
	cmd := exec.Command("make", "otel-config-check", "ARCH="+arch)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "make otel-config-check: %s", out)

	info, err := buildinfo.ReadFile(filepath.Join(root, "build", "bin", "otel-config-check-"+arch))
	require.NoError(t, err)

	for _, setting := range info.Settings {
		assert.NotContains(t, setting.Key, "vcs",
			"the shipped binary must not record %s=%s: build it with -buildvcs=false", setting.Key, setting.Value)
	}
}
