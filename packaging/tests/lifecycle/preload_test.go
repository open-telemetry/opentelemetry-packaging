// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// preloadScenarios covers test category 3 of the integration test plan: the
// injector's postinstall/preremove scripts managing /etc/ld.so.preload.
var preloadScenarios = []scenario{
	{
		name: "preload-created-on-install",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")

			require.True(t, h.fileExists(t, ctx, preloadPath), "%s should exist after install", preloadPath)
			content := h.fileContent(t, ctx, preloadPath)
			require.Equal(t, 1, strings.Count(content, injectorLib),
				"expected exactly one injector entry in %s, got:\n%s", preloadPath, content)
		},
	},
	{
		name: "preload-idempotent-reinstall",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			h.reinstall(t, ctx, "opentelemetry-injector")

			require.Equal(t, 1, h.preloadEntryCount(t, ctx),
				"reinstall must not duplicate or drop the preload entry")
		},
	},
	{
		name: "preload-removed-on-remove",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.fileExists(t, ctx, preloadPath),
				"%s should be deleted when the injector entry was the only one", preloadPath)
		},
	},
	{
		name: "preload-remove-tolerates-missing-file",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			// A local administrator may have cleaned up ld.so.preload by hand;
			// the preuninstall script must not fail the removal over it (a
			// failing preuninstall blocks the package removal altogether).
			h.exec(t, ctx, "rm", "-f", preloadPath)

			h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.installed(t, ctx, "opentelemetry-injector"),
				"removal must succeed when %s is already gone", preloadPath)
		},
	},
	staleTmpfileScenario("preload-remove-clears-stale-tmpfiles",
		func(t *testing.T, ctx context.Context, h *harness) {
			h.remove(t, ctx, "opentelemetry-injector")
		}),
	staleTmpfileScenario("preload-upgrade-clears-stale-tmpfiles",
		func(t *testing.T, ctx context.Context, h *harness) {
			h.enableNextRepo(t, ctx)
			h.upgradeInjector(t, ctx)
			require.Equal(t, 1, h.preloadEntryCount(t, ctx),
				"upgrade must keep the preload entry")
		}),
	{
		name: "preload-whitespace-only-file-deleted-on-remove",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			// Leave whitespace lines around the injector entry; once the entry
			// is removed, a file holding only whitespace must be deleted, not
			// left behind.
			h.exec(t, ctx, "sh", "-c", "printf '\\n \\n' >> "+preloadPath)

			h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.fileExists(t, ctx, preloadPath),
				"%s holding only whitespace lines should be deleted on remove", preloadPath)
		},
	},
	{
		// dpkg runs prerm with failed-upgrade or deconfigure on non-removal
		// transitions; the guard must keep the preload entry for those, not
		// just for "upgrade". Invoke the installed prerm directly with each
		// action (they are dpkg-only, hence deb-only) and assert the entry
		// survives. Fails against a guard that only matches "upgrade".
		name:    "preload-survives-non-removal-prerm-actions",
		formats: []string{"deb"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			prerm := "/var/lib/dpkg/info/opentelemetry-injector.prerm"
			for _, action := range []string{"upgrade", "failed-upgrade", "deconfigure"} {
				h.exec(t, ctx, prerm, action, "0.0.0-dev")
				require.Equal(t, 1, h.preloadEntryCount(t, ctx),
					"preload entry must survive prerm %q", action)
			}
		},
	},
	{
		name: "preload-preserves-foreign-entries",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			// Pre-seed a foreign preload entry, as another product might have.
			// The library does not exist; ld.so warnings are harmless here.
			h.exec(t, ctx, "sh", "-c", "echo /opt/acme/libfoo.so > "+preloadPath)

			h.install(t, ctx, "opentelemetry-injector")
			content := h.fileContent(t, ctx, preloadPath)
			require.Contains(t, content, "/opt/acme/libfoo.so", "foreign entry should survive install")
			require.Equal(t, 1, strings.Count(content, injectorLib))

			h.remove(t, ctx, "opentelemetry-injector")
			require.True(t, h.fileExists(t, ctx, preloadPath),
				"%s should be kept while it has foreign entries", preloadPath)
			content = h.fileContent(t, ctx, preloadPath)
			require.Contains(t, content, "/opt/acme/libfoo.so", "foreign entry should survive remove")
			require.NotContains(t, content, injectorLib, "injector entry should be gone after remove")
		},
	},
	// An immutable ld.so.preload (chattr +i) makes the rewrite fail. The
	// removal must still go through, and the script must warn that the entry
	// is left behind instead of reporting success.
	immutablePreloadScenario("preload-remove-warns-when-replace-fails",
		"/opt/acme/libfoo.so", "WARNING: could not replace "+preloadPath),
	immutablePreloadScenario("preload-remove-warns-when-delete-fails",
		"", "WARNING: could not remove "+preloadPath),
}

// immutablePreloadScenario installs the injector, optionally after seeding a
// foreign preload entry, marks ld.so.preload immutable, and removes the
// injector. It asserts the removal succeeds with wantWarning, without the
// success line, and with the file unchanged.
func immutablePreloadScenario(name, foreignEntry, wantWarning string) scenario {
	return scenario{
		name:   name,
		capAdd: []string{"LINUX_IMMUTABLE"},
		run: func(t *testing.T, ctx context.Context, h *harness) {
			if foreignEntry != "" {
				h.exec(t, ctx, "sh", "-c", "echo "+foreignEntry+" > "+preloadPath)
			}
			h.install(t, ctx, "opentelemetry-injector")
			before := h.fileContent(t, ctx, preloadPath)
			h.exec(t, ctx, "chattr", "+i", preloadPath)

			out := h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.installed(t, ctx, "opentelemetry-injector"),
				"removal must succeed when %s is immutable", preloadPath)
			require.Contains(t, out, wantWarning)
			require.NotContains(t, out, "OpenTelemetry Injector removed from ld.so.preload.",
				"must not report success while the entry remains")
			require.Equal(t, before, h.fileContent(t, ctx, preloadPath),
				"%s should be left unchanged", preloadPath)
		},
	}
}

// staleTmpfileScenario installs the injector, plants the tmpfiles a SIGKILLed
// preuninstall run would leave behind, runs act, and asserts they are gone.
func staleTmpfileScenario(name string, act func(t *testing.T, ctx context.Context, h *harness)) scenario {
	return scenario{
		name: name,
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")
			stale := []string{preloadPath + ".tmp.48", preloadPath + ".tmp.12345"}
			for _, path := range stale {
				h.exec(t, ctx, "sh", "-c", "echo partial > "+path)
			}

			act(t, ctx, h)

			for _, path := range stale {
				require.False(t, h.fileExists(t, ctx, path),
					"%s left by an earlier killed run should be removed", path)
			}
		},
	}
}
