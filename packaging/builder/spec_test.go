// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testConfig returns a Config pointing at the real packaging/ directory, which
// the spec generator reads the lifecycle scripts from. It performs no downloads.
func testConfig(t *testing.T, version string) Config {
	t.Helper()
	packagingDir, err := filepath.Abs("..")
	require.NoError(t, err)
	return Config{
		Version:      version,
		Arch:         "amd64",
		PackagingDir: packagingDir,
	}
}

func renderSpec(t *testing.T, version string) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, WriteSpec(testConfig(t, version), &b))
	return b.String()
}

func TestRPMVersionRelease(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		wantVersion string
		wantRelease string
		wantErr     bool
	}{
		{name: "release", version: "1.2.3", wantVersion: "1.2.3", wantRelease: "1%{?dist}"},
		{name: "release with v prefix", version: "v1.2.3", wantVersion: "1.2.3", wantRelease: "1%{?dist}"},
		{name: "development placeholder", version: "0.0.0-dev", wantVersion: "0.0.0", wantRelease: "0.dev.1%{?dist}"},
		{name: "release candidate", version: "1.0.0-rc.1", wantVersion: "1.0.0", wantRelease: "0.rc.1.1%{?dist}"},
		{name: "suffix with invalid characters", version: "1.0.0-alpha+build", wantVersion: "1.0.0", wantRelease: "0.alpha.build.1%{?dist}"},
		{name: "empty", version: "", wantErr: true},
		{name: "only a suffix", version: "-dev", wantErr: true},
		{name: "empty suffix", version: "1.0.0-", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, release, err := RPMVersionRelease(tt.version)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, version)
			assert.Equal(t, tt.wantRelease, release)
		})
	}
}

// A development build must never shadow the release it precedes, which is the
// bug the earlier proof of concept shipped with its "0.0.0_dev" version.
func TestRPMVersionReleaseKeepsPreReleaseBelowRelease(t *testing.T) {
	devVersion, devRelease, err := RPMVersionRelease("1.0.0-dev")
	require.NoError(t, err)
	relVersion, relRelease, err := RPMVersionRelease("1.0.0")
	require.NoError(t, err)

	assert.Equal(t, relVersion, devVersion,
		"a pre-release must not alter the Version field, or rpm compares the wrong strings")
	assert.True(t, strings.HasPrefix(devRelease, "0."),
		"a pre-release Release must start with 0. to sort below %q, got %q", relRelease, devRelease)
	assert.False(t, strings.HasPrefix(relRelease, "0."))
}

func TestWriteSpecPreamble(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	assert.Contains(t, spec, "Name:           opentelemetry-packaging")
	assert.Contains(t, spec, "Version:        1.2.3")
	assert.Contains(t, spec, "Release:        1%{?dist}")
	assert.Contains(t, spec, "Source0:        %{name}-%{version}.tar.gz")
	assert.Contains(t, spec, "ExclusiveArch:  x86_64 aarch64")

	// Prebuilt binaries: the debuginfo machinery must stay off.
	assert.Contains(t, spec, "%global debug_package %{nil}")
	assert.Contains(t, spec, "%global __strip /bin/true")
	assert.Contains(t, spec, "%global _build_id_links none")
}

// The bundled libraries are private: rpm must not publish their SONAMEs as
// system-wide provides, where they could satisfy an unrelated package. Nothing
// resolves them by SONAME — the injector is preloaded by absolute path — so
// filtering costs nothing. Requires generation must stay on, so that the glibc
// symbol versions the bundled binaries need keep gating installation.
func TestWriteSpecDoesNotPublishBundledLibraries(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	assert.Contains(t, spec, "%global __provides_exclude_from ^%{_prefix}/lib/opentelemetry/.*$")
	assert.NotContains(t, spec, "%global __requires_exclude_from",
		"automatically generated requires are real constraints and must not be filtered")
	assert.NotContains(t, spec, "%{_libdir}/opentelemetry",
		"these packages install to /usr/lib on every architecture, not /usr/lib64")
}

// The version must be a literal: COPR rebuilds the spec embedded in the SRPM
// per chroot without the defines passed at SRPM build time.
func TestWriteSpecBakesVersionLiterally(t *testing.T) {
	spec := renderSpec(t, "0.0.0-dev")

	assert.Contains(t, spec, "Version:        0.0.0")
	assert.Contains(t, spec, "Release:        0.dev.1%{?dist}")
	assert.NotContains(t, spec, "%{!?",
		"the spec must not resolve its version from an optional macro definition")
	assert.NotContains(t, spec, "otel_version",
		"the version must not be indirected through a macro at all")
}

func TestWriteSpecDeclaresEveryPackage(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	for _, comp := range AllComponents {
		assert.Contains(t, spec, "%package -n "+comp.PackageName+"\n",
			"missing subpackage for component %s", comp.Name)
		assert.Contains(t, spec,
			"%files -n "+comp.PackageName+" -f %{_builddir}/filelists/"+comp.Name+".files",
			"missing files section for component %s", comp.Name)
	}

	// The source package itself ships nothing, so it must not have a %files
	// section of its own — that is what allows a noarch metapackage.
	assert.NotContains(t, spec, "\n%files\n")
}

