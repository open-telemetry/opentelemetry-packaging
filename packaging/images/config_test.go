// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package images

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatrix(t *testing.T) {
	cfg, err := Load("..")
	require.NoError(t, err)
	matrix := cfg.Matrix(cfg.Packages)
	require.Len(t, matrix, len(cfg.Packages)*len(cfg.Architectures))
	for _, pkg := range cfg.Packages {
		for _, arch := range cfg.Architectures {
			require.Contains(t, matrix, Target{Component: pkg.Component, Arch: arch.Arch, Runner: arch.Runner})
		}
	}
}

func TestChanged(t *testing.T) {
	cfg, err := Load("..")
	require.NoError(t, err)
	first, second := cfg.Packages[0], cfg.Packages[1]
	for _, tc := range []struct {
		name  string
		paths []string
		want  []Package
	}{
		{"no changes", nil, []Package{}},
		{"one pin", []string{"packaging/common/" + first.Component + "/release.txt"}, []Package{first}},
		{"grouped pins", []string{"packaging/common/" + first.Component + "/release.txt", "packaging/common/" + second.Component + "/release.txt"}, []Package{first, second}},
		{"shared config", []string{"packaging/images/config.json"}, cfg.Packages},
		{"shared Dockerfile", []string{"packaging/images/Dockerfile"}, cfg.Packages},
		{"builder", []string{"packaging/builder/components.go"}, cfg.Packages},
		{"workflow", []string{".github/workflows/images.yml"}, cfg.Packages},
		// Both ship inside the Python package, so a change to either alters
		// image contents and must not select nothing.
		{"config check", []string{"cmd/otel-config-check/main.go"}, cfg.Packages},
		{"notice", []string{"NOTICE"}, cfg.Packages},
		{"unrelated", []string{"packaging/common/not-supported/release.txt", "README.md"}, []Package{}},
		{"prefix collision", []string{"packaging/common/" + first.Component + "-other/release.txt"}, []Package{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, cfg.Changed(tc.paths))
		})
	}
}

// Every configured component must have an upstream version pin, or the whole
// build matrix for it fails after CI has provisioned runners.
func TestConfiguredComponentsHavePins(t *testing.T) {
	cfg, err := Load("..")
	require.NoError(t, err)
	require.NoError(t, cfg.CheckPins(".."))
}

func TestCheckPins(t *testing.T) {
	cfg, err := Load("..")
	require.NoError(t, err)
	// python's pins live in requirements.txt and meta has no directory, so
	// both reach CI as configured components that cannot be built.
	for _, component := range []string{"python", "meta"} {
		t.Run(component, func(t *testing.T) {
			unpinned := Config{Packages: []Package{{Component: component}}, Architectures: cfg.Architectures}
			require.ErrorContains(t, unpinned.CheckPins(".."), component)
		})
	}
}

func TestLoadValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"no packages", func(c *Config) { c.Packages = nil }},
		{"no architectures", func(c *Config) { c.Architectures = nil }},
		{"unknown component", func(c *Config) { c.Packages[0].Component = "not-supported" }},
		{"duplicate component", func(c *Config) { c.Packages = append(c.Packages, c.Packages[0]) }},
		{"duplicate architecture", func(c *Config) { c.Architectures = append(c.Architectures, c.Architectures[0]) }},
		{"missing runner", func(c *Config) { c.Architectures[0].Runner = "" }},
		{"unsupported architecture", func(c *Config) { c.Architectures[0].Arch = "x86_64" }},
		{"architecture with shell metacharacters", func(c *Config) { c.Architectures[0].Arch = "amd64; echo injected" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load("..")
			require.NoError(t, err)
			tc.change(&cfg)
			data, err := json.Marshal(cfg)
			require.NoError(t, err)
			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, "images"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "images", "config.json"), data, 0o644))
			_, err = Load(root)
			require.Error(t, err)
		})
	}
}
