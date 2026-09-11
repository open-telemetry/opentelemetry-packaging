// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goreleaser/nfpm/v2/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureComponent is a minimal component standing in for a vendor package. It
// stages one file from a temporary directory, so it needs no PackagingDir.
func fixtureComponent(t *testing.T, rel Relations) Component {
	t.Helper()
	return Component{
		Name:        "fixture",
		PackageName: "fixture-autoinstrumentation",
		Description: "Fixture package",
		Noarch:      true,
		Relations:   rel,
		ContentsFunc: func(Config) (files.Contents, func(), error) {
			dir := t.TempDir()
			payload := filepath.Join(dir, "payload")
			if err := os.WriteFile(payload, []byte("payload\n"), 0o644); err != nil {
				return nil, nil, err
			}
			return files.Contents{
				RegularFile(payload, "/usr/lib/opentelemetry/fixture/payload", 0o644),
			}, func() {}, nil
		},
	}
}

// TestIdentityDefaults pins the promise that an empty Config keeps the
// repository's own package identity, so adding the override fields changes
// nothing for existing callers.
func TestIdentityDefaults(t *testing.T) {
	cfg := Config{Version: "1.2.3", Arch: "amd64"}

	assert.Equal(t, pkgVendor, cfg.vendor())
	assert.Equal(t, pkgMaintainer, cfg.maintainer())
	assert.Equal(t, pkgLicense, cfg.license())
	assert.Equal(t, pkgHomepage, cfg.homepage())

	info, cleanup, err := fixtureComponent(t, Relations{}).Info(cfg, "deb")
	if cleanup != nil {
		defer cleanup()
	}
	require.NoError(t, err)
	assert.Equal(t, "OpenTelemetry", info.Vendor)
	assert.Equal(t, "The OpenTelemetry Authors", info.Maintainer)
	assert.Equal(t, "Apache-2.0", info.License)
	assert.Equal(t, pkgHomepage, info.Homepage)
}

// TestIdentityOverride covers the vendor case: branding the package without
// touching the packaging rules.
func TestIdentityOverride(t *testing.T) {
	cfg := Config{
		Version:    "1.2.3",
		Arch:       "amd64",
		Vendor:     "ACME",
		Maintainer: "The ACME Company",
		License:    "MIT",
		Homepage:   "https://acme.example",
	}

	info, cleanup, err := fixtureComponent(t, Relations{}).Info(cfg, "deb")
	if cleanup != nil {
		defer cleanup()
	}
	require.NoError(t, err)
	assert.Equal(t, "ACME", info.Vendor)
	assert.Equal(t, "The ACME Company", info.Maintainer)
	assert.Equal(t, "MIT", info.License)
	assert.Equal(t, "https://acme.example", info.Homepage)
}

// TestRelationsConflictsReplaces is the core of the vendor recipe: the virtual
// name in Provides, the concrete upstream name in Conflicts and Replaces.
func TestRelationsConflictsReplaces(t *testing.T) {
	comp := fixtureComponent(t, Relations{
		Provides:  []string{"opentelemetry-java-autoinstrumentation1"},
		Conflicts: []string{"opentelemetry-java-autoinstrumentation"},
		Replaces:  []string{"opentelemetry-java-autoinstrumentation"},
		Suggests:  []string{"opentelemetry-injector1"},
	})

	for _, format := range []string{"deb", "rpm"} {
		t.Run(format, func(t *testing.T) {
			info, cleanup, err := comp.Info(Config{Version: "1.0.0", Arch: "amd64"}, format)
			if cleanup != nil {
				defer cleanup()
			}
			require.NoError(t, err)
			assert.Equal(t, []string{"opentelemetry-java-autoinstrumentation1"}, info.Provides)
			assert.Equal(t, []string{"opentelemetry-java-autoinstrumentation"}, info.Conflicts)
			assert.Equal(t, []string{"opentelemetry-java-autoinstrumentation"}, info.Replaces)
			assert.Equal(t, []string{"opentelemetry-injector1"}, info.Suggests)
		})
	}
}

// TestUpstreamComponentsDeclareNoDisplacement guards the additive claim: none
// of the shipped components displaces anything, so the new fields cannot alter
// their metadata.
func TestUpstreamComponentsDeclareNoDisplacement(t *testing.T) {
	for _, comp := range AllComponents {
		t.Run(comp.Name, func(t *testing.T) {
			assert.Empty(t, comp.Relations.Conflicts)
			assert.Empty(t, comp.Relations.Replaces)
		})
	}
}

