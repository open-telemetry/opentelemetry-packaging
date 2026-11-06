// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Command obs-publish uploads one package's sources to the Open Build Service,
// waits for them to build in every repository and architecture, and releases
// the package to its project's release targets.
//
// The credentials come from OBS_USERNAME and OBS_PASSWORD. Sources identical
// to the package's current ones are neither committed nor released again.
//
// Usage:
//
//	go run ./cmd/obs-publish -project home:opentelemetry:stable:build \
//	    -package opentelemetry-injector -dir build/deb-sources/opentelemetry-injector \
//	    -message "opentelemetry-injector 0.11.0" -release
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/obs"
)

func main() {
	log.SetFlags(0)

	apiURL := flag.String("apiurl", obs.DefaultAPIURL, "OBS API URL")
	project := flag.String("project", "", "OBS project to upload to (required)")
	pkg := flag.String("package", "", "OBS package to upload to (required)")
	dir := flag.String("dir", "", "Directory holding the package's sources, and nothing else (required)")
	message := flag.String("message", "", "Commit message")
	release := flag.Bool("release", false, "Release the package once it has built everywhere")
	releaseUnchanged := flag.Bool("release-unchanged", false, "With -release: release sources the package already holds, once built")
	timeout := flag.Duration("timeout", 3*time.Hour, "How long to wait for the builds")
	interval := flag.Duration("interval", 30*time.Second, "Time between build result checks")
	grace := flag.Duration("grace", 10*time.Minute, "How long after a commit a failure status may still be the previous revision's")
	flag.Parse()

	if *project == "" || *pkg == "" || *dir == "" {
		log.Fatal("error: -project, -package and -dir are required")
	}
	username, password := os.Getenv("OBS_USERNAME"), os.Getenv("OBS_PASSWORD")
	if username == "" || password == "" {
		log.Fatal("error: OBS_USERNAME and OBS_PASSWORD must be set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	outcome, err := obs.Publish(ctx, obs.NewClient(*apiURL, username, password), obs.Options{
		Project:          *project,
		Package:          *pkg,
		Dir:              *dir,
		Message:          *message,
		Release:          *release,
		ReleaseUnchanged: *releaseUnchanged,
		Interval:         *interval,
		Grace:            *grace,
		Logf:             log.Printf,
	})
	if err != nil {
		log.Fatalf("error: %v", err)
	}
	// GitHub Actions outputs, for a step that appends stdout to $GITHUB_OUTPUT.
	fmt.Printf("changed=%t\nrevision=%s\nreleased=%t\n", outcome.Changed, outcome.Revision, outcome.Released)
}
