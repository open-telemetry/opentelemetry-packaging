// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderOBSSpec(t *testing.T, version string, comp Component) string {
	t.Helper()
	var b strings.Builder
	require.NoError(t, WriteOBSSpec(testConfig(t, version), comp, &b))
	return b.String()
}

func TestOBSVersionRelease(t *testing.T) {
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
			version, release, err := OBSVersionRelease(Config{Version: tt.version, Release: tt.release})
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
func TestOBSVersionReleaseMatchesNfpm(t *testing.T) {
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

		version, _, err := OBSVersionRelease(testConfig(t, v))
		require.NoError(t, err)
		assert.Equal(t, Meta.PackageName+"_"+version+"_all.deb", name, "version %s", v)
	}
}

func TestWriteOBSSpecPreamble(t *testing.T) {
	spec := renderOBSSpec(t, "1.2.3", Injector)

	assert.Contains(t, spec, "Name:           opentelemetry-injector\n")
	assert.Contains(t, spec, "Version:        1.2.3\n")
	assert.Contains(t, spec, "Release:        1\n")
	assert.Contains(t, spec, "License:        Apache-2.0\n")
	assert.Contains(t, spec, "Source0:        %{name}-%{version}.tar.gz\n")

	// debbuild writes Group out as the DEB Section field, and omits the field altogether when the tag is absent.
	// Packager becomes the Maintainer.
	// rpm gets neither, so both sit under the debbuild branch.
	debbuild, rpm := vendorBranches(t, spec)
	assert.Contains(t, debbuild, "Group:          utils\n")
	assert.NotContains(t, rpm, "Group:")
	assert.Contains(t, spec, "Packager:       The OpenTelemetry Authors\n")
	// The dependencies are the declared ones only, as in the nfpm packages.
	// For rpm they are whatever it generates from the binaries, which is how the glibc symbol versions reach its Requires.
	assert.Contains(t, debbuild, "AutoReq:        no\n")
	assert.NotContains(t, rpm, "AutoReq")
	assert.NotContains(t, spec, "BuildRequires:", "the payload is prebuilt, the build needs nothing")
}

// Under the build's umask of 002 debbuild would ship group-writable directories
// and man pages; these are the two commands it creates them with.
func TestWriteOBSSpecPackagesUnderUmask022(t *testing.T) {
	debbuild, rpm := vendorBranches(t, renderOBSSpec(t, "1.2.3", Injector))
	assert.Contains(t, debbuild, "%global __mkdir_p umask 022 && /bin/mkdir -p\n")
	assert.Contains(t, debbuild, "%global __gzip umask 022 && /bin/gzip\n")
	assert.NotContains(t, rpm, "__mkdir_p", "rpm builds with its own macros")
}

// The prebuilt payload must reach rpm untouched, and its glibc symbol versions must remain install constraints; the same globals the source RPM sets.
func TestWriteOBSSpecKeepsPrebuiltBinariesIntactUnderRpm(t *testing.T) {
	debbuild, rpm := vendorBranches(t, renderOBSSpec(t, "1.2.3", Injector))
	for _, want := range []string{
		"%global debug_package %{nil}\n",
		"%global __strip /bin/true\n",
		"%global _build_id_links none\n",
		"%global __brp_mangle_shebangs %{nil}\n",
		"%global __brp_python_bytecompile %{nil}\n",
		"%global source_date_epoch_from_changelog 0\n",
		"%global __provides_exclude_from ^%{_prefix}/lib/opentelemetry/.*$\n",
		"%global __requires_exclude ^%{_bindir}/(node|python3)$\n",
	} {
		assert.Contains(t, rpm, want)
		assert.NotContains(t, debbuild, want)
	}
}

// vendorBranches splits the part of a spec that is conditional on the vendor into what debbuild sees and what rpm sees.
// It handles the top level conditionals only, which is all the template has.
func vendorBranches(t *testing.T, spec string) (debbuild, rpm string) {
	t.Helper()
	var d, r strings.Builder
	// state: 0 outside, 1 debbuild branch, 2 rpm branch
	state := 0
	nested := 0
	for line := range strings.SplitSeq(spec, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case state == 0 && trimmed == `%if "%{_vendor}" == "debbuild"`:
			state = 1
			continue
		case state != 0 && strings.HasPrefix(trimmed, "%if"):
			nested++
		case state != 0 && strings.HasPrefix(trimmed, "%ifarch"):
			nested++
		case state != 0 && nested == 0 && trimmed == "%else":
			state = 2
			continue
		case state != 0 && trimmed == "%endif":
			if nested > 0 {
				nested--
				break
			}
			state = 0
			continue
		}
		switch state {
		case 1:
			d.WriteString(line + "\n")
		case 2:
			r.WriteString(line + "\n")
		}
	}
	require.Equal(t, 0, state, "unbalanced vendor conditional")
	return d.String(), r.String()
}

