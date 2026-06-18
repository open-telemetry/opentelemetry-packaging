// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package metadata_test validates that built packages declare correct metadata
// (Provides, Depends, Suggests) and contain the expected files. These tests
// run on the host against built .deb/.rpm artifacts — no containers needed.
package metadata_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TODO: replace dpkgInfo, dpkgContents, dpkgField, hasDpkgDeb and skipIfNoDebTools
// with pault.ag/go/debian/deb which can parse .deb archives natively in Go
// (control file fields, data tar listing) without requiring dpkg-deb on the host.

// dpkgInfo returns the output of `dpkg-deb --info` for a .deb file.
func dpkgInfo(t *testing.T, debPath string) string {
	t.Helper()
	out, err := exec.Command("dpkg-deb", "--info", debPath).CombinedOutput()
	require.NoError(t, err, "dpkg-deb --info failed: %s", string(out))
	return string(out)
}

// dpkgContents returns the output of `dpkg-deb --contents` for a .deb file.
func dpkgContents(t *testing.T, debPath string) string {
	t.Helper()
	out, err := exec.Command("dpkg-deb", "--contents", debPath).CombinedOutput()
	require.NoError(t, err, "dpkg-deb --contents failed: %s", string(out))
	return string(out)
}

// dpkgField returns a specific field from a .deb package.
func dpkgField(t *testing.T, debPath, field string) string {
	t.Helper()
	out, err := exec.Command("dpkg-deb", "--field", debPath, field).CombinedOutput()
	require.NoError(t, err, "dpkg-deb --field %s failed: %s", field, string(out))
	return strings.TrimSpace(string(out))
}

// hasDpkgDeb returns true if dpkg-deb is available on the host.
func hasDpkgDeb() bool {
	_, err := exec.LookPath("dpkg-deb")
	return err == nil
}

// TODO: replace hasRpm, skipIfNoRpmTools and the inline exec.Command("rpm", ...)
// calls with github.com/cavaliergopher/rpm which can parse .rpm headers natively
// in Go — typed Provides(), Requires(), Suggests(), Recommends(), Files() methods
// — without requiring the rpm CLI on the host.

// hasRpm returns true if rpm is available on the host.
func hasRpm() bool {
	_, err := exec.LookPath("rpm")
	return err == nil
}

// hasDebPackages returns true if .deb packages have been built.
func hasDebPackages(t *testing.T) bool {
	t.Helper()
	root := testutil.RepoRoot(t)
	matches, _ := filepath.Glob(filepath.Join(root, "build", "packages", "*.deb"))
	return len(matches) > 0
}

func skipIfNoDebTools(t *testing.T) {
	t.Helper()
	if !hasDpkgDeb() {
		t.Skip("dpkg-deb not available on this host")
	}
	if !hasDebPackages(t) {
		t.Skip("no .deb packages built — run 'make deb-packages' first")
	}
}

// --------------------------------------------------------------------------
// DEB Package Metadata Tests
// --------------------------------------------------------------------------

func TestDebInjectorMetadata(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-injector_", ".deb")
	info := dpkgInfo(t, pkg)

	assert.Contains(t, info, "opentelemetry-injector1",
		"injector should Provide opentelemetry-injector1")
	assert.NotContains(t, dpkgField(t, pkg, "Depends"), "sed",
		"injector should not depend on sed")
	assert.NotContains(t, dpkgField(t, pkg, "Depends"), "grep",
		"injector should not depend on grep")
}

func TestDebInjectorContents(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-injector_", ".deb")
	contents := dpkgContents(t, pkg)

	assert.Contains(t, contents, "/usr/lib/opentelemetry/injector/libotelinject.so",
		"should contain injector library")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/injector.conf",
		"should contain injector config")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/default_env.conf",
		"should contain default env config")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/",
		"should contain conf.d directory")
	assert.Contains(t, contents, "/usr/share/man/",
		"should contain man page")
}

func TestDebJavaMetadata(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	info := dpkgInfo(t, pkg)

	assert.Contains(t, info, "opentelemetry-java-autoinstrumentation1",
		"Java package should Provide opentelemetry-java-autoinstrumentation1")

	depends := dpkgField(t, pkg, "Depends")
	assert.NotContains(t, depends, "opentelemetry-injector",
		"Java package should not hard-depend on injector")
}