// TestContentHelperAliases keeps the exported helpers and the in-repo
// unexported names interchangeable, so components read the same either way.
func TestContentHelperAliases(t *testing.T) {
	assert.Equal(t, configFile("s", "d"), ConfigFile("s", "d"))
	assert.Equal(t, regularFile("s", "d", 0o600), RegularFile("s", "d", 0o600))
	assert.Equal(t, directory("d"), Directory("d"))
	assert.Equal(t, tree("s", "d"), Tree("s", "d"))

	// The nfpm types carry the packaging semantics, so pin them too.
	assert.Equal(t, "config|noreplace", ConfigFile("s", "d").Type)
	assert.Equal(t, os.FileMode(0o644), os.FileMode(ConfigFile("s", "d").FileInfo.Mode))
	assert.Equal(t, "dir", Directory("d").Type)
	assert.Equal(t, os.FileMode(0o755), os.FileMode(Directory("d").FileInfo.Mode))
	assert.Equal(t, "tree", Tree("s", "d").Type)
}

// TestGenerateManPageArbitraryTemplate covers a vendor naming its man pages
// after its own packages rather than after opentelemetry-<component>.
func TestGenerateManPageArbitraryTemplate(t *testing.T) {
	dir := t.TempDir()
	tmpl := filepath.Join(dir, "acme-java.8.tmpl")
	require.NoError(t, os.WriteFile(tmpl, []byte("version @VERSION@ dated @DATE@\n"), 0o644))

	staging := t.TempDir()
	out, err := GenerateManPage(Config{Version: "9.9.9"}, staging, tmpl)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(staging, "acme-java.8.gz"), out)

	f, err := os.Open(out)
	require.NoError(t, err)
	defer f.Close()
	gr, err := gzip.NewReader(f)
	require.NoError(t, err)
	body, err := io.ReadAll(gr)
	require.NoError(t, err)

	assert.Contains(t, string(body), "version 9.9.9 dated ")
	assert.NotContains(t, string(body), "@VERSION@")
	assert.NotContains(t, string(body), "@DATE@")
}

func TestGenerateManPageRejectsNonTemplate(t *testing.T) {
	_, err := GenerateManPage(Config{Version: "1.0.0"}, t.TempDir(), "/nowhere/acme-java.8")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".tmpl")
}

// TestNoticeFile checks the default still resolves to the repository layout,
// and that a caller can point it elsewhere.
func TestNoticeFile(t *testing.T) {
	cfg := Config{PackagingDir: "/src/repo/packaging"}
	assert.Equal(t, filepath.Join("/src/repo", "NOTICE"), cfg.noticeFile())

	cfg.NoticeFile = "/elsewhere/NOTICE"
	assert.Equal(t, "/elsewhere/NOTICE", cfg.noticeFile())
}

// TestBuildReturnsPath covers Build handing back the artifact path, which a
// caller needs in order to report, sign, or publish it.
func TestBuildReturnsPath(t *testing.T) {
	out := t.TempDir()
	cfg := Config{Version: "1.0.0", Arch: "amd64", OutputDir: out, Vendor: "ACME"}

	path, err := Build(cfg, "deb", fixtureComponent(t, Relations{}))
	require.NoError(t, err)

	assert.Equal(t, out, filepath.Dir(path))
	assert.Equal(t, "fixture-autoinstrumentation_1.0.0_all.deb", filepath.Base(path))
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, st.Size())
}

// TestWriteSpecRendersDisplacement checks the rpmbuild path keeps parity with
// nfpm, so a vendor gets the same relations from either producer.
func TestWriteSpecRendersDisplacement(t *testing.T) {
	original := AllComponents
	t.Cleanup(func() { AllComponents = original })
	AllComponents = append(append([]Component{}, original...), fixtureComponent(t, Relations{
		Provides:  []string{"opentelemetry-java-autoinstrumentation1"},
		Conflicts: []string{"opentelemetry-java-autoinstrumentation"},
		Replaces:  []string{"opentelemetry-java-autoinstrumentation"},
	}))

	var b strings.Builder
	require.NoError(t, WriteSpec(testConfig(t, "1.0.0"), &b))
	spec := b.String()

	assert.Contains(t, spec, "Conflicts:      opentelemetry-java-autoinstrumentation")
	// Obsoletes is how a spec file spells Replaces.
	assert.Contains(t, spec, "Obsoletes:      opentelemetry-java-autoinstrumentation")
}

// TestWriteSpecIdentityOverride covers the spec path reading identity from
// Config, which is the half of the seam that is easy to leave behind.
func TestWriteSpecIdentityOverride(t *testing.T) {
	cfg := testConfig(t, "1.0.0")
	cfg.License = "MIT"
	cfg.Homepage = "https://acme.example"

	var b strings.Builder
	require.NoError(t, WriteSpec(cfg, &b))
	spec := b.String()

	assert.Contains(t, spec, "License:        MIT")
	assert.Contains(t, spec, "URL:            https://acme.example")
}
