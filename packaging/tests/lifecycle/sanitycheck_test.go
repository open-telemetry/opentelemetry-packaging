// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
)

// sanityCheckScenarios exercise the otel-instrumentation-check command shipped
// in the injector package: it must report a healthy install after a full
// installation and fail when the injector is present but no language agent is
// registered. It needs no OTLP receiver and no running application.
var sanityCheckScenarios = []scenario{
	{
		name: "sanity-check-passes-on-full-install",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry")

			code, output := testutil.ExecInContainer(t, ctx, h.container, sanityCheckBin)
			assert.Equal(t, 0, code, "the check must pass on a full install.\n\nOutput:\n%s", output)
			assert.Contains(t, output, "Result: PASS", output)
			assert.Contains(t, output, "injector active", output)
			assert.GreaterOrEqual(t, strings.Count(output, "agent registered"), 1,
				"at least one language agent should be reported as registered")
		},
	},
	{
		name: "sanity-check-fails-without-language-agents",
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.install(t, ctx, "opentelemetry-injector")

			code, output := testutil.ExecInContainer(t, ctx, h.container, sanityCheckBin)
			require.Equal(t, 1, code, "the check must fail when no language agent is registered.\n\nOutput:\n%s", output)
			assert.Contains(t, output, "no language agents registered", output)
		},
	},
}
