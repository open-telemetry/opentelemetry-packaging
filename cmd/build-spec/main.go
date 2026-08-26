// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Command build-spec drives the spec-based source RPM build used by COPR.
// It renders the spec, reports its normalized RPM version for source archive
// naming, and stages package payloads when rpmbuild executes the generated spec.
//
// Usage:
//
//	go run ./cmd/build-spec -version 1.0.0 -output build/srpm/SPECS
//	go run ./cmd/build-spec -version 1.0.0 -print-rpm-version
//	go run ./cmd/build-spec -version 1.0.0 -arch amd64 -component injector -stage-root /path/to/buildroot -filelist-dir /path/to/filelists
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
	outputDir := flag.String("output", "build/srpm/SPECS", "Output directory for the generated spec")
	component := flag.String("component", "all", "Component to stage: injector, java, nodejs, dotnet, python, meta, or all")
	packagingDir := flag.String("packaging-dir", "", "Path to packaging/ directory (auto-detected if empty)")
	configCheckBinary := flag.String("config-check-binary", "",
		"Path to a prebuilt otel-config-check binary for the target architecture")
	stageRoot := flag.String("stage-root", "",
		"Stage the payload into this rpmbuild %buildroot instead of generating a spec")
	filelistDir := flag.String("filelist-dir", "",
		"Directory for generated RPM %files fragments; required with -stage-root")
	printRPMVersion := flag.Bool("print-rpm-version", false,
		"Print the normalized RPM Version field and exit")

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
		ConfigCheckBinary: absCheckBinary,
	}

	if *stageRoot != "" {
		stage(cfg, *component, *stageRoot, *filelistDir)
		return
	}
	if *filelistDir != "" {
		log.Fatal("error: -filelist-dir requires -stage-root")
	}
	if *component != "all" {
		log.Fatal("error: -component is only valid with -stage-root")
	}

	writeSpec(cfg, *outputDir)
}

func stage(cfg builder.Config, component, stageRoot, filelistDir string) {
	if filelistDir == "" {
		log.Fatal("error: -filelist-dir is required with -stage-root")
	}

	absRoot, err := filepath.Abs(stageRoot)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	absFilelistDir, err := filepath.Abs(filelistDir)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	components := builder.AllComponents
	if component != "all" {
		selected, ok := builder.ComponentByName(component)
		if !ok {
			log.Fatalf("error: unknown component %q (expected injector, java, nodejs, dotnet, python, meta, or all)", component)
		}
		components = []builder.Component{selected}
	}

	for _, comp := range components {
		if err := builder.Stage(cfg, comp, absRoot, absFilelistDir); err != nil {
			log.Fatalf("error staging %s: %v", comp.Name, err)
		}
	}
	fmt.Println("All components staged successfully")
}

func writeSpec(cfg builder.Config, outputDir string) {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		log.Fatalf("error: creating output dir: %v", err)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	cfg.OutputDir = absOutput

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
}
