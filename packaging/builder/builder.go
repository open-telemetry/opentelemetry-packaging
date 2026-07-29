// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package builder creates OpenTelemetry DEB and RPM packages using nfpm.
//
// Each component (injector, java, nodejs, dotnet, python, meta) is described as
// a Component that carries the package metadata declaratively and knows how to
// stage its payload. The Build function takes a Config, a format string ("deb"
// or "rpm"), and a Component, and writes the package file to the output
// directory.
//
// Package metadata is deliberately separated from payload staging: the metadata
// alone drives the RPM spec generation (see spec.go), which must not download
// any upstream artifact, while the payload staging is shared between the nfpm
// packagers and the spec-based build (see stage.go).
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
	// Name is the short component name used on the command line and as the
	// base name of the generated %files fragment.
	Name string
	// PackageName is the name of the produced package.
	PackageName string
	// Description is used as the package description, and as the RPM summary.
	Description string
	// Noarch marks a package whose payload is architecture independent. It
	// selects the "all" DEB architecture and the "noarch" RPM one.
	Noarch bool
	// Provides, Depends, Recommends, and Suggests declare the package
	// relationships. They all use the interface-versioned virtual names
	// described in docs/design/packages-meta-architecture.md.
	Provides   []string
	Depends    []string
	Recommends []string
	Suggests   []string
	// PostInstall and PreRemove name the lifecycle scripts in
	// packaging/common/scripts, empty when the component has none.
	PostInstall string
	PreRemove   string
	// ContentsFunc stages the component's payload and returns its contents. It
	// also returns a cleanup function that removes any staging directories,
	// which must be called once packaging completes.
	ContentsFunc func(cfg Config) (contents files.Contents, cleanup func(), err error)
}

// Arch returns the package architecture for the given format.
func (c Component) Arch(cfg Config, format string) string {
	if !c.Noarch {
		return cfg.Arch
	}
	if format == "rpm" {
		return "noarch"
	}
	return "all"
}

// Info stages the component's payload and returns a complete nfpm.Info for it.
// The returned cleanup function must be called once packaging completes.
func (c Component) Info(cfg Config, format string) (*nfpm.Info, func(), error) {
	contents, cleanup, err := c.ContentsFunc(cfg)
	if err != nil {
		return nil, cleanup, err
	}

	info := &nfpm.Info{
		Name:        c.PackageName,
		Version:     cfg.Version,
		Arch:        c.Arch(cfg, format),
		Platform:    "linux",
		Description: c.Description,
		Vendor:      pkgVendor,
		Maintainer:  pkgMaintainer,
		License:     pkgLicense,
		Homepage:    pkgHomepage,
		Overridables: nfpm.Overridables{
			Contents:   contents,
			Provides:   c.Provides,
			Depends:    c.Depends,
			Recommends: c.Recommends,
			Suggests:   c.Suggests,
			RPM: nfpm.RPM{
				Summary: c.Description,
			},
		},
	}

	scriptsDir := filepath.Join(cfg.PackagingDir, "common", "scripts")
	if c.PostInstall != "" {
		info.Overridables.Scripts.PostInstall = filepath.Join(scriptsDir, c.PostInstall)
	}
	if c.PreRemove != "" {
		info.Overridables.Scripts.PreRemove = filepath.Join(scriptsDir, c.PreRemove)
	}

	return info, cleanup, nil
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
	info, cleanup, err := comp.Info(cfg, format)
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
