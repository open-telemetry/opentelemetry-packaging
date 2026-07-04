// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package testutil provides shared helpers for packaging integration tests.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// RepoRoot returns the absolute path to the repository root by walking up from
// the caller's directory until it finds a go.mod file.
func RepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not find repo root (no go.mod found)")
		dir = parent
	}
}

// TargetArch returns the CPU architecture the test containers should be built
// and run for. It mirrors the Makefile's ARCH variable (read from the ARCH
// environment variable), defaulting to amd64 — the same default the package
// build uses. The container platform must match the architecture the packages
// under test were built for, otherwise apt/dnf cannot resolve them.
func TargetArch() string {
	if arch := os.Getenv("ARCH"); arch != "" {
		return arch
	}
	return "amd64"
}

// dockerfileBuild returns a FromDockerfile that pins the build to the target
// architecture. testcontainers-go does not otherwise set the build platform,
// so on a host whose native architecture differs from the packages under test
// (e.g. arm64 Apple Silicon building amd64 packages), Docker would build the
// image for the host architecture and the package installation would fail with
// unresolvable dependencies. Setting the build platform here — and
// ImagePlatform on the request — forces the correct architecture.
//
// The builder version must be left at the default (classic builder): forcing
// BuildKit makes the daemon resolve docker.io base images through the client's
// BuildKit session, which testcontainers-go never establishes, and the build
// fails with "failed to resolve source metadata (...): no active sessions".
// The classic builder honors the platform parameter without a session.
func dockerfileBuild(root, dockerfilePath string, buildArgs map[string]*string) testcontainers.FromDockerfile {
	arch := TargetArch()
	return testcontainers.FromDockerfile{
		Context:       root,
		Dockerfile:    dockerfilePath,
		BuildArgs:     buildArgs,
		KeepImage:     true,
		PrintBuildLog: true,
		BuildOptionsModifier: func(opts *client.ImageBuildOptions) {
			opts.Platforms = []ocispec.Platform{{OS: "linux", Architecture: arch}}
		},
	}
}

// imagePlatform returns the "linux/<arch>" platform string for the target
// architecture, used to run the built image.
func imagePlatform() string {
	return "linux/" + TargetArch()
}

