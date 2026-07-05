// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// mkvendor builds the mock acme-java-autoinstrumentation package used by the
// vendor-replacement integration tests. The package provides the same virtual
// name as the upstream Java auto-instrumentation package and conflicts with
// and replaces its concrete name, mirroring the vendor package recipe in
// docs/design/packages-meta-architecture.md. It ships a dummy agent file and
// its own conf.d drop-in so the tests can tell vendor and upstream files
// apart.
//
// This is a test-only tool; the mock vendor component deliberately does not
// live in packaging/builder, where it would show up in the production
// component list used by cmd/build-packages.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"

	// Register packagers via init().
	_ "github.com/goreleaser/nfpm/v2/deb"
	_ "github.com/goreleaser/nfpm/v2/rpm"
)

// AgentContent is the payload of the dummy vendor agent JAR. The vendor tests
// assert on the "ACME" substring to tell the vendor agent from the upstream
// one.
const AgentContent = "ACME vendor test agent\n"

// dropInContent is the vendor's conf.d drop-in. It uses the same key as the
// upstream packaging/common/java/injector.conf, pointing at the same path the
// vendor package installs its own agent to.
const dropInContent = `# ACME vendor drop-in
# Installed by acme-java-autoinstrumentation package

jvm_auto_instrumentation_agent_path=/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar
`

func main() {
	version := flag.String("version", "1.0.0", "package version")
	arch := flag.String("arch", "amd64", "target architecture: amd64 or arm64")
	format := flag.String("format", "all", "package format: deb, rpm, or all")
	output := flag.String("output", "build/packages-vendor", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*output, 0o755); err != nil {
		log.Fatalf("error: creating output directory: %v", err)
	}

	staging, err := os.MkdirTemp("", "acme-java-*")
	if err != nil {
		log.Fatalf("error: creating staging directory: %v", err)
	}
	defer os.RemoveAll(staging)

	agentPath := filepath.Join(staging, "opentelemetry-javaagent.jar")
	if err := os.WriteFile(agentPath, []byte(AgentContent), 0o644); err != nil {
		log.Fatalf("error: writing dummy agent: %v", err)
	}
	dropInPath := filepath.Join(staging, "java.conf")
	if err := os.WriteFile(dropInPath, []byte(dropInContent), 0o644); err != nil {
		log.Fatalf("error: writing drop-in: %v", err)
	}

	formats := []string{*format}
	if *format == "all" {
		formats = []string{"deb", "rpm"}
	}
	for _, f := range formats {
		if err := build(f, *version, *arch, *output, agentPath, dropInPath); err != nil {
			log.Fatalf("error: %v", err)
		}
	}
}

func build(format, version, arch, output, agentPath, dropInPath string) error {
	pkgArch := "all"
	if format == "rpm" {
		pkgArch = "noarch"
	}
	_ = arch // The mock package is arch-independent; the flag mirrors cmd/build-packages.

	description := "Mock ACME vendor replacement for the OpenTelemetry Java auto-instrumentation (test only)"
	info := &nfpm.Info{
		Name:        "acme-java-autoinstrumentation",
		Version:     version,
		Arch:        pkgArch,
		Platform:    "linux",
		Description: description,
		Vendor:      "ACME",
		Maintainer:  "The ACME Company",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/open-telemetry/opentelemetry-packaging",
		Overridables: nfpm.Overridables{
			Provides:  []string{"opentelemetry-java-autoinstrumentation1"},
			Conflicts: []string{"opentelemetry-java-autoinstrumentation"},
			// nfpm maps Replaces to DEB Replaces and RPM Obsoletes.
			Replaces: []string{"opentelemetry-java-autoinstrumentation"},
			Suggests: []string{"opentelemetry-injector1"},
			RPM: nfpm.RPM{
				Summary: description,
			},
			Contents: files.Contents{
				{
					Source:      agentPath,
					Destination: "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar",
					FileInfo:    &files.ContentFileInfo{Mode: 0o644},
				},
				{
					Source:      dropInPath,
					Destination: "/etc/opentelemetry/injector/conf.d/java.conf",
					FileInfo:    &files.ContentFileInfo{Mode: 0o644},
				},
			},
		},
	}

	packager, err := nfpm.Get(format)
	if err != nil {
		return fmt.Errorf("getting %s packager: %w", format, err)
	}

	outPath := filepath.Join(output, packager.ConventionalFileName(info))
	fmt.Printf("Building %s: %s\n", format, filepath.Base(outPath))

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", outPath, err)
	}
	if err := packager.Package(info, f); err != nil {
		f.Close()
		os.Remove(outPath)
		return fmt.Errorf("packaging: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(outPath)
		return fmt.Errorf("closing %s: %w", outPath, err)
	}
	return nil
}