func TestDebJavaContents(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	contents := dpkgContents(t, pkg)

	assert.Contains(t, contents, "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar",
		"should contain Java agent JAR")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/java.conf",
		"should contain Java conf.d drop-in")
}

func TestDebNodejsMetadata(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	info := dpkgInfo(t, pkg)

	assert.Contains(t, info, "opentelemetry-nodejs-autoinstrumentation1",
		"Node.js package should Provide opentelemetry-nodejs-autoinstrumentation1")

	depends := dpkgField(t, pkg, "Depends")
	assert.NotContains(t, depends, "opentelemetry-injector",
		"Node.js package should not hard-depend on injector")
}

func TestDebNodejsContents(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	contents := dpkgContents(t, pkg)

	assert.Contains(t, contents, "register.js",
		"should contain Node.js register.js")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/nodejs.conf",
		"should contain Node.js conf.d drop-in")
}

func TestDebDotnetMetadata(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	info := dpkgInfo(t, pkg)

	assert.Contains(t, info, "opentelemetry-dotnet-autoinstrumentation1",
		".NET package should Provide opentelemetry-dotnet-autoinstrumentation1")

	depends := dpkgField(t, pkg, "Depends")
	assert.NotContains(t, depends, "opentelemetry-injector",
		".NET package should not hard-depend on injector")
}

func TestDebDotnetContents(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	contents := dpkgContents(t, pkg)

	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/dotnet.conf",
		"should contain .NET conf.d drop-in")
}

func TestDebMetapackageMetadata(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry_", ".deb")
	depends := dpkgField(t, pkg, "Depends")

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
	contents := dpkgContents(t, pkg)
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "/usr/share/doc/") {
			continue
		}
		// Directories are fine.
		if strings.HasPrefix(line, "d") {
			continue
		}
		// The only files should be under /usr/share/doc/.
		assert.Contains(t, line, "/usr/share/doc/",
			"metapackage should only contain doc files, found: %s", line)
	}
}

// --------------------------------------------------------------------------
// Conf.d content validation (DEB)
// --------------------------------------------------------------------------

func TestDebJavaConfDContent(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")

	// Extract the conf.d file content and verify it references the correct JAR path.
	tmpDir := t.TempDir()
	extractCmd := exec.Command("dpkg-deb", "--raw-extract", pkg, tmpDir)
	extractOut, err := extractCmd.CombinedOutput()
	require.NoError(t, err, "dpkg-deb --raw-extract failed: %s", string(extractOut))

	confPath := filepath.Join(tmpDir, "etc", "opentelemetry", "injector", "conf.d", "java.conf")
	confData, err := os.ReadFile(confPath)
	require.NoError(t, err, "could not read java.conf from extracted package")

	assert.Contains(t, string(confData), "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar",
		"java.conf should reference the correct JAR path")
}

func TestDebJavaSuggestsInjector(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation_", ".deb")
	suggests := dpkgField(t, pkg, "Suggests")

	assert.Contains(t, suggests, "opentelemetry-injector1",
		"Java package should Suggest opentelemetry-injector1")
}

func TestDebNodejsSuggestsInjector(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")
	suggests := dpkgField(t, pkg, "Suggests")

	assert.Contains(t, suggests, "opentelemetry-injector1",
		"Node.js package should Suggest opentelemetry-injector1")
}

func TestDebDotnetSuggestsInjector(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")
	suggests := dpkgField(t, pkg, "Suggests")

	assert.Contains(t, suggests, "opentelemetry-injector1",
		".NET package should Suggest opentelemetry-injector1")
}

func TestDebNodejsConfDContent(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation_", ".deb")

	tmpDir := t.TempDir()
	extractCmd := exec.Command("dpkg-deb", "--raw-extract", pkg, tmpDir)
	extractOut, err := extractCmd.CombinedOutput()
	require.NoError(t, err, "dpkg-deb --raw-extract failed: %s", string(extractOut))

	confPath := filepath.Join(tmpDir, "etc", "opentelemetry", "injector", "conf.d", "nodejs.conf")
	confData, err := os.ReadFile(confPath)
	require.NoError(t, err, "could not read nodejs.conf from extracted package")

	assert.Contains(t, string(confData), "register.js",
		"nodejs.conf should reference register.js")
}

