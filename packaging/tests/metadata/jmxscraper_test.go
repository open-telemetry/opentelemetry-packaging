// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Metadata assertions for opentelemetry-jmx-scraper.
//
// The scraper sits outside the dependency graph the other packages form, so
// what needs protecting here is different: that it stays independent, that its
// Java runtime stays a suggestion rather than a dependency, and that the
// suggestion keeps its per-ecosystem spelling. See
// docs/design/packages-meta-architecture.md.
package metadata_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cavaliergopher/rpm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pault.ag/go/debian/deb"
)

// RPM file flags. A config file the package manager must not overwrite on
// upgrade carries both, which is what nfpm's "config|noreplace" type means.
const (
	rpmFileConfig    = 1 << 0
	rpmFileNoReplace = 1 << 4
)

// jmxScraperFiles is the payload every format must ship.
var jmxScraperFiles = []string{
	"/usr/lib/opentelemetry-jmx-scraper/opentelemetry-jmx-scraper.jar",
	"/usr/lib/systemd/system/opentelemetry-jmx-scraper.service",
	"/etc/opentelemetry/jmx-scraper/config.properties",
	"/etc/opentelemetry/jmx-scraper/jmx-scraper.env",
	"/usr/share/man/man8/opentelemetry-jmx-scraper.8.gz",
	"/usr/share/doc/opentelemetry-jmx-scraper/README.md",
}

// jmxScraperConfigFiles are the two files an operator edits, which an upgrade
// must never overwrite.
var jmxScraperConfigFiles = []string{
	"/etc/opentelemetry/jmx-scraper/config.properties",
	"/etc/opentelemetry/jmx-scraper/jmx-scraper.env",
}

// --------------------------------------------------------------------------
// DEB
// --------------------------------------------------------------------------

// The Java runtime must stay a suggestion: a hard dependency would conflict
// with a JVM installed by a version manager or by hand. The DEB spelling is an
// alternation, which has no RPM equivalent.
func TestDebJMXScraperSuggestsJavaRuntime(t *testing.T) {
	skipIfNoDebPackages(t)

	d := openDeb(t, findPackage(t, "opentelemetry-jmx-scraper_", ".deb"))

	suggests := d.Control.Suggests.String()
	assert.Contains(t, suggests, "default-jre",
		"the JMX scraper should suggest the Debian default JRE")
	assert.Contains(t, suggests, "java8-runtime",
		"the suggestion should offer the java8-runtime virtual as an alternative")
}

// The scraper is deliberately outside the dependency graph: no injector
// relationship, no metapackage relationship, and no virtual name of its own.
func TestDebJMXScraperIsIndependent(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-jmx-scraper_", ".deb")
	d := openDeb(t, pkg)

	assert.Empty(t, strings.TrimSpace(debProvides(t, d)),
		"the JMX scraper implements no injector-mediated contract, so it provides no virtual name")
	assert.Empty(t, strings.TrimSpace(d.Control.Depends.String()),
		"the JMX scraper must not depend on the injector, the metapackage, or a Java runtime")
}

// The payload is a JAR, a unit, and documentation, none of which is
// architecture specific.
func TestDebJMXScraperIsArchIndependent(t *testing.T) {
	skipIfNoDebPackages(t)

	d := openDeb(t, findPackage(t, "opentelemetry-jmx-scraper_", ".deb"))

	assert.Equal(t, "all", d.Control.Architecture.String(),
		"the JMX scraper payload is architecture independent")
}

func TestDebJMXScraperShipsExpectedFiles(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-jmx-scraper_", ".deb")
	files := debDataFiles(t, pkg)

	for _, want := range jmxScraperFiles {
		assert.True(t, pathsContain(files, want), "the DEB should ship %s", want)
	}
}

// An upgrade must not discard the endpoint and credentials the operator set.
func TestDebJMXScraperMarksConfigFiles(t *testing.T) {
	skipIfNoDebPackages(t)

	pkg := findPackage(t, "opentelemetry-jmx-scraper_", ".deb")
	conffiles := debConffiles(t, pkg)

	for _, want := range jmxScraperConfigFiles {
		assert.Contains(t, conffiles, want,
			"%s is edited by the operator and must be registered as a conffile", want)
	}
}

// --------------------------------------------------------------------------
// RPM
// --------------------------------------------------------------------------

// The RPM spelling differs from the DEB one: java-headless is the virtual the
// EL and Fedora JDK packages provide, and alternations do not exist here.
func TestRpmJMXScraperSuggestsJavaRuntime(t *testing.T) {
	skipIfNoRpmPackages(t)

	p := openRpm(t, findRpmByName(t, "opentelemetry-jmx-scraper"))

	assert.True(t, rpmDepsContain(p.Suggests(), "java-headless"),
		"the JMX scraper should suggest java-headless, the virtual the RPM JDKs provide")
}

func TestRpmJMXScraperIsIndependent(t *testing.T) {
	skipIfNoRpmPackages(t)

	p := openRpm(t, findRpmByName(t, "opentelemetry-jmx-scraper"))

	// Every RPM provides its own name; anything beyond that would be a
	// virtual name, which this package deliberately does not declare.
	var virtuals []string
	for _, d := range p.Provides() {
		if d.Name() != p.Name() {
			virtuals = append(virtuals, d.Name())
		}
	}
	assert.Empty(t, virtuals,
		"the JMX scraper implements no injector-mediated contract, so it provides no virtual name")
	assert.Empty(t, p.Requires(),
		"the JMX scraper must not require the injector, the metapackage, or a Java runtime")
}

