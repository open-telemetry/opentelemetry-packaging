// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package lifecycle contains end-to-end tests for package lifecycle behavior:
// the injector's /etc/ld.so.preload management (postinstall/preremove
// scripts), config file handling across remove, purge, and upgrade, and
// install/remove dependency scenarios including the metapackage.
//
// Unlike the language E2E suites, no packages are installed at image build
// time: every scenario starts from a pristine container that only has the
// local repositories configured, and drives apt-get/dnf through Exec.
package lifecycle

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

const (
	preloadPath      = "/etc/ld.so.preload"
	injectorLib      = "/usr/lib/opentelemetry/injector/libotelinject.so"
	javaAgentJar     = "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar"
	injectorConfDir  = "/etc/opentelemetry/injector"
	injectorConfPath = injectorConfDir + "/injector.conf"
	envConfPath      = injectorConfDir + "/default_env.conf"

	// nextConfigMarker is the line the next-version injector build ships in
	// default_env.conf. Keep in sync with NEXT_CONFIG_MARKER in the Makefile.
	nextConfigMarker = "OTEL_TEST_NEXT_CONFIG_MARKER=1"

	// userMarker simulates a local administrator edit to a config file.
	userMarker = "OTEL_USER_MARKER=1"
)

// languages lists the language auto-instrumentation package name stems.
var languages = []string{"java", "nodejs", "dotnet", "python"}

// target is one (package format, base image) combination in the test matrix.
type target struct {
	format    string // "deb" or "rpm" — selects Dockerfile.<format>
	baseImage string // container base image
}

// matrix lists the base images each package format is exercised against. Add a
// row to cover another base image; the assertions do not change.
var matrix = []target{
	{format: "deb", baseImage: "debian:12"},
	{format: "rpm", baseImage: "fedora:41"},
}

// scenario is a single lifecycle test case. Each scenario runs in its own
// pristine container because every scenario mutates global system state (the
// package database, /etc/ld.so.preload, /etc/opentelemetry).
type scenario struct {
	name    string
	formats []string // nil means both formats
	run     func(t *testing.T, ctx context.Context, h *harness)
}

func TestLifecycle(t *testing.T) {
	ctx := context.Background()

	scenarios := make([]scenario, 0, len(preloadScenarios)+len(configScenarios)+len(installScenarios))
	scenarios = append(scenarios, preloadScenarios...)
	scenarios = append(scenarios, configScenarios...)
	scenarios = append(scenarios, installScenarios...)

	byFormat := map[string][]target{}
	for _, tg := range matrix {
		byFormat[tg.format] = append(byFormat[tg.format], tg)
	}

	for _, format := range []string{"deb", "rpm"} {
		targets := byFormat[format]
		if len(targets) == 0 {
			continue
		}
		t.Run(format, func(t *testing.T) {
			for _, tg := range targets {
				t.Run(imageSlug(tg.baseImage), func(t *testing.T) {
					// Build the image once; the scenario containers start from
					// it. Building per scenario would trigger one concurrent
					// build of the same Dockerfile per subtest.
					image := buildImage(t, ctx, tg)
					for _, sc := range scenarios {
						if !sc.appliesTo(tg.format) {
							continue
						}
						t.Run(sc.name, func(t *testing.T) {
							t.Parallel()
							h := &harness{
								format:    tg.format,
								container: testutil.StartContainerFromImage(t, ctx, image),
							}
							sc.run(t, ctx, h)
						})
					}
				})
			}
		})
	}
}

func (sc scenario) appliesTo(format string) bool {
	if len(sc.formats) == 0 {
		return true
	}
	for _, f := range sc.formats {
		if f == format {
			return true
		}
	}
	return false
}

func imageSlug(image string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(image)
}

// harness wraps a pristine container together with format-specific package
// manager commands.
type harness struct {
	format    string
	container testcontainers.Container
}

// buildImage builds the lifecycle image for a target once and returns its
// image name. The throwaway builder container is cleaned up with the subtest;
// the image itself is kept for the scenario containers.
func buildImage(t *testing.T, ctx context.Context, tg target) string {
	t.Helper()
	arch := testutil.TargetArch()
	buildArgs := map[string]*string{
		"BASE_IMAGE": &tg.baseImage,
		"ARCH":       &arch,
	}
	builder := testutil.StartContainer(t, ctx,
		fmt.Sprintf("packaging/tests/lifecycle/Dockerfile.%s", tg.format), buildArgs)
	return testutil.ImageOf(t, builder)
}

// exec runs a command and requires it to succeed.
func (h *harness) exec(t *testing.T, ctx context.Context, cmd ...string) string {
	t.Helper()
	return testutil.ExecSucceeds(t, ctx, h.container, cmd...)
}

// install installs the given packages from the local repository.
func (h *harness) install(t *testing.T, ctx context.Context, pkgs ...string) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, append([]string{"apt-get", "-y", "install"}, pkgs...)...)
	} else {
		h.exec(t, ctx, append([]string{"dnf", "-y", "install"}, pkgs...)...)
	}
}

// remove removes the given packages (DEB: keeps conffiles).
func (h *harness) remove(t *testing.T, ctx context.Context, pkgs ...string) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, append([]string{"apt-get", "-y", "remove"}, pkgs...)...)
	} else {
		h.exec(t, ctx, append([]string{"dnf", "-y", "remove"}, pkgs...)...)
	}
}

