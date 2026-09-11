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

	// Vendor, Maintainer, License and Homepage set the package identity
	// fields. Each falls back to the OpenTelemetry default when empty, so a
	// vendor building its own components (see the vendor package recipe in
	// docs/design/packages-meta-architecture.md) can brand them without
	// reimplementing the packaging rules.
	Vendor     string
	Maintainer string
	License    string
	Homepage   string

	// NoticeFile is the NOTICE shipped in the documentation directory of the
	// components that bundle third-party code. Empty resolves to the NOTICE
	// next to PackagingDir, which is this repository's layout.
	NoticeFile string
}

// Package identity, with the repository's own values as the defaults.
func (c Config) vendor() string     { return orDefault(c.Vendor, pkgVendor) }
func (c Config) maintainer() string { return orDefault(c.Maintainer, pkgMaintainer) }
func (c Config) license() string    { return orDefault(c.License, pkgLicense) }
func (c Config) homepage() string   { return orDefault(c.Homepage, pkgHomepage) }

// noticeFile resolves the NOTICE path, defaulting to the file next to
// PackagingDir.
func (c Config) noticeFile() string {
	if c.NoticeFile != "" {
		return c.NoticeFile
	}
	return filepath.Join(filepath.Dir(c.PackagingDir), "NOTICE")
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// Relations declares a package's relationships to other packages.
// The values use the interface-versioned virtual names described in
// docs/design/packages-meta-architecture.md.
type Relations struct {
	Provides   []string
	Depends    []string
	Recommends []string
	Suggests   []string
	// Conflicts and Replaces let a package displace another one. A vendor
	// replacement declares the virtual name in Provides and the concrete
	// upstream name in both of these, which is what makes the package manager
	// swap the two in a single transaction.
	//
	// Replaces maps to DEB Replaces and RPM Obsoletes. The RPM side has a
	// visible consequence: with both repositories enabled, installing the
	// obsoleted name redirects to the replacement unless the client passes
	// --setopt=obsoletes=0.
	Conflicts []string
	Replaces  []string
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
	Noarch    bool
	Relations Relations
	// PostInstall and PreRemove name the lifecycle scripts in
	// packaging/common/scripts, empty when the component has none.
	PostInstall string
	PreRemove   string
	// ContentsFunc stages the component's payload and returns its contents. It
	// also returns a cleanup function that removes any staging directories,
	// which must be called once packaging completes.
	ContentsFunc ContentsFunc
}

// ContentsFunc stages a component's payload. It returns the staged contents
// and a cleanup function that removes any staging directories, which the
// caller must invoke once packaging completes. Naming the type lets code
// outside this package declare and compose payload builders.
type ContentsFunc func(cfg Config) (contents files.Contents, cleanup func(), err error)

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
		Vendor:      cfg.vendor(),
		Maintainer:  cfg.maintainer(),
		License:     cfg.license(),
		Homepage:    cfg.homepage(),
		Overridables: nfpm.Overridables{
			Contents:   contents,
			Provides:   c.Relations.Provides,
			Depends:    c.Relations.Depends,
			Recommends: c.Relations.Recommends,
			Suggests:   c.Relations.Suggests,
			Conflicts:  c.Relations.Conflicts,
			Replaces:   c.Relations.Replaces,
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

// Build creates a single package file and returns its path.
//
// It writes nothing to stdout: progress reporting is the caller's business, and
// the returned path is what a caller needs to report or to hand to a signing or
// publishing step.
func Build(cfg Config, format string, comp Component) (string, error) {
	info, cleanup, err := comp.Info(cfg, format)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return "", fmt.Errorf("building info for %s: %w", comp.Name, err)
	}

	packager, err := nfpm.Get(format)
	if err != nil {
		return "", fmt.Errorf("getting %s packager: %w", format, err)
	}

	outPath := filepath.Join(cfg.OutputDir, packager.ConventionalFileName(info))

	f, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", outPath, err)
	}

	if err := packager.Package(info, f); err != nil {
		f.Close()
		os.Remove(outPath)
		return "", fmt.Errorf("packaging %s: %w", comp.Name, err)
	}

	if err := f.Close(); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("closing %s: %w", outPath, err)
	}

	return outPath, nil
}

// Common package metadata.
const (
	pkgVendor     = "OpenTelemetry"
	pkgMaintainer = "The OpenTelemetry Authors"
	pkgLicense    = "Apache-2.0"
	pkgHomepage   = "https://github.com/open-telemetry/opentelemetry-packaging"
)

// The exported helpers below are the vocabulary for writing a ContentsFunc
// outside this package. The unexported aliases keep the in-repo components
// reading as before.
var (
	configFile  = ConfigFile
	regularFile = RegularFile
	directory   = Directory
	tree        = Tree
)

// ConfigFile creates a Content entry for a config file (noreplace for RPM).
func ConfigFile(src, dst string) *files.Content {
	return &files.Content{
		Source:      src,
		Destination: dst,
		Type:        "config|noreplace",
		FileInfo: &files.ContentFileInfo{
			Mode: 0o644,
		},
	}
}

// RegularFile creates a Content entry for a regular file.
func RegularFile(src, dst string, mode os.FileMode) *files.Content {
	return &files.Content{
		Source:      src,
		Destination: dst,
		FileInfo: &files.ContentFileInfo{
			Mode: mode,
		},
	}
}

// Directory creates a Content entry for an empty directory.
func Directory(dst string) *files.Content {
	return &files.Content{
		Destination: dst,
		Type:        "dir",
		FileInfo: &files.ContentFileInfo{
			Mode: 0o755,
		},
	}
}

// Tree creates a Content entry that includes an entire directory tree.
func Tree(src, dst string) *files.Content {
	return &files.Content{
		Source:      src,
		Destination: dst,
		Type:        "tree",
	}
}
