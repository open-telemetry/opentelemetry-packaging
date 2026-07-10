// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package builder creates OpenTelemetry DEB and RPM packages using nfpm.
//
// Each component (injector, java, nodejs, dotnet, meta) is described as a
// Component that knows how to populate an nfpm.Info with the correct metadata,
// file contents, and lifecycle scripts. The Build function takes a Config, a
// format string ("deb" or "rpm"), and a Component, and writes the package file
// to the output directory.
package builder

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"

	// Register packagers via init().
	_ "github.com/goreleaser/nfpm/v2/deb"
	_ "github.com/goreleaser/nfpm/v2/rpm"
)

// Config holds build-wide settings.
type Config struct {
	Version      string // Package version (without leading "v")
	Arch         string // Target architecture: amd64 or arm64
	PackagingDir string // Absolute path to the packaging/ directory
	OutputDir    string // Absolute path to the output directory
	// ConfigCheckBinary is the path to a prebuilt otel-config-check binary
	// for the target architecture, shipped inside the Python package. The
	// builder only assembles packages; the binary is cross-compiled upfront
	// (see the otel-config-check Makefile target).
	ConfigCheckBinary string
}

// Component describes a single package to build.
type Component struct {
	Name        string
	Description string
	// InfoFunc builds an nfpm.Info for this component. It returns the info,
	// a cleanup function that removes any staging directories, and an error.
	// The cleanup function must be called after packaging completes.
	InfoFunc func(cfg Config, format string) (info *nfpm.Info, cleanup func(), err error)
}

// ComponentByName returns the Component with the given name.
func ComponentByName(name string) (Component, bool) {
	for _, c := range AllComponents {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// AllComponents lists every buildable component in dependency order.
var AllComponents = []Component{
	Injector,
	Java,
	Nodejs,
	Dotnet,
	Python,
	Meta,
}

// Build creates a single package file.
func Build(cfg Config, format string, comp Component) error {
	info, cleanup, err := comp.InfoFunc(cfg, format)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return fmt.Errorf("building info for %s: %w", comp.Name, err)
	}

	packager, err := nfpm.Get(format)
	if err != nil {
		return fmt.Errorf("getting %s packager: %w", format, err)
	}

	fileName := packager.ConventionalFileName(info)
	outPath := filepath.Join(cfg.OutputDir, fileName)
	fmt.Printf("Building %s: %s\n", format, fileName)

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}

	if err := packager.Package(info, f); err != nil {
		f.Close()
		os.Remove(outPath)
		return fmt.Errorf("packaging %s: %w", comp.Name, err)
	}

	if err := f.Close(); err != nil {
		os.Remove(outPath)
		return fmt.Errorf("closing %s: %w", outPath, err)
	}

	fmt.Printf("  -> %s\n", outPath)
	return nil
}

// Common package metadata.
const (
	pkgVendor     = "OpenTelemetry"
	pkgMaintainer = "The OpenTelemetry Authors"
	pkgLicense    = "Apache-2.0"
	pkgHomepage   = "https://github.com/open-telemetry/opentelemetry-packaging"
)

// commonInfo returns a base nfpm.Info with fields common to all packages.
func commonInfo(cfg Config, name, description, arch string) *nfpm.Info {
	return &nfpm.Info{
		Name:        name,
		Version:     cfg.Version,
		Arch:        arch,
		Platform:    "linux",
		Description: description,
		Vendor:      pkgVendor,
		Maintainer:  pkgMaintainer,
		License:     pkgLicense,
		Homepage:    pkgHomepage,
		Overridables: nfpm.Overridables{
			RPM: nfpm.RPM{
				Summary: description,
			},
		},
	}
}

// configFile creates a Content entry for a config file (noreplace for RPM).
func configFile(src, dst string) *files.Content {
	return &files.Content{
		Source:      src,
		Destination: dst,
		Type:        "config|noreplace",
		FileInfo: &files.ContentFileInfo{
			Mode: 0o644,
		},
	}
}

// regularFile creates a Content entry for a regular file.
func regularFile(src, dst string, mode os.FileMode) *files.Content {
	return &files.Content{
		Source:      src,
		Destination: dst,
		FileInfo: &files.ContentFileInfo{
			Mode: mode,
		},
	}
}

// directory creates a Content entry for an empty directory.
func directory(dst string) *files.Content {
	return &files.Content{
		Destination: dst,
		Type:        "dir",
		FileInfo: &files.ContentFileInfo{
			Mode: 0o755,
		},
	}
}

// tree creates a Content entry that includes an entire directory tree.
func tree(src, dst string) *files.Content {
	return &files.Content{
		Source:      src,
		Destination: dst,
		Type:        "tree",
	}
}
