// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package changes maps changed repository paths to the components they affect,
// so CI workflows rebuild and publish only the packages a commit changes.
package changes

import "strings"

// PackageInputs are the paths every package build reads, whatever it is
// published as. A change under any of them affects every component.
//
// cmd/otel-config-check and NOTICE are shipped inside the Python package: the
// cross-compiled validator at /usr/lib/opentelemetry/python/otel-config-check,
// and the attribution NOTICE. Without them a commit that only fixes the
// validator selects nothing, and the published package keeps serving the
// previous one with no signal.
var PackageInputs = []string{
	"packaging/builder", "packaging/common/scripts",
	"Makefile", "go.mod", "go.sum",
	"cmd/otel-config-check", "NOTICE",
}

// ComponentDir is the directory holding a component's own inputs: its version
// pin, configuration, and man page template.
func ComponentDir(component string) string {
	return "packaging/common/" + component
}

// Inputs returns the paths a component's build reads: shared, the paths every
// component of a target reads, and the component's own directory.
func Inputs(component string, shared []string) []string {
	return append(append([]string(nil), shared...), ComponentDir(component))
}

// Select returns the components among components that a change to paths
// affects, in their original order. A path under any of shared selects every
// component; a path under a component's own directory selects that one.
func Select(components, shared, paths []string) []string {
	for _, path := range paths {
		for _, prefix := range shared {
			if Under(path, prefix) {
				return append([]string(nil), components...)
			}
		}
	}
	selected := make([]string, 0)
	for _, component := range components {
		for _, path := range paths {
			if Under(path, ComponentDir(component)) {
				selected = append(selected, component)
				break
			}
		}
	}
	return selected
}

// Under reports whether path is prefix or lies inside it.
func Under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}
