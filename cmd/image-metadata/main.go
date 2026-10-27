// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
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

// git runs a git command, reporting its stderr on failure. Without it the only
// diagnostic a failing publish job leaves behind is "exit status 128".
func git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func changedPaths(ref string) ([]string, error) {
	data, err := git("diff", "--no-renames", "--name-only", "-z", ref, "HEAD")
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
	// Yield only to the tip of main, never to an intermediate commit.
	//
	// A workflow run exists per push, at the push head, so the commits in
	// between -- those a rebase-merge, a merge-queue batch, or any
	// multi-commit push brings in -- get no run of their own. Treating one as
	// a superseding build suppresses this tag in favour of a build that never
	// happens, and nothing publishes it: the run at the tip carries a
	// different version, and a manual dispatch republishes whatever main
	// pins now. The tip, by contrast, always has a run.
	//
	// So an older tag still publishes once the version has moved on, and the
	// tip reclaims the tag only when it rebuilds that same version itself.
	pin, err := git("-C", filepath.Join(root, "common", component), "show", ref+":./release.txt")
	if err != nil {
		return err
	}
	tipVersion, err := parseVersion(string(pin), component)
	if err != nil {
		return err
	}
	publish := true
	if tipVersion == version {
		changed, err := changedPaths(ref)
		if err != nil {
			return err
		}
		for _, pkg := range cfg.Changed(changed) {
			if pkg.Component == component {
				publish = false
				break
			}
		}
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
