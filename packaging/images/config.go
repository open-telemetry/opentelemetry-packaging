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
		arches[arch.Arch] = true
	}
	return cfg, nil
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

func (c Config) Changed(paths []string) []Package {
	for _, path := range paths {
		for _, shared := range []string{
			"packaging/images", "packaging/builder", "packaging/common/scripts",
			"cmd/build-packages", "cmd/image-metadata", "packaging/tests/images", "testutil",
			"Makefile", "go.mod", "go.sum", ".github/workflows/images.yml",
		} {
			if under(path, shared) {
				return c.Packages
			}
		}
	}
	packages := make([]Package, 0)
	for _, pkg := range c.Packages {
		for _, path := range paths {
			if under(path, "packaging/common/"+pkg.Component) {
				packages = append(packages, pkg)
				break
			}
		}
	}
	return packages
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