func TestWriteOBSSpecMirrorsTheDependencyModel(t *testing.T) {
	for _, comp := range AllComponents {
		t.Run(comp.Name, func(t *testing.T) {
			spec := renderOBSSpec(t, "1.2.3", comp)
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

// The relationship is Replaces to debbuild and Obsoletes to rpm.
func TestWriteOBSSpecDisplacementRelations(t *testing.T) {
	comp := Component{
		Name:        "fixture",
		PackageName: "acme-java-autoinstrumentation",
		Description: "fixture",
		Relations: Relations{
			Conflicts: []string{"opentelemetry-java-autoinstrumentation"},
			Replaces:  []string{"opentelemetry-java-autoinstrumentation"},
		},
	}
	spec := renderOBSSpec(t, "1.2.3", comp)

	assert.Contains(t, spec, "Conflicts:      opentelemetry-java-autoinstrumentation\n")
	debbuild, rpm := vendorBranches(t, spec)
	assert.Contains(t, debbuild, "Replaces:       opentelemetry-java-autoinstrumentation\n")
	assert.NotContains(t, debbuild, "Obsoletes:")
	assert.Contains(t, rpm, "Obsoletes:      opentelemetry-java-autoinstrumentation\n")
	assert.NotContains(t, rpm, "Replaces:")
}

func TestWriteOBSSpecSelectsThePayloadByArchitecture(t *testing.T) {
	spec := renderOBSSpec(t, "1.2.3", Injector)
	assert.Contains(t, spec, "%{name}-%{version}/%{payload_arch}/root")
	assert.Contains(t, spec, "%files -f %{payload_arch}/%{filelist}\n")
	assert.NotContains(t, spec, "BuildArch:")

	// The archive names payloads after the DEB architectures, so rpm maps its own.
	debbuild, rpm := vendorBranches(t, spec)
	assert.Contains(t, debbuild, "%global payload_arch %{_arch}\n")
	assert.Contains(t, rpm, "%ifarch x86_64\n%global payload_arch amd64\n%endif\n")
	assert.Contains(t, rpm, "%ifarch aarch64\n%global payload_arch arm64\n%endif\n")

	// Each format reads the file list written for it.
	assert.Contains(t, debbuild, "%global filelist injector.deb.files\n")
	assert.Contains(t, rpm, "%global filelist injector.rpm.files\n")

	noarch := renderOBSSpec(t, "1.2.3", Java)
	assert.Contains(t, noarch, "BuildArch:      noarch\n")
	assert.Contains(t, noarch, "%global payload_arch all\n")
	assert.NotContains(t, noarch, "%{_arch}")
	assert.NotContains(t, noarch, "%ifarch")
}

// An architecture the archive carries no payload for would otherwise fail in
// the install section, whose output debbuild discards, leaving nothing to go
// on but the exit status. The guard reports it while the spec is read.
func TestWriteOBSSpecGuardsUnstagedArchitectures(t *testing.T) {
	spec := renderOBSSpec(t, "1.2.3", Injector)
	debbuild, rpm := vendorBranches(t, spec)
	assert.Contains(t, debbuild, "%global payload_archs "+strings.Join(Architectures, " ")+"\n")
	assert.Contains(t, rpm, "%global payload_archs x86_64 aarch64\n")
	assert.Contains(t, spec, "%ifnarch %{payload_archs}\n")
	assert.Contains(t, spec, "%{error:this package is built for %{payload_archs}, not %{_arch}}\n")

	// A noarch payload serves every architecture, so there is nothing to guard.
	assert.NotContains(t, renderOBSSpec(t, "1.2.3", Java), "%ifnarch %{payload_archs}")
}

func TestWriteOBSSpecInlinesLifecycleScripts(t *testing.T) {
	spec := renderOBSSpec(t, "1.2.3", Injector)

	// One spec per package, so the scriptlets belong to the main package.
	assert.Contains(t, spec, "%post\n")
	assert.Contains(t, spec, "%preun\n")
	assert.NotContains(t, spec, "%post -n")
	// Percent signs in the scripts are escaped from macro expansion.
	assert.Contains(t, spec, "%%")

	assert.NotContains(t, renderOBSSpec(t, "1.2.3", Java), "%post")
}

// Comments are macro-expanded too, so the template must not name a section
// with its percent sign anywhere but where the section starts.
func TestWriteOBSSpecSectionsAppearOnce(t *testing.T) {
	spec := renderOBSSpec(t, "1.2.3", Injector)
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

func TestStageOBSWritesAFileListPerFormat(t *testing.T) {
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
	require.NoError(t, StageOBS(testConfig(t, "1.2.3"), comp, root, filelistDir))

	data, err := os.ReadFile(filepath.Join(filelistDir, "fixture"+DEBFileListSuffix))
	require.NoError(t, err)
	deb := string(data)

	// debbuild would turn every attr into a chown and chmod in the postinst.
	assert.NotContains(t, deb, "%attr")
	assert.Contains(t, deb, injectorInstallDir+"/libotelinject.so\n")
	// dpkg treats conffiles as noreplace anyway; debbuild knows no qualifier.
	assert.Contains(t, deb, "%config "+injectorConfigDir+"/injector.conf\n")
	assert.Contains(t, deb, "%dir "+injectorInstallDir+"\n")

	// rpmbuild runs as an unprivileged user and needs the ownership spelled out.
	data, err = os.ReadFile(filepath.Join(filelistDir, "fixture"+RPMFileListSuffix))
	require.NoError(t, err)
	rpm := string(data)
	assert.Contains(t, rpm, "%attr(0755,root,root) "+injectorInstallDir+"/libotelinject.so\n")
	assert.Contains(t, rpm, "%config(noreplace) %attr(0644,root,root) "+injectorConfigDir+"/injector.conf\n")
	assert.Contains(t, rpm, "%dir %attr(")

	// The same files, whichever format lists them.
	assert.Equal(t, listedPaths(deb), listedPaths(rpm))

	// The mode the DEB file list leaves out is in the staged tree.
	info, err := os.Stat(filepath.Join(root, injectorInstallDir, "libotelinject.so"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// listedPaths returns the paths a file list names, without its directives.
func listedPaths(list string) []string {
	var paths []string
	for line := range strings.SplitSeq(strings.TrimSpace(list), "\n") {
		fields := strings.Fields(line)
		paths = append(paths, fields[len(fields)-1])
	}
	sort.Strings(paths)
	return paths
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

func obsFixtureComponent(t *testing.T, noarch bool) Component {
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

func TestWriteOBSSourcesStagesEveryArchitecture(t *testing.T) {
	out := t.TempDir()
	comp := obsFixtureComponent(t, false)
	var checkBinaries []string
	checkBinary := func(arch string) string {
		checkBinaries = append(checkBinaries, arch)
		return "/nonexistent/otel-config-check-" + arch
	}

	sources, err := WriteOBSSources(testConfig(t, "1.2.3"), comp, checkBinary, out)
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
		assert.NotNil(t, entries[prefix+"fixture"+DEBFileListSuffix], "DEB file list for %s", arch)
		assert.NotNil(t, entries[prefix+"fixture"+RPMFileListSuffix], "RPM file list for %s", arch)
	}

	spec, err := os.ReadFile(sources.Spec)
	require.NoError(t, err)
	assert.Contains(t, string(spec), "Name:           opentelemetry-fixture\n")
}

func TestWriteOBSSourcesStagesNoarchOnce(t *testing.T) {
	sources, err := WriteOBSSources(testConfig(t, "1.2.3"), obsFixtureComponent(t, true),
		func(string) string { return "" }, t.TempDir())
	require.NoError(t, err)

	var dirs []string
	for name := range readArchive(t, sources.Archive) {
		parts := strings.Split(name, "/")
		if len(parts) == 3 && parts[2] == "" {
			dirs = append(dirs, parts[1])
		}
	}
	assert.Equal(t, []string{OBSNoarchDir}, dirs)
}

func TestWriteOBSSourcesIsReproducible(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	comp := obsFixtureComponent(t, false)
	noCheck := func(string) string { return "" }

	first, err := WriteOBSSources(testConfig(t, "1.2.3"), comp, noCheck, t.TempDir())
	require.NoError(t, err)
	second, err := WriteOBSSources(testConfig(t, "1.2.3"), comp, noCheck, t.TempDir())
	require.NoError(t, err)

	a, err := os.ReadFile(first.Archive)
	require.NoError(t, err)
	b, err := os.ReadFile(second.Archive)
	require.NoError(t, err)
	assert.Equal(t, a, b, "identical payloads must give identical archives")
}

// rpm expands macros in comments, and an unescaped section macro in one makes rpm 4.16 (EL 9) start that section there: "second install" is how it fails.
// Fedora's newer rpm tolerates it, so nothing but this test and an EL build catches a comment that names a section with its percent sign.
func TestWriteOBSSpecCommentsNameNoMacros(t *testing.T) {
	for _, comp := range AllComponents {
		t.Run(comp.Name, func(t *testing.T) {
			for line := range strings.SplitSeq(renderOBSSpec(t, "1.2.3", comp), "\n") {
				// A doubled percent sign is escaped, as in the lifecycle scripts.
				if strings.HasPrefix(line, "#") {
					assert.NotContains(t, strings.ReplaceAll(line, "%%", ""), "%",
						"comment expands as a macro: %q", line)
				}
			}
		})
	}
}
