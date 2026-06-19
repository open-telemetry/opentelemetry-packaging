// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Command build-packages creates OpenTelemetry DEB and RPM packages using nfpm.
//
// It replaces the previous FPM-based shell scripts and Docker build container,
// producing identical packages as pure Go — no Ruby, FPM, or Docker required
// for package creation itself. Upstream artifacts (libotelinject.so, Java agent
// JAR, Node.js agent, .NET agent) are still fetched from their respective
// release channels.
//
// Usage:
//
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format deb -output build/packages
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format rpm -output build/packages
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format all -output build/packages
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format deb -component injector -output build/packages
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

	version := flag.String("version", "", "Package version (required)")
	arch := flag.String("arch", "amd64", "Target architecture (amd64 or arm64)")
	format := flag.String("format", "all", "Package format: deb, rpm, or all")
	outputDir := flag.String("output", "build/packages", "Output directory for built packages")
	component := flag.String("component", "all", "Component to build: injector, java, nodejs, dotnet, meta, or all")
	packagingDir := flag.String("packaging-dir", "", "Path to packaging/ directory (auto-detected if empty)")

	flag.Parse()

	if *version == "" {
		log.Fatal("error: -version is required")
	}

	// Auto-detect packaging dir relative to the working directory or the binary.
	pkgDir := *packagingDir
	if pkgDir == "" {
		// Try current directory first.
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

	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("error: creating output dir: %v", err)
	}
	absOutput, err := filepath.Abs(*outputDir)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	cfg := builder.Config{
		Version:      strings.TrimPrefix(*version, "v"),
		Arch:         *arch,
		PackagingDir: pkgDir,
		OutputDir:    absOutput,
	}

	var formats []string
	switch *format {
	case "all":
		formats = []string{"deb", "rpm"}
	case "deb", "rpm":
		formats = []string{*format}
	default:
		log.Fatalf("error: unknown format %q (expected deb, rpm, or all)", *format)
	}

	components := builder.AllComponents
	if *component != "all" {
		c, ok := builder.ComponentByName(*component)
		if !ok {
			log.Fatalf("error: unknown component %q (expected injector, java, nodejs, dotnet, meta, or all)", *component)
		}
		components = []builder.Component{c}
	}

	for _, fmt := range formats {
		for _, comp := range components {
			if err := builder.Build(cfg, fmt, comp); err != nil {
				log.Fatalf("error building %s %s: %v", fmt, comp.Name, err)
			}
		}
	}

	fmt.Println("All packages built successfully")
}
