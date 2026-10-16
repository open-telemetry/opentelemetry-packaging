// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/images"
)

var versionPattern = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)$`)

func readVersion(root, component string) (string, error) {
	pin, err := os.ReadFile(filepath.Join(root, "common", component, "release.txt"))
	if err != nil {
		return "", err
	}
	return parseVersion(string(pin), component)
}

func parseVersion(pin, component string) (string, error) {
	var version string
	for line := range strings.SplitSeq(pin, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if version != "" {
			return "", fmt.Errorf("multiple version pins for %s", component)
		}
		match := versionPattern.FindStringSubmatch(line)
		if match == nil {
			return "", fmt.Errorf("invalid upstream version %q", line)
		}
		version = match[1]
	}
	if version == "" {
		return "", fmt.Errorf("missing version pin for %s", component)
	}
	return version, nil
}

func run(out io.Writer, root, component, field string) error {
	cfg, err := images.Load(root)
	if err != nil {
		return err
	}
	switch field {
	case "components":
		var components []string
		for _, pkg := range cfg.Packages {
			components = append(components, pkg.Component)
		}
		_, err = fmt.Fprintln(out, strings.Join(components, " "))
		return err
	}
	pkg, err := cfg.Package(component)
	if err != nil {
		return err
	}
	if field == "package" {
		_, err = fmt.Fprintln(out, pkg.PackageName())
		return err
	}
	version, err := readVersion(root, component)
	if err != nil {
		return err
	}
	if len(version) > 128 {
		return fmt.Errorf("image tag exceeds 128 characters")
	}
	switch field {
	case "":
		_, err = fmt.Fprintf(out, "version=%s\npackage=%s\n", version, pkg.PackageName())
	case "version":
		_, err = fmt.Fprintln(out, version)
	default:
		return fmt.Errorf("unknown field %q", field)
	}
	return err
}

func plan(out io.Writer, cfg images.Config, changed []string) error {
	packages := cfg.Packages
	if changed != nil {
		packages = cfg.Changed(changed)
	}
	matrix, err := json.Marshal(struct {
		Include []images.Target `json:"include"`
	}{cfg.Matrix(packages)})
	if err != nil {
		return err
	}
	packageJSON, err := json.Marshal(packages)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "matrix=%s\npackages=%s\nplatforms=%s\n", matrix, packageJSON, cfg.Platforms())
	return err
}

func planFromRef(out io.Writer, root, base string) error {
	cfg, err := images.Load(root)
	if err != nil {
		return err
	}
	var changed []string
	if base != "" {
		changed, err = changedPaths(base)
		if err != nil {
			return err
		}
	}
	return plan(out, cfg, changed)
}

func changedPaths(ref string) ([]string, error) {
	data, err := exec.Command("git", "diff", "--no-renames", "--name-only", "-z", ref, "HEAD").Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), nil
}

func checkPublication(out io.Writer, root, component, ref string) error {
	cfg, err := images.Load(root)
	if err != nil {
		return err
	}
	if _, err := cfg.Package(component); err != nil {
		return err
	}
	version, err := readVersion(root, component)
	if err != nil {
		return err
	}
	commits, err := exec.Command("git", "rev-list", "--first-parent", "HEAD.."+ref).Output()
	if err != nil {
		return err
	}
	publish := true
	// Look past newer tags for a superseding build of this tag.
	for _, commit := range strings.Fields(string(commits)) {
		pin, err := exec.Command("git", "-C", filepath.Join(root, "common", component), "show", commit+":./release.txt").Output()
		if err != nil {
			return err
		}
		candidateVersion, err := parseVersion(string(pin), component)
		if err != nil {
			return err
		}
		if candidateVersion != version {
			continue
		}
		changed, err := changedPaths(commit)
		if err != nil {
			return err
		}
		for _, pkg := range cfg.Changed(changed) {
			if pkg.Component == component {
				publish = false
				break
			}
		}
		break
	}
	_, err = fmt.Fprintf(out, "publish=%t\n", publish)
	return err
}

func main() {
	component := flag.String("component", "", "Image component for package metadata")
	root := flag.String("packaging-dir", "packaging", "Path to packaging/")
	field := flag.String("field", "", "Print version, package, or components (default: GitHub Actions metadata)")
	buildPlan := flag.Bool("plan", false, "Print a build plan as GitHub Actions outputs")
	base := flag.String("base", "", "Select packages changed since this git ref (default: all)")
	publishRef := flag.String("publish-ref", "", "Check for superseding inputs for the same component and version at this git ref")
	flag.Parse()
	var err error
	if *buildPlan {
		err = planFromRef(os.Stdout, *root, *base)
	} else if *publishRef != "" {
		err = checkPublication(os.Stdout, *root, *component, *publishRef)
	} else {
		err = run(os.Stdout, *root, *component, *field)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
