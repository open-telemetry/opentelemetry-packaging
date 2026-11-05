// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package images

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/builder"
	"github.com/open-telemetry/opentelemetry-packaging/packaging/changes"
)

type Package struct {
	Component string `json:"component"`
}

func (p Package) PackageName() string {
	component, _ := builder.ComponentByName(p.Component)
	return component.PackageName
}

type Architecture struct {
	Arch   string `json:"arch"`
	Runner string `json:"runner"`
}

type Config struct {
	Packages      []Package      `json:"packages"`
	Architectures []Architecture `json:"architectures"`
}

type Target struct {
	Component string `json:"component"`
	Arch      string `json:"arch"`
	Runner    string `json:"runner"`
}

func Load(packagingDir string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(filepath.Join(packagingDir, "images", "config.json"))
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	if len(cfg.Packages) == 0 || len(cfg.Architectures) == 0 {
		return cfg, fmt.Errorf("image packages and architectures must not be empty")
	}
	components := map[string]bool{}
	for _, pkg := range cfg.Packages {
		if _, ok := builder.ComponentByName(pkg.Component); !ok {
			return cfg, fmt.Errorf("unknown package component %q", pkg.Component)
		}
		if components[pkg.Component] {
			return cfg, fmt.Errorf("duplicate image component %q", pkg.Component)
		}
		components[pkg.Component] = true
	}
	arches := map[string]bool{}
	for _, arch := range cfg.Architectures {
		if arch.Arch == "" || arch.Runner == "" || arches[arch.Arch] {
			return cfg, fmt.Errorf("invalid or duplicate architecture %q", arch.Arch)
		}
		// The value reaches Make recipes and $GITHUB_OUTPUT unquoted, and
		// otherwise only fails deep inside build-packages once CI has already
		// provisioned a runner for it.
		if !builder.SupportedArchitecture(arch.Arch) {
			return cfg, fmt.Errorf("unsupported architecture %q (expected one of %s)", arch.Arch, strings.Join(builder.Architectures, ", "))
		}
		arches[arch.Arch] = true
	}
	return cfg, nil
}

// CheckPins reports configured components that have no upstream version pin.
// Load cannot do this: it runs against configuration alone, while the pins
// live in the packaging tree. Without it a component the builder knows but
// that has no release.txt -- python, whose pins live in requirements.txt --
// passes validation and then fans out CI jobs that every one of them fails.
func (c Config) CheckPins(packagingDir string) error {
	for _, pkg := range c.Packages {
		pin := filepath.Join(packagingDir, "common", pkg.Component, "release.txt")
		if _, err := os.Stat(pin); err != nil {
			return fmt.Errorf("image component %q has no upstream version pin: %w", pkg.Component, err)
		}
	}
	return nil
}

func (c Config) Package(component string) (Package, error) {
	for _, pkg := range c.Packages {
		if pkg.Component == component {
			return pkg, nil
		}
	}
	return Package{}, fmt.Errorf("unsupported image component %q", component)
}

func (c Config) Matrix(packages []Package) []Target {
	targets := make([]Target, 0, len(packages)*len(c.Architectures))
	for _, pkg := range packages {
		for _, arch := range c.Architectures {
			targets = append(targets, Target{Component: pkg.Component, Arch: arch.Arch, Runner: arch.Runner})
		}
	}
	return targets
}

func (c Config) Platforms() string {
	platforms := make([]string, 0, len(c.Architectures))
	for _, arch := range c.Architectures {
		platforms = append(platforms, "linux/"+arch.Arch)
	}
	return strings.Join(platforms, ",")
}

// sharedInputs are the paths besides changes.PackageInputs that every image
// build reads: the image tooling, the nfpm build it extracts, and the planner
// that versions it.
var sharedInputs = []string{
	"packaging/images", "cmd/build-packages", "cmd/package-metadata",
	"packaging/tests/images", "testutil", ".github/workflows/images.yml",
}

func (c Config) Changed(paths []string) []Package {
	components := make([]string, 0, len(c.Packages))
	for _, pkg := range c.Packages {
		components = append(components, pkg.Component)
	}
	selected := changes.Select(components, append(sharedInputs, changes.PackageInputs...), paths)
	packages := make([]Package, 0, len(selected))
	for _, component := range selected {
		packages = append(packages, Package{Component: component})
	}
	return packages
}