func TestRpmJMXScraperIsNoarch(t *testing.T) {
	skipIfNoRpmPackages(t)

	p := openRpm(t, findRpmByName(t, "opentelemetry-jmx-scraper"))

	assert.Equal(t, "noarch", p.Architecture(),
		"the JMX scraper payload is architecture independent")
}

func TestRpmJMXScraperShipsExpectedFiles(t *testing.T) {
	skipIfNoRpmPackages(t)

	p := openRpm(t, findRpmByName(t, "opentelemetry-jmx-scraper"))
	files := rpmFileNames(p)

	for _, want := range jmxScraperFiles {
		assert.True(t, pathsContain(files, want), "the RPM should ship %s", want)
	}
}

// %config(noreplace) leaves the operator's file in place and writes a .rpmnew
// alongside it. Without the noreplace flag an upgrade silently replaces it.
func TestRpmJMXScraperMarksConfigFiles(t *testing.T) {
	skipIfNoRpmPackages(t)

	p := openRpm(t, findRpmByName(t, "opentelemetry-jmx-scraper"))

	for _, want := range jmxScraperConfigFiles {
		flags, found := rpmFileFlags(p, want)
		if !assert.True(t, found, "the RPM should ship %s", want) {
			continue
		}
		assert.NotZero(t, flags&rpmFileConfig, "%s should be marked as a config file", want)
		assert.NotZero(t, flags&rpmFileNoReplace,
			"%s is edited by the operator, so an upgrade must not replace it", want)
	}
}

// TestJMXScraperConfigFileModes pins the permission split the DynamicUser unit
// depends on. The service reads config.properties as a transient UID, so that
// file has to stay world-readable; systemd reads jmx-scraper.env as root before
// dropping privileges, so that one carries the secrets and must not be
// world-readable. Flipping either mode silently breaks the design.
func TestJMXScraperConfigFileModes(t *testing.T) {
	t.Run("deb", func(t *testing.T) {
		skipIfNoDebPackages(t)

		modes := debFileModes(t, findPackage(t, "opentelemetry-jmx-scraper_", ".deb"))
		assertJMXScraperConfigModes(t, func(path string) (os.FileMode, bool) {
			mode, found := modes[path]
			return mode, found
		})
	})

	t.Run("rpm", func(t *testing.T) {
		skipIfNoRpmPackages(t)

		p := openRpm(t, findRpmByName(t, "opentelemetry-jmx-scraper"))
		assertJMXScraperConfigModes(t, func(path string) (os.FileMode, bool) {
			for _, f := range p.Files() {
				if f.Name() == path {
					return f.Mode(), true
				}
			}
			return 0, false
		})
	})
}

func assertJMXScraperConfigModes(t *testing.T, modeOf func(string) (os.FileMode, bool)) {
	t.Helper()

	const (
		properties = "/etc/opentelemetry/jmx-scraper/config.properties"
		env        = "/etc/opentelemetry/jmx-scraper/jmx-scraper.env"
	)

	if mode, found := modeOf(properties); assert.True(t, found, "the package should ship %s", properties) {
		assert.NotZero(t, mode.Perm()&0o004,
			"%s must stay world-readable, because the DynamicUser service reads it as a transient UID, got %v",
			properties, mode.Perm())
	}

	if mode, found := modeOf(env); assert.True(t, found, "the package should ship %s", env) {
		assert.Zero(t, mode.Perm()&0o007,
			"%s holds the credentials and must not be world-readable, got %v", env, mode.Perm())
	}
}

// --------------------------------------------------------------------------
// Helpers
// --------------------------------------------------------------------------

// debFileModes maps each packaged path to the mode recorded in the data
// archive.
func debFileModes(t *testing.T, path string) map[string]os.FileMode {
	t.Helper()

	d, closer, err := deb.LoadFile(path)
	require.NoError(t, err)
	defer closer()

	modes := make(map[string]os.FileMode)
	for {
		hdr, err := d.Data.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		modes[strings.TrimPrefix(hdr.Name, ".")] = hdr.FileInfo().Mode()
	}
	return modes
}

// rpmFileFlags returns the RPM file flags for one path in the package.
func rpmFileFlags(p *rpm.Package, name string) (int64, bool) {
	for _, f := range p.Files() {
		if f.Name() == name {
			return f.Flags(), true
		}
	}
	return 0, false
}

// debConffiles returns the contents of the conffiles member of a .deb's
// control archive, which is how dpkg learns which files it must preserve.
//
// The deb library parses the control paragraph but does not expose the other
// control members, so this reads the archive entry directly.
func debConffiles(t *testing.T, path string) string {
	t.Helper()

	d, closer, err := deb.LoadFile(path)
	require.NoError(t, err)
	defer closer()

	entry, ok := d.ArContent["control."+d.ControlExt]
	require.True(t, ok, "no control archive in %s", path)

	// LoadFile has already read this entry to parse the control paragraph,
	// so rewind before reading it again.
	_, err = entry.Data.Seek(0, io.SeekStart)
	require.NoError(t, err)

	tr, closeTar, err := entry.Tarfile()
	require.NoError(t, err)
	defer closeTar.Close()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if strings.TrimPrefix(hdr.Name, "./") == "conffiles" {
			body, err := io.ReadAll(tr)
			require.NoError(t, err)
			return string(body)
		}
	}
	t.Fatalf("no conffiles member in the control archive of %s", path)
	return ""
}
