// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Command build-spec/deb writes the sources of the DEB packages built by debbuild
// on the Open Build Service: for each component, a spec and a source archive
// holding the payload already staged for every architecture.
//
// Each component lands in its own directory, named after its package, which is
// the content of the OBS package of the same name.
//
// Usage:
//
//	go run ./cmd/build-spec/deb -version 1.0.0 -output build/deb-sources
//	go run ./cmd/build-spec/deb -version 1.0.0 -component injector -output build/deb-sources
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/builder"
)

func main() {
	log.SetFlags(0)

	printArchs := flag.Bool("print-architectures", false,
		"Print the architectures the source archives are staged for, one per line, and exit")
	version := flag.String("version", "", "Package version (required)")
	release := flag.String("release", "", "Packaging revision (defaults to 1)")
	outputDir := flag.String("output", "build/deb-sources", "Output directory; each component gets a subdirectory named after its package")
	component := flag.String("component", "all", "Component to write: injector, java, nodejs, dotnet, python, meta, or all")
	packagingDir := flag.String("packaging-dir", "", "Path to packaging/ directory (auto-detected if empty)")
	configCheckDir := flag.String("config-check-dir", filepath.Join("build", "bin"),
		"Directory holding the prebuilt otel-config-check-<arch> binaries, one per architecture")

	flag.Parse()

	// The Makefile cross-compiles otel-config-check for each of these before
	// writing the sources, so that the list lives in one place.
	if *printArchs {
		for _, arch := range builder.Architectures {
			fmt.Println(arch)
		}
		return
	}

	if *version == "" {
		log.Fatal("error: -version is required")
	}

	pkgDir := *packagingDir
	if pkgDir == "" {
		if _, err := os.Stat("packaging"); err == nil {
			pkgDir = "packaging"
		} else {
			log.Fatal("error: could not find packaging/ directory; use -packaging-dir")
		}
	}
	pkgDir, err := filepath.Abs(pkgDir)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	absCheckDir, err := filepath.Abs(*configCheckDir)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	absOutput, err := filepath.Abs(*outputDir)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	components := builder.AllComponents
	if *component != "all" {
		selected, ok := builder.ComponentByName(*component)
		if !ok {
			log.Fatalf("error: unknown component %q (expected injector, java, nodejs, dotnet, python, meta, or all)", *component)
		}
		components = []builder.Component{selected}
	}

	cfg := builder.Config{
		Version:      strings.TrimPrefix(*version, "v"),
		Release:      *release,
		PackagingDir: pkgDir,
	}
	configCheckBinary := func(arch string) string {
		return filepath.Join(absCheckDir, "otel-config-check-"+arch)
	}

	for _, comp := range components {
		// The directory is what gets committed to OBS, so an archive left over
		// from another version must not survive in it.
		dir := filepath.Join(absOutput, comp.PackageName)
		if err := os.RemoveAll(dir); err != nil {
			log.Fatalf("error: %v", err)
		}
		sources, err := builder.WriteDebSources(cfg, comp, configCheckBinary, dir)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		fmt.Printf("Wrote %s\nWrote %s\n", sources.Spec, sources.Archive)
	}
}
