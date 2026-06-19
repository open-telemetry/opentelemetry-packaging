// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package metadata_test validates that built packages declare correct metadata
// (Provides, Depends, Suggests, Recommends) and contain the expected files.
// These tests run on the host against built .deb/.rpm artifacts — no containers
// or CLI tools (dpkg-deb, rpm) needed; packages are parsed natively in Go.
package metadata_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cavaliergopher/rpm"
	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pault.ag/go/debian/deb"
)

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// findPackage finds a .deb or .rpm file matching the given name prefix in the
// build output directory.
func findPackage(t *testing.T, namePrefix, ext string) string {
	t.Helper()
	root := testutil.RepoRoot(t)
	pattern := filepath.Join(root, "build", "packages", namePrefix+"*"+ext)
	matches, err := filepath.Glob(pattern)
	require.NoError(t, err)
	require.NotEmpty(t, matches, "no %s package found matching %s", ext, pattern)
	return matches[0]
}

// openDeb opens a .deb file and returns the parsed Deb struct.
func openDeb(t *testing.T, path string) *deb.Deb {
	t.Helper()
	d, closer, err := deb.LoadFile(path)
	require.NoError(t, err)
	t.Cleanup(func() { closer() })
	return d
}

// debProvides returns the Provides field from a .deb's control paragraph.
func debProvides(t *testing.T, d *deb.Deb) string {
	t.Helper()
	return d.Control.Paragraph.Values["Provides"]
}

// debDataFiles returns all file paths from the .deb data archive.
func debDataFiles(t *testing.T, path string) []string {
	t.Helper()
	d, closer, err := deb.LoadFile(path)
	require.NoError(t, err)
	defer closer()

	var paths []string
	for {
		hdr, err := d.Data.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		paths = append(paths, hdr.Name)
	}
	return paths
}

// debExtractFile extracts a single file from the .deb data archive and returns
// its contents.
func debExtractFile(t *testing.T, path, target string) string {
	t.Helper()
	d, closer, err := deb.LoadFile(path)
	require.NoError(t, err)
	defer closer()

	for {
		hdr, err := d.Data.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		// Normalize: strip leading "./" if present.
		name := strings.TrimPrefix(hdr.Name, ".")
		if name == target || strings.TrimPrefix(name, "/") == strings.TrimPrefix(target, "/") {
			data, err := io.ReadAll(d.Data)
			require.NoError(t, err)
			return string(data)
		}
	}
	t.Fatalf("file %s not found in %s", target, path)
	return ""
}