// RunPackageTest builds a Docker image from the given Dockerfile (path relative
// to the repo root), runs it, and returns the container's combined output.
// The container is expected to exit on its own; a non-zero exit code fails the test.
func RunPackageTest(
	t *testing.T,
	ctx context.Context,
	dockerfilePath string,
	buildArgs map[string]*string,
) string {
	t.Helper()
	root := RepoRoot(t)

	req := testcontainers.ContainerRequest{
		FromDockerfile: dockerfileBuild(root, dockerfilePath, buildArgs),
		ImagePlatform:  imagePlatform(),
		WaitingFor:     wait.ForExit().WithExitTimeout(5 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	logs, err := container.Logs(ctx)
	require.NoError(t, err)
	defer logs.Close()

	logBytes, err := io.ReadAll(logs)
	require.NoError(t, err)
	output := string(logBytes)

	state, err := container.State(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, state.ExitCode, "container exited with non-zero status.\n\nOutput:\n%s", output)

	return output
}

// StartServiceContainer builds a Docker image from the given Dockerfile, starts
// the container with the specified exposed ports, and waits for an HTTP endpoint
// to become ready. Returns the running container.
func StartServiceContainer(
	t *testing.T,
	ctx context.Context,
	dockerfilePath string,
	buildArgs map[string]*string,
	exposedPorts []string,
	waitPort string,
	waitPath string,
) testcontainers.Container {
	t.Helper()
	return StartServiceContainerOpts(t, ctx, ServiceContainerOptions{
		DockerfilePath: dockerfilePath,
		BuildArgs:      buildArgs,
		ExposedPorts:   exposedPorts,
		WaitPort:       waitPort,
		WaitPath:       waitPath,
	})
}

// ServiceContainerOptions configures a long-running service container.
type ServiceContainerOptions struct {
	// DockerfilePath is the path to the Dockerfile relative to the repo root.
	DockerfilePath string
	// BuildArgs are passed to the image build.
	BuildArgs map[string]*string
	// ExposedPorts are published to the host (e.g. "8080/tcp").
	ExposedPorts []string
	// WaitPort/WaitPath define the HTTP readiness probe.
	WaitPort string
	WaitPath string
	// Env sets environment variables inside the container.
	Env map[string]string
	// HostAccessPorts are host ports the container may reach at
	// host.testcontainers.internal — used to point a workload at an
	// otelsink.Sink listening on the host.
	HostAccessPorts []int
}

// StartServiceContainerOpts builds and starts a service container per the given
// options, waiting for its HTTP endpoint to become ready. Returns the running
// container.
func StartServiceContainerOpts(t *testing.T, ctx context.Context, opts ServiceContainerOptions) testcontainers.Container {
	t.Helper()
	root := RepoRoot(t)

	req := testcontainers.ContainerRequest{
		FromDockerfile:  dockerfileBuild(root, opts.DockerfilePath, opts.BuildArgs),
		ImagePlatform:   imagePlatform(),
		ExposedPorts:    opts.ExposedPorts,
		Env:             opts.Env,
		HostAccessPorts: opts.HostAccessPorts,
		WaitingFor:      wait.ForHTTP(opts.WaitPath).WithPort(opts.WaitPort).WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	return container
}

// StartContainer builds a Docker image from the given Dockerfile and starts the
// container. Unlike StartServiceContainer, it does not wait for an HTTP endpoint;
// use this for containers that run `sleep infinity` and are probed via Exec.
func StartContainer(
	t *testing.T,
	ctx context.Context,
	dockerfilePath string,
	buildArgs map[string]*string,
) testcontainers.Container {
	t.Helper()
	root := RepoRoot(t)

	req := testcontainers.ContainerRequest{
		FromDockerfile: dockerfileBuild(root, dockerfilePath, buildArgs),
		ImagePlatform:  imagePlatform(),
		WaitingFor:     wait.ForLog("").WithStartupTimeout(2 * time.Minute),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	return container
}

// ContainerHTTPGet sends an HTTP GET request to the given port and path on
// a running container. Returns the response status code.
func ContainerHTTPGet(t *testing.T, ctx context.Context, container testcontainers.Container, port, path string) int {
	t.Helper()
	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, port)
	require.NoError(t, err)

	url := fmt.Sprintf("http://%s:%s%s", host, mappedPort.Port(), path)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	return resp.StatusCode
}

// ContainerLogs returns the combined stdout/stderr of a running container.
func ContainerLogs(t *testing.T, ctx context.Context, container testcontainers.Container) string {
	t.Helper()
	logs, err := container.Logs(ctx)
	require.NoError(t, err)
	defer logs.Close()

	data, err := io.ReadAll(logs)
	require.NoError(t, err)
	return string(data)
}

// WaitForFileContaining polls a container until the given file contains the
// specified substring, or the timeout is reached. Returns the file contents.
func WaitForFileContaining(t *testing.T, ctx context.Context, container testcontainers.Container, path, substr string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		reader, err := container.CopyFileFromContainer(ctx, path)
		if err == nil {
			data, readErr := io.ReadAll(reader)
			reader.Close()
			if readErr == nil && strings.Contains(string(data), substr) {
				return string(data)
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("timed out waiting for %s to contain %q", path, substr)
	return ""
}

// FileExistsInContainer returns true if the given path exists inside the container.
func FileExistsInContainer(t *testing.T, ctx context.Context, container testcontainers.Container, path string) bool {
	t.Helper()
	code, _, err := container.Exec(ctx, []string{"test", "-f", path})
	require.NoError(t, err)
	return code == 0
}

// FileContainsInContainer returns true if the file at the given path inside the
// container contains the specified substring.
func FileContainsInContainer(t *testing.T, ctx context.Context, container testcontainers.Container, path, substr string) bool {
	t.Helper()
	reader, err := container.CopyFileFromContainer(ctx, path)
	require.NoError(t, err, "failed to copy %s from container", path)
	defer reader.Close()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return strings.Contains(string(data), substr)
}

// --------------------------------------------------------------------------
// OTLP JSON helpers for trace assertions
// --------------------------------------------------------------------------

// TraceExport represents the top-level structure of an OTLP JSON export.
type TraceExport struct {
	ResourceSpans []ResourceSpan `json:"resourceSpans"`
}

// ResourceSpan is a collection of spans from a resource.
type ResourceSpan struct {
	Resource   Resource    `json:"resource"`
	ScopeSpans []ScopeSpan `json:"scopeSpans"`
}

// Resource describes the entity producing telemetry.
type Resource struct {
	Attributes []Attribute `json:"attributes"`
}

// ScopeSpan groups spans by instrumentation scope.
type ScopeSpan struct {
	Scope InstrumentationScope `json:"scope"`
	Spans []Span               `json:"spans"`
}

// InstrumentationScope identifies the instrumentation library.
type InstrumentationScope struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Span represents a single trace span.
type Span struct {
	Name       string      `json:"name"`
	Kind       int         `json:"kind"`
	Attributes []Attribute `json:"attributes"`
}

// Attribute is a key-value pair.
type Attribute struct {
	Key   string         `json:"key"`
	Value AttributeValue `json:"value"`
}

// AttributeValue holds a typed attribute value.
type AttributeValue struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    string `json:"intValue,omitempty"`
}

// SpanKindServer is the OTLP numeric value for SPAN_KIND_SERVER.
const SpanKindServer = 2

// ParseTraceExportsFromLogs extracts OTLP JSON trace exports from container
// logs. The console exporter writes one JSON object per line; non-JSON lines
// (e.g., Tomcat output) are silently skipped.
func ParseTraceExportsFromLogs(t *testing.T, logs string) []TraceExport {
	t.Helper()
	var exports []TraceExport
	for _, line := range strings.Split(logs, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var export TraceExport
		if err := json.Unmarshal([]byte(line), &export); err != nil {
			continue // not an OTLP JSON line
		}
		if len(export.ResourceSpans) > 0 {
			exports = append(exports, export)
		}
	}
	return exports
}

// AllSpans extracts all spans from a slice of trace exports.
func AllSpans(exports []TraceExport) []Span {
	var spans []Span
	for _, export := range exports {
		for _, rs := range export.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				spans = append(spans, ss.Spans...)
			}
		}
	}
	return spans
}

// AllResources extracts all resources from a slice of trace exports.
func AllResources(exports []TraceExport) []Resource {
	var resources []Resource
	for _, export := range exports {
		for _, rs := range export.ResourceSpans {
			resources = append(resources, rs.Resource)
		}
	}
	return resources
}

// HasAttribute checks if a slice of attributes contains a given key.
func HasAttribute(attrs []Attribute, key string) bool {
	for _, a := range attrs {
		if a.Key == key {
			return true
		}
	}
	return false
}
