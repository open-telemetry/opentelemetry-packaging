// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/zip"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// jmxRulesPrefix is where the scraper looks for the metric definitions it
// inherits from opentelemetry-java-instrumentation. Definitions carried by the
// scraper itself sit at the root of the JAR instead.
const jmxRulesPrefix = "jmx/rules/"

// JMXScraperTargetSystems downloads the pinned JMX scraper JAR and returns the
// target system names that version accepts, sorted.
//
// The accepted set is a property of the packaged JAR rather than of upstream's
// documentation, which tracks main and so lists definitions a pinned release
// does not have. Callers that document the values must read them from here.
func JMXScraperTargetSystems(cfg Config) ([]string, error) {
	staging, err := os.MkdirTemp("", "otel-jmx-targets-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	jarPath := filepath.Join(staging, "opentelemetry-jmx-scraper.jar")
	if err := downloadJMXScraper(cfg, jarPath); err != nil {
		return nil, fmt.Errorf("downloading JMX scraper: %w", err)
	}
	return targetSystemsInJAR(jarPath)
}

// targetSystemsInJAR lists the target systems a scraper JAR accepts.
//
// JmxScraperConfig resolves the name X by loading "X.yaml" or
// "jmx/rules/X.yaml" from the classpath, so the accepted set is the union of
// the YAML basenames at those two locations.
func targetSystemsInJAR(jarPath string) ([]string, error) {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", jarPath, err)
	}
	defer zr.Close()

	seen := make(map[string]struct{})
	for _, f := range zr.File {
		name, ok := targetSystemName(f.Name)
		if !ok {
			continue
		}
		seen[name] = struct{}{}
	}

	// An empty result means upstream moved the definitions, which would
	// otherwise silently erase the documented list.
	if len(seen) == 0 {
		return nil, fmt.Errorf("no target system definitions found in %s: "+
			"upstream may have moved them out of the JAR root and %s", jarPath, jmxRulesPrefix)
	}
	return slices.Sorted(maps.Keys(seen)), nil
}

// targetSystemName maps a JAR entry to the target system it defines, reporting
// whether the entry is one of those definitions at all.
func targetSystemName(entry string) (string, bool) {
	var base string
	switch {
	case strings.HasPrefix(entry, jmxRulesPrefix):
		base = strings.TrimPrefix(entry, jmxRulesPrefix)
	case !strings.Contains(entry, "/"):
		base = entry
	default:
		return "", false
	}

	if strings.Contains(base, "/") {
		return "", false
	}
	for _, ext := range []string{".yaml", ".yml"} {
		if name, ok := strings.CutSuffix(base, ext); ok {
			return name, true
		}
	}
	return "", false
}
