// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package lifecycle

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	jmxScraperPackage = "opentelemetry-jmx-scraper"
	jmxScraperUnit    = "opentelemetry-jmx-scraper.service"

	// systemctlLog is where the systemctl stub records its invocations.
	systemctlLog = "/tmp/systemctl.log"

	// systemctlStub stands in for systemctl, which the lifecycle images do
	// not run: it logs every invocation and answers is-active and is-enabled
	// from marker files, so a scenario can pick the unit's state. It goes in
	// /usr/bin because rpm runs scriptlets with a PATH that omits /usr/local.
	systemctlStub = `#!/bin/sh
echo "$*" >> ` + systemctlLog + `
case "$1" in
    is-active) [ -e /tmp/systemctl-active ] ;;
    is-enabled) [ -e /tmp/systemctl-enabled ] ;;
esac
`
)

// jmxScraperScenarios covers the JMX scraper's pre-uninstall scriptlet: on
// removal it must stop and disable the service whether the operator started
// it, enabled it, or both, and leave it alone when it is neither.
var jmxScraperScenarios = []scenario{
	jmxScraperRemoveScenario("jmx-scraper-remove-stops-started-service", true, false, true),
	jmxScraperRemoveScenario("jmx-scraper-remove-disables-enabled-service", false, true, true),
	jmxScraperRemoveScenario("jmx-scraper-remove-leaves-inactive-service", false, false, false),
}

func jmxScraperRemoveScenario(name string, active, enabled, wantDisable bool) scenario {
	return scenario{
		name: name,
		run: func(t *testing.T, ctx context.Context, h *harness) {
			h.exec(t, ctx, "mkdir", "-p", "/run/systemd/system")
			h.exec(t, ctx, "sh", "-c",
				`printf '%s' "$1" > /usr/bin/systemctl && chmod 0755 /usr/bin/systemctl`, "sh", systemctlStub)
			if active {
				h.exec(t, ctx, "touch", "/tmp/systemctl-active")
			}
			if enabled {
				h.exec(t, ctx, "touch", "/tmp/systemctl-enabled")
			}

			h.install(t, ctx, jmxScraperPackage)
			h.remove(t, ctx, jmxScraperPackage)

			require.False(t, h.installed(t, ctx, jmxScraperPackage))
			calls := h.fileContent(t, ctx, systemctlLog)
			require.Contains(t, calls, jmxScraperUnit,
				"the pre-uninstall scriptlet should have reached systemctl")
			if wantDisable {
				require.Contains(t, calls, "disable --now "+jmxScraperUnit,
					"removal should stop and disable the service")
			} else {
				require.NotContains(t, calls, "disable --now "+jmxScraperUnit,
					"removal should leave a service that is neither active nor enabled alone")
			}
		},
	}
}
