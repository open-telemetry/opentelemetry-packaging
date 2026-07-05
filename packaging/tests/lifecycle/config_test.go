// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// configScenarios covers test category 4 of the integration test plan: config
// file behavior across remove, purge, and upgrade. The injector's
// injector.conf and default_env.conf are config|noreplace files; conf.d
// drop-ins shipped by language packages are regular files.
//
// Upgrades use the next-version repository, whose injector build ships a
// default_env.conf that differs from the current one (see the Makefile) —
// dpkg and rpm skip conffile conflict handling entirely when pristine
// contents are identical between versions.
var configScenarios = []scenario{
	{
		name:    "config-remove-keeps-conffiles",
		formats: []string{"deb"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			h.remove(t, ctx, "opentelemetry-injector")

			status, known := h.debStatus(t, ctx, "opentelemetry-injector")
			require.True(t, known, "dpkg should still know the package after remove")
			require.Equal(t, "rc", status, "package should be in removed-conffiles-remain state")
			require.True(t, h.fileExists(t, ctx, injectorConfPath), "injector.conf should survive remove")
			require.True(t, h.fileExists(t, ctx, envConfPath), "default_env.conf should survive remove")
			require.False(t, h.fileExists(t, ctx, injectorLib), "the library should be gone after remove")
		},
	},
	{
		name:    "config-purge-removes-conffiles",
		formats: []string{"deb"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			h.remove(t, ctx, "opentelemetry-injector")
			h.exec(t, ctx, "apt-get", "-y", "purge", "opentelemetry-injector")

			_, known := h.debStatus(t, ctx, "opentelemetry-injector")
			require.False(t, known, "dpkg should no longer know the package after purge")
			require.False(t, h.fileExists(t, ctx, injectorConfDir),
				"%s should be gone after purge", injectorConfDir)
		},
	},
	{
		name:    "config-upgrade-modified-confold",
		formats: []string{"deb"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			oldVersion := h.version(t, ctx, "opentelemetry-injector")
			h.appendLine(t, ctx, envConfPath, userMarker)

			h.enableNextRepo(t, ctx)
			h.upgradeInjector(t, ctx, "--force-confdef", "--force-confold")

			h.requireUpgraded(t, ctx, oldVersion)
			content := h.fileContent(t, ctx, envConfPath)
			require.Contains(t, content, userMarker, "user modification should be preserved with confold")
			require.NotContains(t, content, nextConfigMarker, "new pristine config should not be applied with confold")
			require.Equal(t, 1, h.preloadEntryCount(t, ctx), "preload entry should survive the upgrade")
		},
	},
	{
		name:    "config-upgrade-modified-confnew",
		formats: []string{"deb"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			oldVersion := h.version(t, ctx, "opentelemetry-injector")
			h.appendLine(t, ctx, envConfPath, userMarker)

			h.enableNextRepo(t, ctx)
			h.upgradeInjector(t, ctx, "--force-confnew")

			h.requireUpgraded(t, ctx, oldVersion)
			content := h.fileContent(t, ctx, envConfPath)
			require.Contains(t, content, nextConfigMarker, "new pristine config should be applied with confnew")
			require.NotContains(t, content, userMarker, "user modification should be replaced with confnew")
		},
	},
	{
		name:    "config-remove-unmodified",
		formats: []string{"rpm"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.fileExists(t, ctx, injectorConfPath), "unmodified config should be removed")
			require.False(t, h.fileExists(t, ctx, envConfPath), "unmodified config should be removed")
			require.False(t, h.fileExists(t, ctx, envConfPath+".rpmsave"),
				"no .rpmsave should be created for an unmodified config")
		},
	},
	{
		name:    "config-remove-modified-rpmsave",
		formats: []string{"rpm"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			h.appendLine(t, ctx, envConfPath, userMarker)
			h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.fileExists(t, ctx, envConfPath), "the config itself is removed")
			savePath := envConfPath + ".rpmsave"
			require.True(t, h.fileExists(t, ctx, savePath), "modified config should be saved as .rpmsave")
			require.Contains(t, h.fileContent(t, ctx, savePath), userMarker)
		},
	},
	{
		name:    "config-upgrade-modified-rpmnew",
		formats: []string{"rpm"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			oldVersion := h.version(t, ctx, "opentelemetry-injector")
			h.appendLine(t, ctx, envConfPath, userMarker)

			h.enableNextRepo(t, ctx)
			h.upgradeInjector(t, ctx)

			h.requireUpgraded(t, ctx, oldVersion)
			content := h.fileContent(t, ctx, envConfPath)
			require.Contains(t, content, userMarker, "user-modified noreplace config should stay in place")
			require.NotContains(t, content, nextConfigMarker)
			newPath := envConfPath + ".rpmnew"
			require.True(t, h.fileExists(t, ctx, newPath), "new pristine config should land as .rpmnew")
			require.Contains(t, h.fileContent(t, ctx, newPath), nextConfigMarker)
			// Regression test: the old version's %preun runs after the new
			// version's %post on upgrade and must not strip the preload entry.
			require.Equal(t, 1, h.preloadEntryCount(t, ctx), "preload entry should survive the upgrade")
		},
	},
	{
		name: "config-upgrade-unmodified",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			oldVersion := h.version(t, ctx, "opentelemetry-injector")

			h.enableNextRepo(t, ctx)
			h.upgradeInjector(t, ctx)

			h.requireUpgraded(t, ctx, oldVersion)
			content := h.fileContent(t, ctx, envConfPath)
			require.Contains(t, content, nextConfigMarker,
				"an unmodified config should be replaced by the new pristine version")
			require.False(t, h.fileExists(t, ctx, envConfPath+".rpmnew"), "no .rpmnew for unmodified config")
			require.False(t, h.fileExists(t, ctx, envConfPath+".dpkg-dist"), "no .dpkg-dist for unmodified config")
			require.Equal(t, 1, h.preloadEntryCount(t, ctx), "preload entry should survive the upgrade")
		},
	},
	{
		name: "config-custom-dropin-survives",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			dropIn := injectorConfDir + "/conf.d/99-custom.conf"
			h.exec(t, ctx, "sh", "-c", "echo 'OTEL_SERVICE_NAME=custom' > "+dropIn)

			h.enableNextRepo(t, ctx)
			h.upgradeInjector(t, ctx)
			require.True(t, h.fileExists(t, ctx, dropIn), "custom drop-in should survive upgrade")
			require.Contains(t, h.fileContent(t, ctx, dropIn), "OTEL_SERVICE_NAME=custom")

			h.remove(t, ctx, "opentelemetry-injector")
			require.True(t, h.fileExists(t, ctx, dropIn),
				"custom drop-in is not owned by the package and should survive remove")
		},
	},
}
