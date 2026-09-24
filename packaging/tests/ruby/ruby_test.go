// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ruby_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/open-telemetry/opentelemetry-packaging/testutil/otelsink"
	"github.com/stretchr/testify/assert"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const exportTimeout = 90 * time.Second

type target struct {
	format    string
	baseImage string
}

var matrix = []target{
	{format: "deb", baseImage: "ruby:3.3-slim-bookworm"},
	{format: "rpm", baseImage: "fedora:41"},
}

func TestRubyAutoInstrumentation(t *testing.T) {
	ctx := context.Background()

	for _, tg := range matrix {
		tg := tg
		t.Run(tg.format+"/"+imageSlug(tg.baseImage), func(t *testing.T) {
			t.Parallel()
			runRubyCase(t, ctx, tg)
		})
	}
}

func imageSlug(image string) string {
	return strings.NewReplacer(":", "-", "/", "-").Replace(image)
}

func runRubyCase(t *testing.T, ctx context.Context, tg target) {
	arch := testutil.TargetArch()
	buildArgs := map[string]*string{
		"BASE_IMAGE": &tg.baseImage,
		"ARCH":       &arch,
	}
	if tg.format == "rpm" {
		rpmArch := "x86_64"
		if arch == "arm64" {
			rpmArch = "aarch64"
		}
		buildArgs["RPM_ARCH"] = &rpmArch
	}

	sink := otelsink.Start(t)
	container := testutil.StartServiceContainerOpts(t, ctx, testutil.ServiceContainerOptions{
		DockerfilePath:  fmt.Sprintf("packaging/tests/ruby/Dockerfile.%s", tg.format),
		BuildArgs:       buildArgs,
		ExposedPorts:    []string{"3000/tcp"},
		WaitPort:        "3000/tcp",
		WaitPath:        "/",
		Env:             sink.Env(),
		HostAccessPorts: sink.HostAccessPorts(),
	})

	for range 3 {
		status := testutil.ContainerHTTPGet(t, ctx, container, "3000/tcp", "/")
		assert.Equal(t, 200, status)
	}

	traces := sink.WaitForTraces(t, exportTimeout, func(tr *otelsink.Traces) bool {
		return tr.WithKind(tracepb.Span_SPAN_KIND_CLIENT).Len() > 0
	})
	spans := traces.
		WithKind(tracepb.Span_SPAN_KIND_CLIENT).
		WithResourceAttribute("service.name", "ruby-testapp").
		Spans()
	assert.NotEmpty(t, spans, "Ruby auto-instrumentation should export a client span")
}