// pathsContain checks if any path in the list contains the given substring.
func pathsContain(paths []string, substr string) bool {
	for _, p := range paths {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

// openRpm opens an .rpm file and returns the parsed Package.
func openRpm(t *testing.T, path string) *rpm.Package {
	t.Helper()
	p, err := rpm.Open(path)
	require.NoError(t, err)
	return p
}

// rpmDepsContain checks if a dependency slice contains the named package.
func rpmDepsContain(deps []rpm.Dependency, name string) bool {
	for _, d := range deps {
		if d.Name() == name {
			return true
		}
	}
	return false
}

// rpmFileNames returns all file paths from an RPM package.
func rpmFileNames(p *rpm.Package) []string {
	var names []string
	for _, f := range p.Files() {
		names = append(names, f.Name())
	}
	return names
}

// hasDebPackages returns true if .deb packages have been built.
func hasDebPackages(t *testing.T) bool {
	t.Helper()
	root := testutil.RepoRoot(t)
	matches, _ := filepath.Glob(filepath.Join(root, "build", "packages", "*.deb"))
	return len(matches) > 0
}

// hasRpmPackages returns true if .rpm packages have been built.
func hasRpmPackages(t *testing.T) bool {
	t.Helper()
	root := testutil.RepoRoot(t)
	matches, _ := filepath.Glob(filepath.Join(root, "build", "packages", "*.rpm"))
	return len(matches) > 0
}

func skipIfNoDebPackages(t *testing.T) {
	t.Helper()
	if !hasDebPackages(t) {
		t.Skip("no .deb packages built — run 'make deb-packages' first")
	}
}

func skipIfNoRpmPackages(t *testing.T) {
	t.Helper()
	if !hasRpmPackages(t) {
		t.Skip("no .rpm packages built — run 'make rpm-packages' first")
	}
}

// --------------------------------------------------------------------------
// DEB Package Metadata Tests
// --------------------------------------------------------------------------

func TestDebInjectorMetadata(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-injector_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, debProvides(t, d), "opentelemetry-injector1",
		"injector should Provide opentelemetry-injector1")
	assert.NotContains(t, d.Control.Depends.String(), "sed",
		"injector should not depend on sed")
	assert.NotContains(t, d.Control.Depends.String(), "grep",
		"injector should not depend on grep")
}

func TestDebInjectorContents(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-injector_", ".deb")
	paths := debDataFiles(t, pkg)

	assert.True(t, pathsContain(paths, "/usr/lib/opentelemetry/injector/libotelinject.so"),
		"should contain injector library")
	assert.True(t, pathsContain(paths, "/etc/opentelemetry/injector/injector.conf"),
		"should contain injector config")
	assert.True(t, pathsContain(paths, "/etc/opentelemetry/injector/default_env.conf"),
		"should contain default env config")
	assert.True(t, pathsContain(paths, "/etc/opentelemetry/injector/conf.d"),
		"should contain conf.d directory")
	assert.True(t, pathsContain(paths, "/usr/share/man/"),
		"should contain man page")
}

func TestDebJavaMetadata(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, debProvides(t, d), "opentelemetry-java-autoinstrumentation1",
		"Java package should Provide opentelemetry-java-autoinstrumentation1")
	assert.NotContains(t, d.Control.Depends.String(), "opentelemetry-injector",
		"Java package should not hard-depend on injector")
}

func TestDebJavaContents(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	paths := debDataFiles(t, pkg)

	assert.True(t, pathsContain(paths, "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar"),
		"should contain Java agent JAR")
	assert.True(t, pathsContain(paths, "/etc/opentelemetry/injector/conf.d/java.conf"),
		"should contain Java conf.d drop-in")
}

func TestDebNodejsMetadata(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, debProvides(t, d), "opentelemetry-nodejs-autoinstrumentation1",
		"Node.js package should Provide opentelemetry-nodejs-autoinstrumentation1")
	assert.NotContains(t, d.Control.Depends.String(), "opentelemetry-injector",
		"Node.js package should not hard-depend on injector")
}

func TestDebNodejsContents(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	paths := debDataFiles(t, pkg)

	assert.True(t, pathsContain(paths, "register.js"),
		"should contain Node.js register.js")
	assert.True(t, pathsContain(paths, "/etc/opentelemetry/injector/conf.d/nodejs.conf"),
		"should contain Node.js conf.d drop-in")
}

func TestDebDotnetMetadata(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, debProvides(t, d), "opentelemetry-dotnet-autoinstrumentation1",
		".NET package should Provide opentelemetry-dotnet-autoinstrumentation1")
	assert.NotContains(t, d.Control.Depends.String(), "opentelemetry-injector",
		".NET package should not hard-depend on injector")
}

func TestDebDotnetContents(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	paths := debDataFiles(t, pkg)

	assert.True(t, pathsContain(paths, "/etc/opentelemetry/injector/conf.d/dotnet.conf"),
		"should contain .NET conf.d drop-in")
}

