// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// Package testutil provides shared helpers for packaging integration tests.
package testutil

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/moby/moby/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
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

// ImageOf returns the name of the image a container runs, e.g. to start
// further containers from an image that StartContainer built.
func ImageOf(t *testing.T, container testcontainers.Container) string {
	t.Helper()
	dc, ok := container.(*testcontainers.DockerContainer)
	require.True(t, ok, "container is not a DockerContainer")
	require.NotEmpty(t, dc.Image)
	return dc.Image
}

// StartContainerFromImage starts a container from an existing local image with
// its default command. Suites whose scenarios run many containers from the
// same Dockerfile should build the image once via StartContainer and start
// the remaining containers from its image: concurrent scenario subtests would
// otherwise each trigger their own build of the same Dockerfile, multiplying
// build work and image storage.
func StartContainerFromImage(t *testing.T, ctx context.Context, image string) testcontainers.Container {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:         image,
		ImagePlatform: imagePlatform(),
		WaitingFor:    wait.ForLog("").WithStartupTimeout(2 * time.Minute),
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
	// A dedicated client with a timeout: an unresponsive container should
	// fail this call fast, not wedge the test until its -timeout expires.
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
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

// FileExistsInContainer returns true if the given path exists inside the container.
func FileExistsInContainer(t *testing.T, ctx context.Context, container testcontainers.Container, path string) bool {
	t.Helper()
	code, _, err := container.Exec(ctx, []string{"test", "-f", path})
	require.NoError(t, err)
	return code == 0
}

// FileContentInContainer returns the full contents of a file inside the
// container, failing the test if the file cannot be read.
func FileContentInContainer(t *testing.T, ctx context.Context, container testcontainers.Container, path string) string {
	t.Helper()
	reader, err := container.CopyFileFromContainer(ctx, path)
	require.NoError(t, err, "failed to copy %s from container", path)
	defer reader.Close()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

// ExecInContainer runs the given command inside the container and returns its
// exit code and combined stdout/stderr.
func ExecInContainer(t *testing.T, ctx context.Context, container testcontainers.Container, cmd ...string) (int, string) {
	t.Helper()
	code, reader, err := container.Exec(ctx, cmd, tcexec.Multiplexed())
	require.NoError(t, err)

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return code, string(data)
}

// ExecSucceeds runs the given command inside the container, fails the test if
// it exits non-zero, and returns its combined stdout/stderr.
func ExecSucceeds(t *testing.T, ctx context.Context, container testcontainers.Container, cmd ...string) string {
	t.Helper()
	code, output := ExecInContainer(t, ctx, container, cmd...)
	require.Equal(t, 0, code, "command %v failed.\n\nOutput:\n%s", cmd, output)
	return output
}
