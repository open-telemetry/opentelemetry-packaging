// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package vendor contains end-to-end tests for the vendor replacement
// mechanism (test category 6 of the integration test plan): a vendor package
// that Provides the same virtual name as the upstream Java
// auto-instrumentation package, and Conflicts with and Replaces its concrete
// name, can be installed alongside the metapackage, swapped in on an existing
// system, and reverted — with the metapackage staying installed throughout.
//
// The mock vendor package (acme-java-autoinstrumentation) is built by
// ./mkvendor into a separate local repository; see the Makefile targets
// local-apt-vendor-repo and local-rpm-vendor-repo. The repositories are kept
// separate so the E2E suites never see two providers of the same virtual
// package.
package vendor

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

const (
	upstreamPkg = "opentelemetry-java-autoinstrumentation"
	vendorPkg   = "acme-java-autoinstrumentation"
	agentPath   = "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar"
	dropInPath  = "/etc/opentelemetry/injector/conf.d/java.conf"

	// vendorMarker appears in the mock vendor's agent file and drop-in; keep
	// in sync with ./mkvendor.
	vendorMarker = "ACME"
	// upstreamDropInMarker appears in the upstream drop-in
	// (packaging/common/java/injector.conf).
	upstreamDropInMarker = "Installed by opentelemetry-java-autoinstrumentation package"
)

// target is one (package format, base image) combination in the test matrix.
type target struct {
	format    string // "deb" or "rpm" — selects Dockerfile.<format>
	baseImage string // container base image
}

// matrix lists the base images each package format is exercised against.
var matrix = []target{
	{format: "deb", baseImage: "debian:12"},
	{format: "rpm", baseImage: "fedora:41"},
}

type scenario struct {
	name string
	run  func(t *testing.T, ctx context.Context, h *harness)
}

var scenarios = []scenario{
	{
		name: "fresh-install-with-vendor",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			// Name the vendor package explicitly: with two providers of the
			// Java virtual, the metapackage's Recommends alone is ambiguous.
			h.install(t, ctx, "opentelemetry", vendorPkg)

			require.True(t, h.installed(t, ctx, "opentelemetry"))
			require.True(t, h.installed(t, ctx, "opentelemetry-injector"))
			require.True(t, h.installed(t, ctx, vendorPkg))
			require.False(t, h.installed(t, ctx, upstreamPkg),
				"the vendor package conflicts with the upstream one")
			require.Contains(t, h.fileContent(t, ctx, agentPath), vendorMarker)
			require.Contains(t, h.fileContent(t, ctx, dropInPath), vendorMarker)
		},
	},
	{
		name: "swap-to-vendor",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.installWithUpstreamJava(t, ctx, "opentelemetry", "opentelemetry-injector", upstreamPkg)
			require.NotContains(t, h.fileContent(t, ctx, agentPath), vendorMarker,
				"sanity: the upstream agent is in place before the swap")

			h.install(t, ctx, vendorPkg)

			require.True(t, h.installed(t, ctx, vendorPkg))
			require.False(t, h.installed(t, ctx, upstreamPkg),
				"the upstream package should be replaced in the same transaction")
			require.True(t, h.installed(t, ctx, "opentelemetry"),
				"the metapackage must survive the swap: the virtual is still provided")
			require.True(t, h.installed(t, ctx, "opentelemetry-injector"))
			require.Contains(t, h.fileContent(t, ctx, agentPath), vendorMarker)
			require.Contains(t, h.fileContent(t, ctx, dropInPath), vendorMarker)
			h.requireCleanDropInDir(t, ctx)
			if h.format == "deb" {
				owner := h.exec(t, ctx, "dpkg", "-S", dropInPath)
				require.Contains(t, owner, vendorPkg, "the vendor package should own the drop-in")
			}
		},
	},
	{
		// The image has both the upstream and the vendor repositories
		// enabled, so the Java virtual has two providers. This scenario
		// validates what dependency resolution does when the user installs
		// the metapackage without naming a provider: the install must
		// succeed, the hard dependency chain must be complete, and the two
		// conflicting providers must never end up installed together.
		name: "metapackage-with-both-providers",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry")

			require.True(t, h.installed(t, ctx, "opentelemetry"))
			require.True(t, h.installed(t, ctx, "opentelemetry-injector"))
			upstream := h.installed(t, ctx, upstreamPkg)
			acme := h.installed(t, ctx, vendorPkg)
			require.False(t, upstream && acme,
				"the conflicting Java providers must never be installed together")
			// Observed on both debian:12 (apt) and fedora:41 (dnf5): the
			// resolvers do pick a provider rather than skip the Recommends —
			// both happen to pick acme, which sorts first.
			require.True(t, upstream || acme,
				"the resolver should install one of the two Java providers")
			t.Logf("Java provider resolution with two candidates: upstream=%v, acme=%v", upstream, acme)
			// The single-provider virtuals must resolve normally regardless
			// of the Java ambiguity.
			for _, lang := range []string{"nodejs", "dotnet", "python"} {
				require.True(t, h.installed(t, ctx, "opentelemetry-"+lang+"-autoinstrumentation"),
					"%s has a single provider and should be installed via Recommends", lang)
			}
		},
	},
	{
		name: "revert-to-upstream",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.installWithUpstreamJava(t, ctx, "opentelemetry", "opentelemetry-injector", upstreamPkg)
			h.install(t, ctx, vendorPkg)
			require.Contains(t, h.fileContent(t, ctx, agentPath), vendorMarker,
				"sanity: the vendor agent is in place before the revert")

			h.revertToUpstream(t, ctx)

			require.True(t, h.installed(t, ctx, upstreamPkg))
			require.False(t, h.installed(t, ctx, vendorPkg))
			require.True(t, h.installed(t, ctx, "opentelemetry"),
				"the metapackage must survive the revert")
			require.NotContains(t, h.fileContent(t, ctx, agentPath), vendorMarker)
			require.Contains(t, h.fileContent(t, ctx, dropInPath), upstreamDropInMarker)
			h.requireCleanDropInDir(t, ctx)
		},
	},
}