func TestDebMetapackageMetadata(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry_", ".deb")
	d := openDeb(t, pkg)
	depends := d.Control.Depends.String()

	assert.Contains(t, depends, "opentelemetry-injector1",
		"metapackage should depend on opentelemetry-injector1")

	// Language packages should be in Recommends, NOT in Depends.
	assert.NotContains(t, depends, "opentelemetry-java-autoinstrumentation1",
		"metapackage should not hard-depend on Java — should be in Recommends")
	assert.NotContains(t, depends, "opentelemetry-nodejs-autoinstrumentation1",
		"metapackage should not hard-depend on Node.js — should be in Recommends")
	assert.NotContains(t, depends, "opentelemetry-dotnet-autoinstrumentation1",
		"metapackage should not hard-depend on .NET — should be in Recommends")

	// Should NOT depend on concrete package names.
	assert.NotContains(t, depends, "opentelemetry-injector ",
		"metapackage should not depend on concrete injector package name")

	// Check it does not have any files beyond doc.
	paths := debDataFiles(t, pkg)
	for _, p := range paths {
		name := strings.TrimPrefix(p, ".")
		if name == "" || strings.Contains(name, "/usr/share/doc/") {
			continue
		}
		// Directories are fine.
		if strings.HasSuffix(name, "/") {
			continue
		}
		assert.Contains(t, name, "/usr/share/doc/",
			"metapackage should only contain doc files, found: %s", name)
	}
}

// --------------------------------------------------------------------------
// DEB Conf.d content validation
// --------------------------------------------------------------------------

func TestDebJavaConfDContent(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	content := debExtractFile(t, pkg, "/etc/opentelemetry/injector/conf.d/java.conf")

	assert.Contains(t, content, "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar",
		"java.conf should reference the correct JAR path")
}

func TestDebNodejsConfDContent(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	content := debExtractFile(t, pkg, "/etc/opentelemetry/injector/conf.d/nodejs.conf")

	assert.Contains(t, content, "register.js",
		"nodejs.conf should reference register.js")
}

func TestDebDotnetConfDContent(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	content := debExtractFile(t, pkg, "/etc/opentelemetry/injector/conf.d/dotnet.conf")

	assert.Contains(t, content, "/usr/lib/opentelemetry/dotnet",
		"dotnet.conf should reference the correct path prefix")
}

// --------------------------------------------------------------------------
// DEB Suggests validation
// --------------------------------------------------------------------------

func TestDebJavaSuggestsInjector(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, d.Control.Suggests.String(), "opentelemetry-injector1",
		"Java package should Suggest opentelemetry-injector1")
}

func TestDebNodejsSuggestsInjector(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, d.Control.Suggests.String(), "opentelemetry-injector1",
		"Node.js package should Suggest opentelemetry-injector1")
}

func TestDebDotnetSuggestsInjector(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	d := openDeb(t, pkg)

	assert.Contains(t, d.Control.Suggests.String(), "opentelemetry-injector1",
		".NET package should Suggest opentelemetry-injector1")
}

// --------------------------------------------------------------------------
// DEB Recommends validation
// --------------------------------------------------------------------------

func TestDebMetapackageRecommendsLanguagePackages(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry_", ".deb")
	d := openDeb(t, pkg)
	recommends := d.Control.Recommends.String()

	assert.Contains(t, recommends, "opentelemetry-java-autoinstrumentation1",
		"metapackage should Recommend opentelemetry-java-autoinstrumentation1")
	assert.Contains(t, recommends, "opentelemetry-nodejs-autoinstrumentation1",
		"metapackage should Recommend opentelemetry-nodejs-autoinstrumentation1")
	assert.Contains(t, recommends, "opentelemetry-dotnet-autoinstrumentation1",
		"metapackage should Recommend opentelemetry-dotnet-autoinstrumentation1")
}

// --------------------------------------------------------------------------
// RPM Package Metadata Tests
// --------------------------------------------------------------------------

func TestRpmInjectorMetadata(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-injector-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Provides(), "opentelemetry-injector1"),
		"injector should Provide opentelemetry-injector1")

	for _, dep := range p.Requires() {
		assert.NotEqual(t, "sed", dep.Name(), "injector should not require sed")
		assert.NotEqual(t, "grep", dep.Name(), "injector should not require grep")
	}
}

func TestRpmInjectorContents(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-injector-", ".rpm")
	p := openRpm(t, pkg)
	names := rpmFileNames(p)

	assert.True(t, pathsContain(names, "/usr/lib/opentelemetry/injector/libotelinject.so"))
	assert.True(t, pathsContain(names, "/etc/opentelemetry/injector/injector.conf"))
	assert.True(t, pathsContain(names, "/etc/opentelemetry/injector/default_env.conf"))
}