// reinstall reinstalls an already-installed package at the same version.
func (h *harness) reinstall(t *testing.T, ctx context.Context, pkg string) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, "apt-get", "-y", "install", "--reinstall", pkg)
	} else {
		h.exec(t, ctx, "dnf", "-y", "reinstall", pkg)
	}
}

// enableNextRepo makes the staged next-version repository visible to the
// package manager. It is offline (file://), so this never touches the network.
func (h *harness) enableNextRepo(t *testing.T, ctx context.Context) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, "sh", "-c",
			"echo 'deb [trusted=yes] file:///local-repo-next stable main'"+
				" > /etc/apt/sources.list.d/opentelemetry-next.list && apt-get update")
	} else {
		h.exec(t, ctx, "sh", "-c",
			"printf '[opentelemetry-local-next]\\nname=OpenTelemetry Local Next\\n"+
				"baseurl=file:///local-repo-next/packages\\nenabled=1\\ngpgcheck=0\\n'"+
				" > /etc/yum.repos.d/opentelemetry-local-next.repo")
	}
}

// upgradeInjector upgrades the injector to the next-version repository's
// build. dpkgOpts are dpkg --force-conf* options applied on DEB (ignored on
// RPM), keeping conffile conflict handling deterministic in a non-interactive
// container.
func (h *harness) upgradeInjector(t *testing.T, ctx context.Context, dpkgOpts ...string) {
	t.Helper()
	if h.format == "deb" {
		cmd := []string{"apt-get", "-y"}
		for _, opt := range dpkgOpts {
			cmd = append(cmd, "-o", "Dpkg::Options::="+opt)
		}
		cmd = append(cmd, "install", "opentelemetry-injector")
		h.exec(t, ctx, cmd...)
	} else {
		h.exec(t, ctx, "dnf", "-y", "upgrade", "opentelemetry-injector")
	}
}

// installed reports whether a package is currently installed (DEB: fully
// installed, not just present with residual config).
func (h *harness) installed(t *testing.T, ctx context.Context, pkg string) bool {
	t.Helper()
	if h.format == "deb" {
		code, out := testutil.ExecInContainer(t, ctx, h.container,
			"dpkg-query", "-W", "-f=${db:Status-Abbrev}", pkg)
		return code == 0 && strings.HasPrefix(out, "ii")
	}
	code, _ := testutil.ExecInContainer(t, ctx, h.container, "rpm", "-q", pkg)
	return code == 0
}

// debStatus returns the dpkg status abbreviation (e.g. "ii", "rc") and whether
// dpkg knows the package at all.
func (h *harness) debStatus(t *testing.T, ctx context.Context, pkg string) (string, bool) {
	t.Helper()
	require.Equal(t, "deb", h.format, "debStatus is DEB-only")
	code, out := testutil.ExecInContainer(t, ctx, h.container,
		"dpkg-query", "-W", "-f=${db:Status-Abbrev}", pkg)
	return strings.TrimSpace(out), code == 0
}

// version returns the installed version of a package.
func (h *harness) version(t *testing.T, ctx context.Context, pkg string) string {
	t.Helper()
	if h.format == "deb" {
		return strings.TrimSpace(h.exec(t, ctx, "dpkg-query", "-W", "-f=${Version}", pkg))
	}
	return strings.TrimSpace(h.exec(t, ctx, "rpm", "-q", "--qf", "%{VERSION}", pkg))
}

// fileExists reports whether a path exists in the container (file or directory).
func (h *harness) fileExists(t *testing.T, ctx context.Context, path string) bool {
	t.Helper()
	code, _ := testutil.ExecInContainer(t, ctx, h.container, "test", "-e", path)
	return code == 0
}

// fileContent returns the contents of a file in the container.
func (h *harness) fileContent(t *testing.T, ctx context.Context, path string) string {
	t.Helper()
	return testutil.FileContentInContainer(t, ctx, h.container, path)
}

// appendLine appends a line to a file in the container.
func (h *harness) appendLine(t *testing.T, ctx context.Context, path, line string) {
	t.Helper()
	h.exec(t, ctx, "sh", "-c", fmt.Sprintf("echo '%s' >> %s", line, path))
}

// preloadEntryCount returns how many times the injector library appears in
// /etc/ld.so.preload (0 if the file does not exist).
func (h *harness) preloadEntryCount(t *testing.T, ctx context.Context) int {
	t.Helper()
	if !h.fileExists(t, ctx, preloadPath) {
		return 0
	}
	return strings.Count(h.fileContent(t, ctx, preloadPath), injectorLib)
}

// nextVersion returns the version the next-version repository serves, as
// exported by the Makefile. Tests fall back to weaker assertions when unset
// (bare `go test` after `make local-*-repo-next`).
func nextVersion() string {
	return os.Getenv("NEXT_VERSION")
}

// requireUpgraded asserts the injector was upgraded to the next version.
func (h *harness) requireUpgraded(t *testing.T, ctx context.Context, oldVersion string) {
	t.Helper()
	got := h.version(t, ctx, "opentelemetry-injector")
	require.NotEqual(t, oldVersion, got, "injector version should have changed on upgrade")
	if next := nextVersion(); next != "" {
		require.Equal(t, next, got, "injector should be at the next version")
	}
}
