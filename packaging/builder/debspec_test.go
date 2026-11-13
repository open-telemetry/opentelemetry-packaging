// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderDebSpec(t *testing.T, version string, comp Component) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, WriteDebSpec(testConfig(t, version), comp, &b))
	return b.String()
}

func TestDebVersionRelease(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		release     string
		wantVersion string
		wantRelease string
		wantErr     bool
	}{
		{name: "release", version: "1.2.3", wantVersion: "1.2.3", wantRelease: "1"},
		{name: "release with v prefix", version: "v1.2.3", wantVersion: "1.2.3", wantRelease: "1"},
		{name: "development placeholder", version: "0.0.0-dev", wantVersion: "0.0.0~dev", wantRelease: "1"},
		{name: "release candidate", version: "1.0.0-rc.1", wantVersion: "1.0.0~rc.1", wantRelease: "1"},
		{name: "build metadata", version: "1.0.0-alpha+build", wantVersion: "1.0.0~alpha+build", wantRelease: "1"},
		{name: "explicit revision", version: "1.2.3", release: "2", wantVersion: "1.2.3", wantRelease: "2"},
		{name: "empty", version: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, release, err := DebVersionRelease(Config{Version: tt.version, Release: tt.release})
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

// The version must be the one nfpm writes, so a DEB built by debbuild and one
// built by nfpm from the same tag compare as the same upstream version.
func TestDebVersionReleaseMatchesNfpm(t *testing.T) {
	packager, err := nfpm.Get("deb")
	require.NoError(t, err)

	for _, v := range []string{"1.2.3", "0.0.0-dev", "1.0.0-rc.1", "1.0.0-alpha+build"} {
		info, cleanup, err := Meta.Info(testConfig(t, v), "deb")
		if cleanup != nil {
			defer cleanup()
		}
		require.NoError(t, err)

		// The file name nfpm gives the package spells out the version it
		// writes, after the defaults it applies while packaging.
		name := packager.ConventionalFileName(nfpm.WithDefaults(info))

		version, _, err := DebVersionRelease(testConfig(t, v))
		require.NoError(t, err)
		assert.Equal(t, Meta.PackageName+"_"+version+"_all.deb", name, "version %s", v)
	}
}

func TestWriteDebSpecPreamble(t *testing.T) {
	spec := renderDebSpec(t, "1.2.3", Injector)

	assert.Contains(t, spec, "Name:           opentelemetry-injector\n")
	assert.Contains(t, spec, "Version:        1.2.3\n")
	assert.Contains(t, spec, "Release:        1\n")
	assert.Contains(t, spec, "License:        Apache-2.0\n")
	assert.Contains(t, spec, "Packager:       The OpenTelemetry Authors\n")
	// debbuild writes Group out as the DEB Section field, and omits the field
	// altogether when the tag is absent.
	assert.Contains(t, spec, "Group:          utils\n")
	assert.Contains(t, spec, "Source0:        %{name}-%{version}.tar.gz\n")
	// The dependencies are the declared ones only, as in the nfpm packages.
	assert.Contains(t, spec, "AutoReq:        no\n")
	assert.NotContains(t, spec, "BuildRequires:", "the payload is prebuilt, the build needs nothing")
}

// Under the build's umask of 002 debbuild would ship group-writable directories
// and man pages; these are the two commands it creates them with.
func TestWriteDebSpecPackagesUnderUmask022(t *testing.T) {
	spec := renderDebSpec(t, "1.2.3", Injector)
	assert.Contains(t, spec, "%global __mkdir_p umask 022 && /bin/mkdir -p\n")
	assert.Contains(t, spec, "%global __gzip umask 022 && /bin/gzip\n")
}

func TestWriteDebSpecMirrorsTheDependencyModel(t *testing.T) {
	for _, comp := range AllComponents {
		t.Run(comp.Name, func(t *testing.T) {
			spec := renderDebSpec(t, "1.2.3", comp)
			for _, p := range comp.Relations.Provides {
				assert.Contains(t, spec, "Provides:       "+p+"\n")
			}
			for _, d := range comp.Relations.Depends {
				assert.Contains(t, spec, "Requires:       "+d+"\n")
			}
			for _, r := range comp.Relations.Recommends {
				assert.Contains(t, spec, "Recommends:     "+r+"\n")
			}
			for _, s := range comp.Relations.Suggests {
				assert.Contains(t, spec, "Suggests:       "+s+"\n")
			}
		})
	}
}

// debbuild reads Replaces directly; the RPM spec's Obsoletes spelling would
// work too, but the DEB field is what the relationship is called here.
func TestWriteDebSpecDisplacementRelations(t *testing.T) {
	comp := Component{
		Name:        "fixture",
		PackageName: "acme-java-autoinstrumentation",
		Description: "fixture",
		Relations: Relations{
			Conflicts: []string{"opentelemetry-java-autoinstrumentation"},
			Replaces:  []string{"opentelemetry-java-autoinstrumentation"},
		},
	}
	spec := renderDebSpec(t, "1.2.3", comp)

	assert.Contains(t, spec, "Conflicts:      opentelemetry-java-autoinstrumentation\n")
	assert.Contains(t, spec, "Replaces:       opentelemetry-java-autoinstrumentation\n")
	assert.NotContains(t, spec, "Obsoletes:")
}

func TestWriteDebSpecSelectsThePayloadByArchitecture(t *testing.T) {
	spec := renderDebSpec(t, "1.2.3", Injector)
	assert.Contains(t, spec, "%{name}-%{version}/%{_arch}/root")
	assert.Contains(t, spec, "%files -f %{_arch}/injector.files\n")
	assert.NotContains(t, spec, "BuildArch:")

	noarch := renderDebSpec(t, "1.2.3", Java)
	assert.Contains(t, noarch, "BuildArch:      noarch\n")
	assert.Contains(t, noarch, "%{name}-%{version}/all/root")
	assert.Contains(t, noarch, "%files -f all/java.files\n")
	assert.NotContains(t, noarch, "%{_arch}")
}

// An architecture the archive carries no payload for would otherwise fail in
// the install section, whose output debbuild discards, leaving nothing to go
// on but the exit status. The guard reports it while the spec is read.
func TestWriteDebSpecGuardsUnstagedArchitectures(t *testing.T) {
	spec := renderDebSpec(t, "1.2.3", Injector)
	archs := strings.Join(Architectures, " ")
	assert.Contains(t, spec, "%ifnarch "+archs+"\n")
	assert.Contains(t, spec, "%{error:this package is built for "+archs+", not %{_arch}}\n")

	// A noarch payload serves every architecture, so there is nothing to guard.
	assert.NotContains(t, renderDebSpec(t, "1.2.3", Java), "%ifnarch")
}

func TestWriteDebSpecInlinesLifecycleScripts(t *testing.T) {
	spec := renderDebSpec(t, "1.2.3", Injector)

	// One spec per package, so the scriptlets belong to the main package.
	assert.Contains(t, spec, "%post\n")
	assert.Contains(t, spec, "%preun\n")
	assert.NotContains(t, spec, "%post -n")
	// Percent signs in the scripts are escaped from macro expansion.
	assert.Contains(t, spec, "%%")

	assert.NotContains(t, renderDebSpec(t, "1.2.3", Java), "%post")
}

// Comments are macro-expanded too, so the template must not name a section
// with its percent sign anywhere but where the section starts.
func TestWriteDebSpecSectionsAppearOnce(t *testing.T) {
	spec := renderDebSpec(t, "1.2.3", Injector)
	for _, section := range []string{"%description", "%prep", "%build", "%install", "%files", "%post", "%preun"} {
		count := 0
		for line := range strings.SplitSeq(spec, "\n") {
			if line == section || strings.HasPrefix(line, section+" ") {
				count++
			}
		}
		assert.Equal(t, 1, count, "section %s", section)
	}
}

func TestStageDebFileListCarriesNoAttributes(t *testing.T) {
	src := writeSource(t, "libotelinject.so", "payload")
	conf := writeSource(t, "injector.conf", "conf\n")

	root := t.TempDir()
	filelistDir := t.TempDir()
	comp := Component{
		Name:        "fixture",
		PackageName: "opentelemetry-fixture",
		Description: "fixture",
		ContentsFunc: func(Config) (files.Contents, func(), error) {
			return files.Contents{
				regularFile(src, injectorInstallDir+"/libotelinject.so", 0o755),
				configFile(conf, injectorConfigDir+"/injector.conf"),
			}, func() {}, nil
		},
	}
	require.NoError(t, StageDeb(testConfig(t, "1.2.3"), comp, root, filelistDir))

	data, err := os.ReadFile(filepath.Join(filelistDir, "fixture.files"))
	require.NoError(t, err)
	list := string(data)

	// debbuild would turn every attr into a chown and chmod in the postinst.
	assert.NotContains(t, list, "%attr")
	assert.Contains(t, list, injectorInstallDir+"/libotelinject.so\n")
	// dpkg treats conffiles as noreplace anyway; debbuild knows no qualifier.
	assert.Contains(t, list, "%config "+injectorConfigDir+"/injector.conf\n")
	assert.Contains(t, list, "%dir "+injectorInstallDir+"\n")

	// The mode the file list leaves out is in the staged tree.
	info, err := os.Stat(filepath.Join(root, injectorInstallDir, "libotelinject.so"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// readArchive returns the entries of a gzipped tar by name.
func readArchive(t *testing.T, path string) map[string]*tar.Header {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	tr := tar.NewReader(gz)

	entries := map[string]*tar.Header{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return entries
		}
		require.NoError(t, err)
		entries[hdr.Name] = hdr
	}
}

func debFixtureComponent(t *testing.T, noarch bool) Component {
	t.Helper()
	src := writeSource(t, "payload.bin", "payload")
	return Component{
		Name:        "fixture",
		PackageName: "opentelemetry-fixture",
		Description: "fixture",
		Noarch:      noarch,
		ContentsFunc: func(cfg Config) (files.Contents, func(), error) {
			return files.Contents{
				regularFile(src, installDir+"/fixture/"+cfg.Arch+".bin", 0o755),
			}, func() {}, nil
		},
	}
}

func TestWriteDebSourcesStagesEveryArchitecture(t *testing.T) {
	out := t.TempDir()
	comp := debFixtureComponent(t, false)
	var checkBinaries []string
	checkBinary := func(arch string) string {
		checkBinaries = append(checkBinaries, arch)
		return "/nonexistent/otel-config-check-" + arch
	}

	sources, err := WriteDebSources(testConfig(t, "1.2.3"), comp, checkBinary, out)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(out, "opentelemetry-fixture.spec"), sources.Spec)
	assert.Equal(t, filepath.Join(out, "opentelemetry-fixture-1.2.3.tar.gz"), sources.Archive)
	assert.Equal(t, Architectures, checkBinaries)

	entries := readArchive(t, sources.Archive)
	for _, arch := range Architectures {
		prefix := "opentelemetry-fixture-1.2.3/" + arch + "/"
		bin := entries[prefix+"root"+installDir+"/fixture/"+arch+".bin"]
		require.NotNil(t, bin, "payload for %s", arch)
		assert.Equal(t, int64(0o755), bin.Mode&0o777)
		assert.Equal(t, 0, bin.Uid)
		assert.Equal(t, "root", bin.Uname)
		assert.NotNil(t, entries[prefix+"fixture.files"], "file list for %s", arch)
	}

	spec, err := os.ReadFile(sources.Spec)
	require.NoError(t, err)
	assert.Contains(t, string(spec), "Name:           opentelemetry-fixture\n")
}

func TestWriteDebSourcesStagesNoarchOnce(t *testing.T) {
	sources, err := WriteDebSources(testConfig(t, "1.2.3"), debFixtureComponent(t, true),
		func(string) string { return "" }, t.TempDir())
	require.NoError(t, err)

	var dirs []string
	for name := range readArchive(t, sources.Archive) {
		parts := strings.Split(name, "/")
		if len(parts) == 3 && parts[2] == "" {
			dirs = append(dirs, parts[1])
		}
	}
	assert.Equal(t, []string{DebNoarchDir}, dirs)
}

func TestWriteDebSourcesIsReproducible(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	comp := debFixtureComponent(t, false)
	noCheck := func(string) string { return "" }

	first, err := WriteDebSources(testConfig(t, "1.2.3"), comp, noCheck, t.TempDir())
	require.NoError(t, err)
	second, err := WriteDebSources(testConfig(t, "1.2.3"), comp, noCheck, t.TempDir())
	require.NoError(t, err)

	a, err := os.ReadFile(first.Archive)
	require.NoError(t, err)
	b, err := os.ReadFile(second.Archive)
	require.NoError(t, err)
	assert.Equal(t, a, b, "identical payloads must give identical archives")
}
