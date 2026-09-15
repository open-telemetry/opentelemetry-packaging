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
// The package is assembled with packaging/builder, exactly as a real vendor
// would: the component declares its relations and brands itself through
// builder.Config, and builder.Build produces the package. That keeps this mock
// honest — it exercises the same API surface a vendor depends on, so a change
// that breaks vendor packages breaks these tests too.
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

	"github.com/goreleaser/nfpm/v2/files"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/builder"
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

const description = "Mock ACME vendor replacement for the OpenTelemetry Java auto-instrumentation (test only)"

// vendorComponent is the mock vendor package. Provides carries the virtual
// name it satisfies, while Conflicts and Replaces name the concrete upstream
// package it displaces; nfpm renders Replaces as DEB Replaces and RPM
// Obsoletes.
var vendorComponent = builder.Component{
	Name:        "acme-java",
	PackageName: "acme-java-autoinstrumentation",
	Description: description,
	Noarch:      true,
	Relations: builder.Relations{
		Provides:  []string{"opentelemetry-java-autoinstrumentation1"},
		Conflicts: []string{"opentelemetry-java-autoinstrumentation"},
		Replaces:  []string{"opentelemetry-java-autoinstrumentation"},
		Suggests:  []string{"opentelemetry-injector1"},
	},
	ContentsFunc: vendorContents,
}

// vendorContents stages the two files the package ships.
func vendorContents(builder.Config) (files.Contents, func(), error) {
	staging, err := os.MkdirTemp("", "acme-java-*")
	if err != nil {
		return nil, nil, fmt.Errorf("creating staging directory: %w", err)
	}
	cleanup := func() { os.RemoveAll(staging) }

	agentPath := filepath.Join(staging, "opentelemetry-javaagent.jar")
	if err := os.WriteFile(agentPath, []byte(AgentContent), 0o644); err != nil {
		return nil, cleanup, fmt.Errorf("writing dummy agent: %w", err)
	}
	dropInPath := filepath.Join(staging, "java.conf")
	if err := os.WriteFile(dropInPath, []byte(dropInContent), 0o644); err != nil {
		return nil, cleanup, fmt.Errorf("writing drop-in: %w", err)
	}

	return files.Contents{
		builder.RegularFile(agentPath, "/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar", 0o644),
		builder.RegularFile(dropInPath, "/etc/opentelemetry/injector/conf.d/java.conf", 0o644),
	}, cleanup, nil
}

func main() {
	version := flag.String("version", "1.0.0", "package version")
	arch := flag.String("arch", "amd64", "target architecture: amd64 or arm64")
	format := flag.String("format", "all", "package format: deb, rpm, or all")
	output := flag.String("output", "build/packages-vendor", "output directory")
	flag.Parse()

	if err := os.MkdirAll(*output, 0o755); err != nil {
		log.Fatalf("error: creating output directory: %v", err)
	}

	// The mock ships no lifecycle scripts and stages its payload from a temp
	// directory, so it needs no PackagingDir: a vendor writing its own
	// component brings its own layout.
	cfg := builder.Config{
		Version:    *version,
		Arch:       *arch,
		OutputDir:  *output,
		Vendor:     "ACME",
		Maintainer: "The ACME Company",
		License:    "Apache-2.0",
		Homepage:   "https://github.com/open-telemetry/opentelemetry-packaging",
	}

	formats := []string{*format}
	if *format == "all" {
		formats = []string{"deb", "rpm"}
	}
	for _, f := range formats {
		outPath, err := builder.Build(cfg, f, vendorComponent)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		fmt.Printf("Building %s: %s\n", f, filepath.Base(outPath))
	}
}