func TestRpmJavaMetadata(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Provides(), "opentelemetry-java-autoinstrumentation1"),
		"Java RPM should Provide opentelemetry-java-autoinstrumentation1")

	assert.False(t, rpmDepsContain(p.Requires(), "opentelemetry-injector"),
		"Java RPM should not hard-require injector")
}

func TestRpmJavaContents(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)
	names := rpmFileNames(p)

	assert.True(t, pathsContain(names, "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar"))
	assert.True(t, pathsContain(names, "/etc/opentelemetry/injector/conf.d/java.conf"))
}

func TestRpmNodejsMetadata(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Provides(), "opentelemetry-nodejs-autoinstrumentation1"),
		"Node.js RPM should Provide opentelemetry-nodejs-autoinstrumentation1")

	assert.False(t, rpmDepsContain(p.Requires(), "opentelemetry-injector"),
		"Node.js RPM should not hard-require injector")
}

func TestRpmNodejsContents(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)
	names := rpmFileNames(p)

	assert.True(t, pathsContain(names, "register.js"))
	assert.True(t, pathsContain(names, "/etc/opentelemetry/injector/conf.d/nodejs.conf"))
}

func TestRpmDotnetMetadata(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Provides(), "opentelemetry-dotnet-autoinstrumentation1"),
		".NET RPM should Provide opentelemetry-dotnet-autoinstrumentation1")

	assert.False(t, rpmDepsContain(p.Requires(), "opentelemetry-injector"),
		".NET RPM should not hard-require injector")
}

func TestRpmDotnetContents(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)
	names := rpmFileNames(p)

	assert.True(t, pathsContain(names, "/etc/opentelemetry/injector/conf.d/dotnet.conf"))
}

func TestRpmMetapackageMetadata(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-0", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Requires(), "opentelemetry-injector1"),
		"metapackage should Require opentelemetry-injector1")

	// Language packages should be in Recommends, NOT in Requires.
	assert.False(t, rpmDepsContain(p.Requires(), "opentelemetry-java-autoinstrumentation1"),
		"metapackage should not hard-require Java — should be in Recommends")
	assert.False(t, rpmDepsContain(p.Requires(), "opentelemetry-nodejs-autoinstrumentation1"),
		"metapackage should not hard-require Node.js — should be in Recommends")
	assert.False(t, rpmDepsContain(p.Requires(), "opentelemetry-dotnet-autoinstrumentation1"),
		"metapackage should not hard-require .NET — should be in Recommends")
}

// --------------------------------------------------------------------------
// RPM Suggests validation
// --------------------------------------------------------------------------

func TestRpmJavaSuggestsInjector(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Suggests(), "opentelemetry-injector1"),
		"Java RPM should Suggest opentelemetry-injector1")
}

func TestRpmNodejsSuggestsInjector(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Suggests(), "opentelemetry-injector1"),
		"Node.js RPM should Suggest opentelemetry-injector1")
}

func TestRpmDotnetSuggestsInjector(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation-", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Suggests(), "opentelemetry-injector1"),
		".NET RPM should Suggest opentelemetry-injector1")
}

// --------------------------------------------------------------------------
// RPM Recommends validation
// --------------------------------------------------------------------------

func TestRpmMetapackageRecommendsLanguagePackages(t *testing.T) {
	skipIfNoRpmPackages(t)

	pkg := findPackage(t, "opentelemetry-0", ".rpm")
	p := openRpm(t, pkg)

	assert.True(t, rpmDepsContain(p.Recommends(), "opentelemetry-java-autoinstrumentation1"))
	assert.True(t, rpmDepsContain(p.Recommends(), "opentelemetry-nodejs-autoinstrumentation1"))
	assert.True(t, rpmDepsContain(p.Recommends(), "opentelemetry-dotnet-autoinstrumentation1"))
}

// --------------------------------------------------------------------------
// Host tool detection — print a helpful message if tools are missing
// --------------------------------------------------------------------------

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