func TestDebDotnetConfDContent(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation_", ".deb")

	tmpDir := t.TempDir()
	extractCmd := exec.Command("dpkg-deb", "--raw-extract", pkg, tmpDir)
	extractOut, err := extractCmd.CombinedOutput()
	require.NoError(t, err, "dpkg-deb --raw-extract failed: %s", string(extractOut))

	confPath := filepath.Join(tmpDir, "etc", "opentelemetry", "injector", "conf.d", "dotnet.conf")
	confData, err := os.ReadFile(confPath)
	require.NoError(t, err, "could not read dotnet.conf from extracted package")

	assert.Contains(t, string(confData), "/usr/lib/opentelemetry/dotnet",
		"dotnet.conf should reference the correct path prefix")
}

func TestDebMetapackageRecommendsLanguagePackages(t *testing.T) {
	skipIfNoDebTools(t)

	pkg := findPackage(t, "opentelemetry_", ".deb")
	recommends := dpkgField(t, pkg, "Recommends")

	assert.Contains(t, recommends, "opentelemetry-java-autoinstrumentation1",
		"metapackage should Recommend opentelemetry-java-autoinstrumentation1")
	assert.Contains(t, recommends, "opentelemetry-nodejs-autoinstrumentation1",
		"metapackage should Recommend opentelemetry-nodejs-autoinstrumentation1")
	assert.Contains(t, recommends, "opentelemetry-dotnet-autoinstrumentation1",
		"metapackage should Recommend opentelemetry-dotnet-autoinstrumentation1")
}

// --------------------------------------------------------------------------
// Skip helpers for RPM
// --------------------------------------------------------------------------

func skipIfNoRpmTools(t *testing.T) {
	t.Helper()
	if !hasRpm() {
		t.Skip("rpm not available on this host")
	}
	root := testutil.RepoRoot(t)
	matches, _ := filepath.Glob(filepath.Join(root, "build", "packages", "*.rpm"))
	if len(matches) == 0 {
		t.Skip("no .rpm packages built — run 'make rpm-packages' first")
	}
}

// --------------------------------------------------------------------------
// RPM Package Metadata Tests (mirror the DEB tests)
// --------------------------------------------------------------------------

func TestRpmInjectorMetadata(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-injector-", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--provides", pkg).CombinedOutput()
	require.NoError(t, err)
	provides := string(out)

	assert.Contains(t, provides, "opentelemetry-injector1",
		"injector should Provide opentelemetry-injector1")

	reqOut, err := exec.Command("rpm", "-qp", "--requires", pkg).CombinedOutput()
	require.NoError(t, err)
	requires := string(reqOut)

	assert.NotContains(t, requires, "sed",
		"injector should not require sed")
	assert.NotContains(t, requires, "grep",
		"injector should not require grep")
}

func TestRpmInjectorContents(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-injector-", ".rpm")

	out, err := exec.Command("rpm", "-qpl", pkg).CombinedOutput()
	require.NoError(t, err)
	contents := string(out)

	assert.Contains(t, contents, "/usr/lib/opentelemetry/injector/libotelinject.so")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/injector.conf")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/default_env.conf")
}

func TestRpmMetapackageMetadata(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-0", ".rpm")

	reqOut, err := exec.Command("rpm", "-qp", "--requires", pkg).CombinedOutput()
	require.NoError(t, err)
	requires := string(reqOut)

	assert.Contains(t, requires, "opentelemetry-injector1",
		"metapackage should Require opentelemetry-injector1")

	// Language packages should be in Recommends, NOT in Requires.
	assert.NotContains(t, requires, "opentelemetry-java-autoinstrumentation1",
		"metapackage should not hard-require Java — should be in Recommends")
	assert.NotContains(t, requires, "opentelemetry-nodejs-autoinstrumentation1",
		"metapackage should not hard-require Node.js — should be in Recommends")
	assert.NotContains(t, requires, "opentelemetry-dotnet-autoinstrumentation1",
		"metapackage should not hard-require .NET — should be in Recommends")
}

func TestRpmJavaMetadata(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--provides", pkg).CombinedOutput()
	require.NoError(t, err)
	provides := string(out)

	assert.Contains(t, provides, "opentelemetry-java-autoinstrumentation1",
		"Java RPM should Provide opentelemetry-java-autoinstrumentation1")

	reqOut, err := exec.Command("rpm", "-qp", "--requires", pkg).CombinedOutput()
	require.NoError(t, err)
	requires := string(reqOut)

	assert.NotContains(t, requires, "opentelemetry-injector",
		"Java RPM should not hard-require injector")
}