func TestVendorReplacement(t *testing.T) {
	ctx := context.Background()

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

func imageSlug(image string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(image)
}

type harness struct {
	format    string
	container testcontainers.Container
}

// buildImage builds the vendor image for a target once and returns its image
// name. The throwaway builder container is cleaned up with the subtest; the
// image itself is kept for the scenario containers.
func buildImage(t *testing.T, ctx context.Context, tg target) string {
	t.Helper()
	arch := testutil.TargetArch()
	buildArgs := map[string]*string{
		"BASE_IMAGE": &tg.baseImage,
		"ARCH":       &arch,
	}
	builder := testutil.StartContainer(t, ctx,
		fmt.Sprintf("packaging/tests/vendor/Dockerfile.%s", tg.format), buildArgs)
	return testutil.ImageOf(t, builder)
}

func (h *harness) exec(t *testing.T, ctx context.Context, cmd ...string) string {
	t.Helper()
	return testutil.ExecSucceeds(t, ctx, h.container, cmd...)
}

func (h *harness) install(t *testing.T, ctx context.Context, pkgs ...string) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, append([]string{"apt-get", "-y", "install"}, pkgs...)...)
	} else {
		h.exec(t, ctx, append([]string{"dnf", "-y", "install"}, pkgs...)...)
	}
}

// installWithUpstreamJava installs packages including the upstream Java
// package, pinning the upstream provider deterministically.
//
// On RPM this needs obsoletes processing disabled: with the vendor repository
// enabled, the vendor package's Obsoletes on the upstream name makes dnf
// redirect a plain `dnf install opentelemetry-java-autoinstrumentation` to
// the vendor package.
func (h *harness) installWithUpstreamJava(t *testing.T, ctx context.Context, pkgs ...string) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, append([]string{"apt-get", "-y", "install"}, pkgs...)...)
	} else {
		h.exec(t, ctx, append([]string{"dnf", "-y", "--setopt=obsoletes=0", "install"}, pkgs...)...)
	}
}

// revertToUpstream replaces the vendor package with the upstream one.
//
// On DEB, installing the upstream package makes apt remove the conflicting
// vendor package in the same transaction. On RPM, the vendor package
// Obsoletes the upstream name, so a plain `dnf install` would be redirected
// straight back to the vendor package; `dnf swap` with obsoletes processing
// disabled performs the remove+install in one transaction instead.
func (h *harness) revertToUpstream(t *testing.T, ctx context.Context) {
	t.Helper()
	if h.format == "deb" {
		h.exec(t, ctx, "apt-get", "-y", "install", upstreamPkg)
	} else {
		h.exec(t, ctx, "dnf", "-y", "--setopt=obsoletes=0", "swap", vendorPkg, upstreamPkg)
	}
}

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

func (h *harness) fileContent(t *testing.T, ctx context.Context, path string) string {
	t.Helper()
	return testutil.FileContentInContainer(t, ctx, h.container, path)
}

// requireCleanDropInDir asserts the conf.d directory contains exactly one
// java.conf and no package-manager residue (.dpkg-*, .rpmnew, .rpmsave).
func (h *harness) requireCleanDropInDir(t *testing.T, ctx context.Context) {
	t.Helper()
	listing := h.exec(t, ctx, "ls", "/etc/opentelemetry/injector/conf.d")
	names := strings.Fields(listing)
	javaCount := 0
	for _, name := range names {
		require.NotContains(t, name, ".dpkg-", "no dpkg conffile residue expected: %s", listing)
		require.NotContains(t, name, ".rpmnew", "no rpm residue expected: %s", listing)
		require.NotContains(t, name, ".rpmsave", "no rpm residue expected: %s", listing)
		if name == "java.conf" {
			javaCount++
		}
	}
	require.Equal(t, 1, javaCount, "expected exactly one java.conf, got: %s", listing)
}
