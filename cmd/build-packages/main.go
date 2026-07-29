// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Command build-packages creates OpenTelemetry DEB and RPM packages using nfpm.
//
// It replaces the previous FPM-based shell scripts and Docker build container,
// producing identical packages as pure Go — no Ruby, FPM, or Docker required
// for package creation itself. Upstream artifacts (libotelinject.so, Java agent
// JAR, Node.js agent, .NET agent) are still fetched from their respective
// release channels. Python packages are fetched via pip at build time.
//
// Usage:
//
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format deb -output build/packages
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format rpm -output build/packages
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format all -output build/packages
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -format deb -component injector -output build/packages
//
// It also drives the spec-based RPM build used by COPR, where rpmbuild owns the
// packaging step instead of nfpm:
//
//	go run ./cmd/build-packages -version 1.0.0 -format spec -output build/srpm/SPECS
//	go run ./cmd/build-packages -version 1.0.0 -arch amd64 -component injector -stage-root /path/to/buildroot -filelist-dir /path/to/filelists
//	go run ./cmd/build-packages -version 1.0.0 -print-rpm-version
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
	format := flag.String("format", "all", "Package format: deb, rpm, spec, or all")
	outputDir := flag.String("output", "build/packages", "Output directory for built packages")
	component := flag.String("component", "all", "Component to build: injector, java, nodejs, dotnet, python, meta, or all")
	packagingDir := flag.String("packaging-dir", "", "Path to packaging/ directory (auto-detected if empty)")
	configCheckBinary := flag.String("config-check-binary", "",
		"Path to a prebuilt otel-config-check binary for the target architecture "+
			"(defaults to build/bin/otel-config-check-<arch>; build it with `make otel-config-check`)")
	stageRoot := flag.String("stage-root", "",
		"Stage the payload into this directory (an rpmbuild %buildroot) instead of building a package")
	filelistDir := flag.String("filelist-dir", "",
		"Directory for the generated RPM %files fragments; required with -stage-root")
	printRPMVersion := flag.Bool("print-rpm-version", false,
		"Print the RPM Version field derived from -version and exit")

	flag.Parse()

	if *version == "" {
		log.Fatal("error: -version is required")
	}

	if *printRPMVersion {
		rpmVersion, _, err := builder.RPMVersionRelease(*version)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		fmt.Println(rpmVersion)
		return
	}

	switch *arch {
	case "amd64", "arm64":
		// valid
	default:
		log.Fatalf("error: unknown architecture %q (expected amd64 or arm64)", *arch)
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

	checkBinary := *configCheckBinary
	if checkBinary == "" {
		checkBinary = filepath.Join("build", "bin", "otel-config-check-"+*arch)
	}
	absCheckBinary, err := filepath.Abs(checkBinary)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	cfg := builder.Config{
		Version:           strings.TrimPrefix(*version, "v"),
		Arch:              *arch,
		PackagingDir:      pkgDir,
		OutputDir:         absOutput,
		ConfigCheckBinary: absCheckBinary,
	}

	components := builder.AllComponents
	if *component != "all" {
		c, ok := builder.ComponentByName(*component)
		if !ok {
			log.Fatalf("error: unknown component %q (expected injector, java, nodejs, dotnet, python, meta, or all)", *component)
		}
		components = []builder.Component{c}
	}

	// Staging mode: materialize the payload into an rpmbuild buildroot and emit
	// the matching %files fragments, leaving the packaging itself to rpmbuild.
	if *stageRoot != "" {
		if *filelistDir == "" {
			log.Fatal("error: -filelist-dir is required with -stage-root")
		}
		absRoot, err := filepath.Abs(*stageRoot)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		absFilelistDir, err := filepath.Abs(*filelistDir)
		if err != nil {
			log.Fatalf("error: %v", err)
		}
		for _, comp := range components {
			if err := builder.Stage(cfg, comp, absRoot, absFilelistDir); err != nil {
				log.Fatalf("error staging %s: %v", comp.Name, err)
			}
		}
		fmt.Println("All components staged successfully")
		return
	}

	var formats []string
	switch *format {
	case "all":
		formats = []string{"deb", "rpm"}
	case "deb", "rpm":
		formats = []string{*format}
	case "spec":
		specPath := filepath.Join(absOutput, builder.SpecName+".spec")
		f, err := os.Create(specPath)
		if err != nil {
			log.Fatalf("error: creating %s: %v", specPath, err)
		}
		if err := builder.WriteSpec(cfg, f); err != nil {
			f.Close()
			os.Remove(specPath)
			log.Fatalf("error writing spec: %v", err)
		}
		if err := f.Close(); err != nil {
			log.Fatalf("error: closing %s: %v", specPath, err)
		}
		fmt.Printf("Wrote %s\n", specPath)
		return
	default:
		log.Fatalf("error: unknown format %q (expected deb, rpm, spec, or all)", *format)
	}

	for _, pkgFormat := range formats {
		for _, comp := range components {
			if err := builder.Build(cfg, pkgFormat, comp); err != nil {
				log.Fatalf("error building %s %s: %v", pkgFormat, comp.Name, err)
			}
		}
	}

	fmt.Println("All packages built successfully")
}
