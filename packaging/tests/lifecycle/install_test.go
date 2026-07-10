// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// installScenarios covers test category 5 of the integration test plan:
// install and remove combinations, validating the metapackage dependency
// model (Depends on the injector virtual, Recommends the language virtuals,
// language packages only Suggest the injector).
var installScenarios = []scenario{
	{
		name: "install-metapackage-pulls-recommends",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry")

			require.True(t, h.installed(t, ctx, "opentelemetry"))
			require.True(t, h.installed(t, ctx, "opentelemetry-injector"),
				"the metapackage hard-depends on the injector virtual")
			for _, lang := range languages {
				require.True(t, h.installed(t, ctx, "opentelemetry-"+lang+"-autoinstrumentation"),
					"%s should be installed via Recommends (weak deps are on by default)", lang)
			}
			require.Equal(t, 1, h.preloadEntryCount(t, ctx), "the injector should be configured")
		},
	},
	{
		name: "install-injector-alone",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")

			require.True(t, h.installed(t, ctx, "opentelemetry-injector"))
			require.False(t, h.installed(t, ctx, "opentelemetry"), "the metapackage should not be pulled in")
			for _, lang := range languages {
				require.False(t, h.installed(t, ctx, "opentelemetry-"+lang+"-autoinstrumentation"),
					"%s should not be pulled in by the injector", lang)
			}
		},
	},
	{
		name: "install-injector-plus-java",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector", "opentelemetry-java-autoinstrumentation")

			require.True(t, h.installed(t, ctx, "opentelemetry-injector"))
			require.True(t, h.installed(t, ctx, "opentelemetry-java-autoinstrumentation"))
			for _, lang := range []string{"nodejs", "dotnet", "python"} {
				require.False(t, h.installed(t, ctx, "opentelemetry-"+lang+"-autoinstrumentation"))
			}
			require.True(t, h.fileExists(t, ctx, injectorConfDir+"/conf.d/java.conf"),
				"the Java drop-in should be installed")
		},
	},
	{
		name: "install-language-without-injector",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-java-autoinstrumentation")

			require.True(t, h.installed(t, ctx, "opentelemetry-java-autoinstrumentation"))
			require.False(t, h.installed(t, ctx, "opentelemetry-injector"),
				"the language package only Suggests the injector; it must not be pulled in")
			require.False(t, h.fileExists(t, ctx, preloadPath), "no injector, no preload file")
		},
	},
	{
		name: "remove-deletes-payload-files",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector", "opentelemetry-java-autoinstrumentation")
			require.True(t, h.fileExists(t, ctx, injectorLib))
			require.True(t, h.fileExists(t, ctx, javaAgentJar))

			h.remove(t, ctx, "opentelemetry-injector", "opentelemetry-java-autoinstrumentation")

			require.False(t, h.fileExists(t, ctx, injectorLib),
				"the injector library should be gone after remove")
			require.False(t, h.fileExists(t, ctx, javaAgentJar),
				"the Java agent JAR should be gone after remove")
		},
	},
	{
		name: "remove-language-keeps-injector",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry")
			h.remove(t, ctx, "opentelemetry-java-autoinstrumentation")

			require.False(t, h.installed(t, ctx, "opentelemetry-java-autoinstrumentation"))
			require.True(t, h.installed(t, ctx, "opentelemetry-injector"),
				"removing a language package must not remove the injector")
			require.True(t, h.installed(t, ctx, "opentelemetry"),
				"a violated Recommends must not force the metapackage out")
			require.Equal(t, 1, h.preloadEntryCount(t, ctx))
		},
	},
	{
		name: "remove-injector-removes-metapackage",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry")
			h.remove(t, ctx, "opentelemetry-injector")

			require.False(t, h.installed(t, ctx, "opentelemetry-injector"))
			require.False(t, h.installed(t, ctx, "opentelemetry"),
				"the metapackage hard-depends on the injector and must be removed with it")
			require.False(t, h.fileExists(t, ctx, preloadPath))
			if h.format == "deb" {
				// apt removes only the dependency chain in the transaction; the
				// language packages stay behind (as auto-installed).
				for _, lang := range languages {
					require.True(t, h.installed(t, ctx, "opentelemetry-"+lang+"-autoinstrumentation"),
						"%s should stay installed after removing the injector", lang)
				}
			}
			// On RPM, whether the language packages are swept in the same
			// transaction depends on dnf's clean_requirements_on_remove
			// handling of weak-dependency-installed packages; the assertion is
			// intentionally limited to the hard dependency chain above.
		},
	},
}
