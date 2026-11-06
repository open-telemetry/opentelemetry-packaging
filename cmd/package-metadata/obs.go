// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/builder"
	"github.com/open-telemetry/opentelemetry-packaging/packaging/changes"
)

// obsInputs are the paths besides changes.PackageInputs that every OBS source
// set is generated from.
//
// The planner, the publisher and the workflow are deliberately not among them:
// they decide what is uploaded, not what it contains, so a change to them
// republishes nothing. The first upload after they change is a manual dispatch.
var obsInputs = []string{"cmd/build-spec/deb"}

func obsShared() []string {
	return append(append([]string(nil), changes.PackageInputs...), obsInputs...)
}

// obsPackage is one OBS package a run uploads.
type obsPackage struct {
	Component string `json:"component"`
	Package   string `json:"package"`
	// Version is the upstream version the package ships, which is also its
	// version on OBS (builder.UpstreamVersion).
	Version string `json:"version"`
	// SourceDateEpoch is the commit time of the latest change to the
	// component's inputs. Generating its sources with it makes an unchanged
	// component byte-identical to its last upload, which OBS then neither
	// commits nor rebuilds.
	SourceDateEpoch string `json:"source_date_epoch"`
}

// obsPlan prints the OBS packages to upload as a GitHub Actions output. An
// explicit selection ("all" or a comma-separated list of components) wins;
// otherwise base selects the components changed since that ref, and no base
// selects every component.
func obsPlan(out io.Writer, root, base, selection string) error {
	components, err := obsComponents(base, selection)
	if err != nil {
		return err
	}
	packages := make([]obsPackage, 0, len(components))
	for _, comp := range components {
		version, err := builder.UpstreamVersion(builder.Config{PackagingDir: root}, comp.Name)
		if err != nil {
			return fmt.Errorf("version of %s: %w", comp.Name, err)
		}
		epoch, err := lastChange(changes.Inputs(comp.Name, obsShared()))
		if err != nil {
			return fmt.Errorf("source date of %s: %w", comp.Name, err)
		}
		packages = append(packages, obsPackage{
			Component:       comp.Name,
			Package:         comp.PackageName,
			Version:         version,
			SourceDateEpoch: epoch,
		})
	}

	data, err := json.Marshal(packages)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "packages=%s\n", data)
	return err
}

func obsComponents(base, selection string) ([]builder.Component, error) {
	selection = strings.TrimSpace(selection)
	if selection == "" && base == "" {
		selection = "all"
	}
	if selection == "all" {
		return builder.AllComponents, nil
	}

	var names []string
	if selection != "" {
		for name := range strings.SplitSeq(selection, ",") {
			names = append(names, strings.TrimSpace(name))
		}
	} else {
		paths, err := changedPaths(base)
		if err != nil {
			return nil, err
		}
		all := make([]string, 0, len(builder.AllComponents))
		for _, comp := range builder.AllComponents {
			all = append(all, comp.Name)
		}
		names = changes.Select(all, obsShared(), paths)
	}

	components := make([]builder.Component, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		comp, ok := builder.ComponentByName(name)
		if !ok {
			return nil, fmt.Errorf("unknown component %q", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("component %q selected twice", name)
		}
		seen[name] = true
		components = append(components, comp)
	}
	return components, nil
}

// lastChange returns the commit time of the latest commit touching any of
// paths, in seconds since the Unix epoch.
func lastChange(paths []string) (string, error) {
	data, err := git(append([]string{"log", "-1", "--format=%ct", "--"}, paths...)...)
	if err != nil {
		return "", err
	}
	epoch := strings.TrimSpace(string(data))
	if epoch == "" {
		return "", fmt.Errorf("no commit touches %s; is the history shallow?", strings.Join(paths, ", "))
	}
	return epoch, nil
}