func TestRpmJavaContents(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qpl", pkg).CombinedOutput()
	require.NoError(t, err)
	contents := string(out)

	assert.Contains(t, contents, "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/java.conf")
}

func TestRpmNodejsMetadata(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--provides", pkg).CombinedOutput()
	require.NoError(t, err)
	provides := string(out)

	assert.Contains(t, provides, "opentelemetry-nodejs-autoinstrumentation1",
		"Node.js RPM should Provide opentelemetry-nodejs-autoinstrumentation1")

	reqOut, err := exec.Command("rpm", "-qp", "--requires", pkg).CombinedOutput()
	require.NoError(t, err)
	requires := string(reqOut)

	assert.NotContains(t, requires, "opentelemetry-injector",
		"Node.js RPM should not hard-require injector")
}

func TestRpmNodejsContents(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qpl", pkg).CombinedOutput()
	require.NoError(t, err)
	contents := string(out)

	assert.Contains(t, contents, "register.js")
	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/nodejs.conf")
}

func TestRpmDotnetMetadata(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--provides", pkg).CombinedOutput()
	require.NoError(t, err)
	provides := string(out)

	assert.Contains(t, provides, "opentelemetry-dotnet-autoinstrumentation1",
		".NET RPM should Provide opentelemetry-dotnet-autoinstrumentation1")

	reqOut, err := exec.Command("rpm", "-qp", "--requires", pkg).CombinedOutput()
	require.NoError(t, err)
	requires := string(reqOut)

	assert.NotContains(t, requires, "opentelemetry-injector",
		".NET RPM should not hard-require injector")
}

func TestRpmDotnetContents(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qpl", pkg).CombinedOutput()
	require.NoError(t, err)
	contents := string(out)

	assert.Contains(t, contents, "/etc/opentelemetry/injector/conf.d/dotnet.conf")
}

// --------------------------------------------------------------------------
// RPM Suggests validation (requires rpm --supplements or xml parsing)
// --------------------------------------------------------------------------

func TestRpmJavaSuggestsInjector(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-java-autoinstrumentation-", ".rpm")

	// RPM Suggests are shown via --supplements or by querying the XML directly.
	// Use --qf to query the Suggests tag.
	out, err := exec.Command("rpm", "-qp", "--qf", "[%{SUGGESTSNAME}\n]", pkg).CombinedOutput()
	if err != nil {
		t.Skip("rpm version does not support SUGGESTSNAME query")
	}
	suggests := string(out)

	assert.Contains(t, suggests, "opentelemetry-injector1",
		"Java RPM should Suggest opentelemetry-injector1")
}

func TestRpmNodejsSuggestsInjector(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-nodejs-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--qf", "[%{SUGGESTSNAME}\n]", pkg).CombinedOutput()
	if err != nil {
		t.Skip("rpm version does not support SUGGESTSNAME query")
	}
	suggests := string(out)

	assert.Contains(t, suggests, "opentelemetry-injector1",
		"Node.js RPM should Suggest opentelemetry-injector1")
}

func TestRpmDotnetSuggestsInjector(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-dotnet-autoinstrumentation-", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--qf", "[%{SUGGESTSNAME}\n]", pkg).CombinedOutput()
	if err != nil {
		t.Skip("rpm version does not support SUGGESTSNAME query")
	}
	suggests := string(out)

	assert.Contains(t, suggests, "opentelemetry-injector1",
		".NET RPM should Suggest opentelemetry-injector1")
}

func TestRpmMetapackageRecommendsLanguagePackages(t *testing.T) {
	skipIfNoRpmTools(t)

	pkg := findPackage(t, "opentelemetry-0", ".rpm")

	out, err := exec.Command("rpm", "-qp", "--qf", "[%{RECOMMENDSNAME}\n]", pkg).CombinedOutput()
	if err != nil {
		t.Skip("rpm version does not support RECOMMENDSNAME query")
	}
	recommends := string(out)

	assert.Contains(t, recommends, "opentelemetry-java-autoinstrumentation1")
	assert.Contains(t, recommends, "opentelemetry-nodejs-autoinstrumentation1")
	assert.Contains(t, recommends, "opentelemetry-dotnet-autoinstrumentation1")
}

// --------------------------------------------------------------------------
// Host tool detection — print a helpful message if tools are missing
// --------------------------------------------------------------------------

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
