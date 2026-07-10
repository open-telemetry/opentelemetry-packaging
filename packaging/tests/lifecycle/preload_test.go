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
}
