// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Command update-jmx-target-systems rewrites the list of JMX target systems in
// the documentation shipped with the opentelemetry-jmx-scraper package.
//
// The values a scraper accepts come from the metric definitions inside its JAR,
// so the list changes whenever the pin in
// packaging/common/jmx-scraper/release.txt moves. Running this command after a
// version bump keeps config.properties, the man page, and the README truthful;
// CI runs it and fails when the result differs from what is committed.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/builder"
)

// generatedRegion is one block of documentation derived from the JAR. Only the
// list itself sits between the markers, so the prose around it stays editable.
type generatedRegion struct {
	// path is relative to the packaging directory.
	path   string
	begin  string
	end    string
	render func(systems []string) string
}

// config.properties deliberately carries no generated block. It ships as
// config|noreplace and every operator edits it, so rewriting it on a version
// bump would raise a dpkg conffile prompt, leave an rpm .rpmnew behind, and
// strand the list at whatever the operator first installed. It points at these
// two documents instead, neither of which is a configuration file.
var regions = []generatedRegion{
	{
		path:   "common/jmx-scraper/opentelemetry-jmx-scraper.8.tmpl",
		begin:  `.\" BEGIN GENERATED target systems (make update-jmx-target-systems)`,
		end:    `.\" END GENERATED`,
		render: renderManPage,
	},
	{
		path:   "common/jmx-scraper/README.md",
		begin:  "<!-- BEGIN GENERATED target systems (make update-jmx-target-systems) -->",
		end:    "<!-- END GENERATED -->",
		render: renderMarkdown,
	},
}

func main() {
	packagingDir := flag.String("packaging-dir", "packaging", "Path to the packaging directory")
	check := flag.Bool("check", false, "Report whether the documented list is stale instead of rewriting it")
	flag.Parse()

	systems, err := builder.JMXScraperTargetSystems(builder.Config{PackagingDir: *packagingDir})
	if err != nil {
		log.Fatalf("error: reading target systems: %v", err)
	}
	fmt.Printf("  %d target systems: %s\n", len(systems), strings.Join(systems, ", "))

	var stale []string
	for _, region := range regions {
		path := *packagingDir + "/" + region.path
		updated, err := rewrite(path, region, systems, *check)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		if !updated {
			fmt.Printf("  unchanged %s\n", path)
			continue
		}
		stale = append(stale, path)
		if *check {
			fmt.Printf("  STALE %s\n", path)
			continue
		}
		fmt.Printf("  updated %s\n", path)
	}

	if *check && len(stale) > 0 {
		log.Fatalf("error: the documented JMX target systems are out of date in %s.\n"+
			"       Run 'make update-jmx-target-systems' and commit the result.",
			strings.Join(stale, ", "))
	}
}

// rewrite replaces the region's marked block in the file at path, reporting
// whether that changed anything. With check set it leaves the file alone and
// only reports what the rewrite would have done.
func rewrite(path string, region generatedRegion, systems []string, check bool) (bool, error) {
	original, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}

	lines := strings.Split(string(original), "\n")
	begin, end := -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case region.begin:
			begin = i
		case region.end:
			end = i
		}
	}
	switch {
	case begin < 0:
		return false, fmt.Errorf("%s: missing marker %q", path, region.begin)
	case end < 0:
		return false, fmt.Errorf("%s: missing marker %q", path, region.end)
	case end < begin:
		return false, fmt.Errorf("%s: marker %q precedes %q", path, region.end, region.begin)
	}

	body := strings.Split(region.render(systems), "\n")
	updated := append([]string{}, lines[:begin+1]...)
	updated = append(updated, body...)
	updated = append(updated, lines[end:]...)

	result := strings.Join(updated, "\n")
	if result == string(original) {
		return false, nil
	}
	if check {
		return true, nil
	}
	return true, os.WriteFile(path, []byte(result), 0o644)
}

// renderManPage emits a roff bullet list, matching how the README presents the
// same values. The enclosing .RS/.RE live outside the markers in the template.
func renderManPage(systems []string) string {
	items := make([]string, 0, len(systems)*2)
	for _, system := range systems {
		items = append(items, `.IP \(bu 2`, system)
	}
	return strings.Join(items, "\n")
}

// renderMarkdown emits one bullet per value, which stays readable as the list
// grows and keeps a version bump to a one-line-per-system diff.
func renderMarkdown(systems []string) string {
	bullets := make([]string, 0, len(systems))
	for _, system := range systems {
		bullets = append(bullets, "- `"+system+"`")
	}
	return strings.Join(bullets, "\n")
}