func TestWriteSpecMirrorsTheDependencyModel(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	// Interface-versioned virtual names, per docs/design/packages-meta-architecture.md.
	assert.Contains(t, spec, "Provides:       opentelemetry-injector1")
	assert.Contains(t, spec, "Provides:       opentelemetry-java-autoinstrumentation1")
	assert.Contains(t, spec, "Suggests:       opentelemetry-injector1")
	assert.Contains(t, spec, "Requires:       opentelemetry-injector1")
	assert.Contains(t, spec, "Recommends:     opentelemetry-java-autoinstrumentation1")

	// The metapackage must not hard-depend on a language package.
	assert.NotContains(t, spec, "Requires:       opentelemetry-java-autoinstrumentation1")
}

func TestWriteSpecArchitectures(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	// Every noarch package, and only those, carries a BuildArch line.
	var wantNoarch, wantArched []string
	for _, comp := range AllComponents {
		if comp.Noarch {
			wantNoarch = append(wantNoarch, comp.PackageName)
		} else {
			wantArched = append(wantArched, comp.PackageName)
		}
	}
	require.NotEmpty(t, wantNoarch)
	require.NotEmpty(t, wantArched)

	for _, name := range wantNoarch {
		assert.Contains(t, subpackageStanza(t, spec, name), "BuildArch:      noarch",
			"%s is architecture-independent and must be noarch", name)
	}
	for _, name := range wantArched {
		assert.NotContains(t, subpackageStanza(t, spec, name), "BuildArch:",
			"%s carries architecture-specific files and must not be noarch", name)
	}
}

// subpackageStanza returns the %package stanza for one package name.
func subpackageStanza(t *testing.T, spec, packageName string) string {
	t.Helper()
	start := strings.Index(spec, "%package -n "+packageName+"\n")
	require.GreaterOrEqual(t, start, 0, "no stanza for %s", packageName)
	rest := spec[start:]
	end := strings.Index(rest, "\n%description")
	require.GreaterOrEqual(t, end, 0, "unterminated stanza for %s", packageName)
	return rest[:end]
}

func TestWriteSpecInlinesLifecycleScripts(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	assert.Contains(t, spec, "%post -n opentelemetry-injector\n")
	assert.Contains(t, spec, "%preun -n opentelemetry-injector\n")
	assert.Contains(t, spec, "/etc/ld.so.preload",
		"the inlined scriptlets must carry the preload registration")

	// Only the injector has lifecycle scripts.
	assert.Equal(t, 1, strings.Count(spec, "\n%post -n "))
	assert.Equal(t, 1, strings.Count(spec, "\n%preun -n "))
}

// Every literal percent sign must be escaped, or rpm expands it as a macro —
// including inside comments, which is how an unescaped "%install" once swallowed
// a whole preamble on EL9.
func TestWriteSpecEscapesPercentSigns(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	// preuninstall-injector.sh contains printf '%s\n'.
	assert.Contains(t, spec, `printf '%%s\n'`,
		"the printf format in the inlined scriptlet must be escaped")
	assert.NotContains(t, spec, `printf '%s\n'`)

	for line := range strings.SplitSeq(spec, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Drop the escaped pairs first: what is left is an unescaped macro.
		bare := strings.ReplaceAll(trimmed, "%%", "")
		for _, section := range []string{"%install", "%build", "%prep", "%files", "%post", "%preun", "%changelog"} {
			assert.NotContains(t, bare, section,
				"comment %q contains an unescaped section macro", trimmed)
		}
	}
}

// A generated spec whose conditionals do not balance fails to parse, and the
// Python guard adds several of them.
func TestWriteSpecConditionalsBalance(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	var opened, closed int
	for line := range strings.SplitSeq(spec, "\n") {
		switch trimmed := strings.TrimSpace(line); {
		case strings.HasPrefix(trimmed, "%endif"):
			closed++
		case strings.HasPrefix(trimmed, "%if"):
			opened++
		}
	}

	assert.Positive(t, opened)
	assert.Equal(t, opened, closed, "every conditional must be closed")
}

func TestEscapePercent(t *testing.T) {
	assert.Equal(t, "%%install", escapePercent("%install"))
	assert.Equal(t, `printf '%%s\n'`, escapePercent(`printf '%s\n'`))
	assert.Equal(t, "no percent here", escapePercent("no percent here"))
	assert.Equal(t, "%%%%", escapePercent("%%"))
}

// The Python package is gated: its wheels are built for a single CPython ABI, so
// chroots whose interpreter differs must not build it — nor recommend it.
func TestWriteSpecGuardsThePythonPackage(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	assert.Contains(t, spec, "%global with_python")

	pythonStanza := subpackageStanza(t, spec, Python.PackageName)
	assert.Contains(t, spec[:strings.Index(spec, pythonStanza)], "%if %{with_python}")

	// The metapackage's recommendation of Python is conditional too.
	metaStanza := subpackageStanza(t, spec, Meta.PackageName)
	require.Contains(t, metaStanza, Python.PackageName+"1")
	recommendIdx := strings.Index(metaStanza, "Recommends:     "+Python.PackageName+"1")
	require.GreaterOrEqual(t, recommendIdx, 0)
	assert.Contains(t, metaStanza[:recommendIdx], "%if %{with_python}")
}

func TestWriteSpecStagesEveryComponent(t *testing.T) {
	spec := renderSpec(t, "1.2.3")

	assert.Contains(t, spec, "-stage-root %{buildroot}")
	assert.Contains(t, spec, "-filelist-dir %{_builddir}/filelists")
	assert.Contains(t, spec, "-arch %{otel_arch}")

	install := spec[strings.Index(spec, "\n%install\n"):]
	for _, comp := range AllComponents {
		assert.Contains(t, install, comp.Name,
			"component %s is never staged in %%install", comp.Name)
	}
}
