// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// SpecName is the source package name, and therefore the spec file name. It
// intentionally matches no binary package: a spec can mark a subpackage as
// noarch but never its main package, so the noarch metapackage has to be a
// subpackage, and a main package without a %files section emits no RPM at all.
const SpecName = "opentelemetry-packaging"

var (
	//go:embed opentelemetry-packaging.spec.tmpl
	specTemplateSource string

	specTemplate = template.Must(template.New(SpecName + ".spec.tmpl").Parse(specTemplateSource))
)

// specExclusiveArch lists the RPM architectures the payloads support, so COPR
// chroots for any other one fail up front rather than part-way through.
const specExclusiveArch = "x86_64 aarch64"

// pythonGuard opens the conditional restricting the Python stanzas to chroots
// whose interpreter matches the bundled wheels. The template closes it with a
// literal %endif.
const pythonGuard = "%if %{with_python}"

// specBuildRequires are the buildroot packages the generated spec needs.
// The Go toolchain compiles the staging helper and the configuration validator
// from the vendored sources; npm and pip fetch the Node.js and Python bundles.
var specBuildRequires = []string{
	"golang",
	"npm",
	"python3",
	"python3-pip",
	"tar",
	"gzip",
}

// specData is the template input describing the whole spec.
type specData struct {
	Name          string
	Version       string
	Release       string
	Summary       string
	License       string
	URL           string
	ExclusiveArch string
	BuildRequires []string
	PythonGuard   string
	Packages      []specPackage
	Scriptlets    []specScriptlet
	// UnguardedComponents is the shell word list of components staged
	// unconditionally; GuardedComponents are staged behind PythonGuard.
	UnguardedComponents string
	GuardedComponents   []string
}

// specPackage describes one %package stanza and its %files section.
type specPackage struct {
	// Component is the short component name, and therefore the base name of the
	// generated file list.
	Component   string
	Name        string
	Summary     string
	Description string
	Noarch      bool
	Provides    []string
	Requires    []string
	Recommends  []specRelation
	Suggests    []string
	// Guarded marks a package whose stanzas sit inside the Python conditional.
	Guarded bool
}

// specRelation is a package relationship that may be conditional on its own: a
// chroot that does not build the Python package must not recommend it either.
type specRelation struct {
	Name    string
	Guarded bool
}

// specScriptlet is one lifecycle scriptlet, with its body already escaped for
// inclusion in a spec.
type specScriptlet struct {
	Section     string
	PackageName string
	Body        string
}

// WriteSpec renders an RPM spec from the shared Component metadata, giving the
// dependency model one source for both nfpm and rpmbuild.
//
// Payloads are staged later, inside the buildroot, by Stage. The version is
// written as a literal because COPR rebuilds the spec embedded in the SRPM once
// per chroot, inheriting no macro definitions from the SRPM build.
func WriteSpec(cfg Config, w io.Writer) error {
	version, release, err := RPMVersionRelease(cfg.Version)
	if err != nil {
		return err
	}

	data := &specData{
		Name:          SpecName,
		Version:       version,
		Release:       release,
		Summary:       metaDescription,
		License:       pkgLicense,
		URL:           pkgHomepage,
		ExclusiveArch: specExclusiveArch,
		BuildRequires: specBuildRequires,
		PythonGuard:   pythonGuard,
	}

	var unguarded []string
	for _, comp := range AllComponents {
		guarded := isGuarded(comp)

		pkg := specPackage{
			Component:   comp.Name,
			Name:        comp.PackageName,
			Summary:     comp.Description,
			Description: comp.Description,
			Noarch:      comp.Noarch,
			Provides:    comp.Relations.Provides,
			Requires:    comp.Relations.Depends,
			Suggests:    comp.Relations.Suggests,
			Guarded:     guarded,
		}
		for _, r := range comp.Relations.Recommends {
			pkg.Recommends = append(pkg.Recommends, specRelation{
				Name:    r,
				Guarded: strings.HasPrefix(r, Python.PackageName),
			})
		}
		data.Packages = append(data.Packages, pkg)

		if guarded {
			data.GuardedComponents = append(data.GuardedComponents, comp.Name)
		} else {
			unguarded = append(unguarded, comp.Name)
		}

		scriptlets, err := componentScriptlets(cfg, comp)
		if err != nil {
			return err
		}
		data.Scriptlets = append(data.Scriptlets, scriptlets...)
	}
	data.UnguardedComponents = strings.Join(unguarded, " ")

	return specTemplate.Execute(w, data)
}

func isGuarded(comp Component) bool {
	return comp.Name == Python.Name
}

func componentScriptlets(cfg Config, comp Component) ([]specScriptlet, error) {
	sources := []struct {
		section string
		file    string
	}{
		{"post", comp.PostInstall},
		{"preun", comp.PreRemove},
	}

	var scriptlets []specScriptlet
	for _, s := range sources {
		if s.file == "" {
			continue
		}
		body, err := readScript(cfg, s.file)
		if err != nil {
			return nil, err
		}
		scriptlets = append(scriptlets, specScriptlet{
			Section:     s.section,
			PackageName: comp.PackageName,
			Body:        body,
		})
	}
	return scriptlets, nil
}

func readScript(cfg Config, name string) (string, error) {
	path := filepath.Join(cfg.PackagingDir, "common", "scripts", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading lifecycle script: %w", err)
	}
	return escapePercent(strings.TrimRight(string(data), "\n")), nil
}

// escapePercent protects literal percent signs from RPM macro expansion, which
// applies inside comments and scriptlets too — preuninstall-injector.sh contains
// printf '%s\n'.
func escapePercent(s string) string {
	return strings.ReplaceAll(s, "%", "%%")
}

var versionSuffixInvalid = regexp.MustCompile(`[^A-Za-z0-9.]+`)

// RPMVersionRelease maps a package version to RPM Version and Release fields.
// The returned version also names the SRPM tarball, so the Makefile reads it
// from here instead of reimplementing the rules in shell.
//
// A pre-release suffix cannot stay in Version, where rpm reads a hyphen as the
// version-release separator and sorts "1.0.0_dev" above "1.0.0". Moving it into
// a "0." Release keeps every pre-release below the matching release.
func RPMVersionRelease(v string) (version, release string, err error) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return "", "", fmt.Errorf("empty version")
	}

	base, suffix, hasSuffix := strings.Cut(v, "-")
	if base == "" {
		return "", "", fmt.Errorf("invalid version %q", v)
	}
	if !hasSuffix {
		return base, "1%{?dist}", nil
	}

	sanitized := strings.Trim(versionSuffixInvalid.ReplaceAllString(suffix, "."), ".")
	if sanitized == "" {
		return "", "", fmt.Errorf("invalid version suffix in %q", v)
	}
	return base, "0." + sanitized + ".1%{?dist}", nil
}
