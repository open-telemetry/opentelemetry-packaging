// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package images_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/images"
	"github.com/open-telemetry/opentelemetry-packaging/testutil"
	"github.com/stretchr/testify/require"
	"pault.ag/go/debian/deb"
)

type entry struct {
	kind   byte
	mode   int64
	link   string
	digest string
}

func TestPackageImage(t *testing.T) {
	component := os.Getenv("IMAGE_COMPONENT")
	if component == "" {
		t.Skip("run make integration-test-image-<component>")
	}
	cfg, err := images.Load(filepath.Join(testutil.RepoRoot(t), "packaging"))
	require.NoError(t, err)
	pkg, err := cfg.Package(component)
	require.NoError(t, err)
	arch := testutil.TargetArch()
	require.True(t, slices.ContainsFunc(cfg.Architectures, func(a images.Architecture) bool { return a.Arch == arch }))

	image, engine := os.Getenv("IMAGE_REF"), os.Getenv("CONTAINER_ENGINE")
	require.NotEmpty(t, image)
	require.NotEmpty(t, engine)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Keep stderr out of the return value: callers use it as a container ID.
	command := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, engine, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		require.NoError(t, err, "%s %v: %s", engine, args, stderr.String())
		return strings.TrimSpace(string(out))
	}

	expected := packageFiles(t, pkg.PackageName())
	// --pull=never keeps a missing or wrong-platform local image a failure
	// rather than a silent test of the already published image.
	container := command("create", "--pull=never", "--platform", "linux/"+arch, image)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		out, err := exec.CommandContext(cleanupCtx, engine, "rm", "--force", container).CombinedOutput()
		if err != nil {
			t.Errorf("removing container: %v: %s", err, out)
		}
	})
	export := exec.CommandContext(ctx, engine, "export", container)
	var stderr bytes.Buffer
	export.Stderr = &stderr
	stdout, err := export.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, export.Start())
	actual := archiveFiles(t, tar.NewReader(stdout))
	require.NoError(t, export.Wait(), "%s", stderr.String())

	roots := map[string]bool{}
	for name, want := range expected {
		got, ok := actual[name]
		require.True(t, ok, "missing package path %s", name)
		require.Equal(t, want, got, "package path %s", name)
		roots["/"+strings.SplitN(strings.TrimPrefix(name, "/"), "/", 2)[0]] = true
	}
	// The image may carry the package's own payload and the base image, and
	// nothing else. Anything further is build or installation state that leaked
	// in -- a dpkg database, an ld.so.preload written by a maintainer script,
	// the DEB itself, a byte-compiled .pyc. Comparing against the base rather
	// than a hard-coded path list keeps this meaningful for every component.
	base := baseFiles(ctx, t, engine, arch)
	for name, got := range actual {
		if _, ok := expected[name]; ok {
			continue
		}
		want, ok := base[name]
		require.True(t, ok, "unexpected installation state %s: in neither the package nor the base image", name)
		require.Equal(t, want, got, "base image path %s", name)
	}

	args := []string{
		"run", "--rm", "--platform", "linux/" + arch,
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--tmpfs", "/target:rw,mode=1777", "--entrypoint", "cp", image, "-a",
	}
	var paths []string
	for root := range roots {
		paths = append(paths, root)
	}
	slices.Sort(paths)
	args = append(args, paths...)
	command(append(args, "/target/")...)
}

func archiveFiles(t *testing.T, archive *tar.Reader) map[string]entry {
	t.Helper()
	entries := map[string]entry{}
	for {
		hdr, err := archive.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		name := path.Clean("/" + hdr.Name)
		if name == "/" {
			continue
		}
		file := entry{kind: hdr.Typeflag, mode: hdr.Mode, link: hdr.Linkname}
		if hdr.Typeflag == tar.TypeReg || hdr.Typeflag == tar.TypeRegA {
			file.kind = tar.TypeReg
			digest := sha256.New()
			_, err := io.Copy(digest, archive)
			require.NoError(t, err)
			file.digest = fmt.Sprintf("%x", digest.Sum(nil))
		}
		entries[name] = file
	}
	return entries
}

func TestArchiveFiles(t *testing.T) {
	var buffer bytes.Buffer
	archive := tar.NewWriter(&buffer)
	for _, hdr := range []*tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./etc/example", Typeflag: tar.TypeDir, Mode: 0o750},
		{Name: "./etc/example/config", Typeflag: tar.TypeReg, Mode: 0o640, Size: 3},
		{Name: "./config-link", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "/etc/example/config"},
	} {
		require.NoError(t, archive.WriteHeader(hdr))
		if hdr.Typeflag == tar.TypeReg {
			_, err := archive.Write([]byte("abc"))
			require.NoError(t, err)
		}
	}
	require.NoError(t, archive.Close())
	require.Equal(t, map[string]entry{
		"/etc/example":        {kind: tar.TypeDir, mode: 0o750},
		"/etc/example/config": {kind: tar.TypeReg, mode: 0o640, digest: fmt.Sprintf("%x", sha256.Sum256([]byte("abc")))},
		"/config-link":        {kind: tar.TypeSymlink, mode: 0o777, link: "/etc/example/config"},
	}, archiveFiles(t, tar.NewReader(&buffer)))
}

// baseFiles exports the final stage's base image from the Dockerfile, so the
// test can tell the base layer apart from state the build leaked in.
func baseFiles(ctx context.Context, t *testing.T, engine, arch string) map[string]entry {
	t.Helper()
	dockerfile, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "packaging", "images", "Dockerfile"))
	require.NoError(t, err)
	var base string
	for line := range strings.SplitSeq(string(dockerfile), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		// Skip FROM flags such as --platform to reach the image reference.
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "--") {
				base = field
				break
			}
		}
	}
	require.NotEmpty(t, base, "no FROM instruction in packaging/images/Dockerfile")

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, engine, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		require.NoError(t, err, "%s %v: %s", engine, args, stderr.String())
		return strings.TrimSpace(string(out))
	}
	// The base is an upstream image rather than the artifact under test, so
	// unlike the image itself it may legitimately be pulled here.
	container := run("create", "--platform", "linux/"+arch, base)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		out, err := exec.CommandContext(cleanupCtx, engine, "rm", "--force", container).CombinedOutput()
		if err != nil {
			t.Errorf("removing base container: %v: %s", err, out)
		}
	})
	export := exec.CommandContext(ctx, engine, "export", container)
	var stderr bytes.Buffer
	export.Stderr = &stderr
	stdout, err := export.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, export.Start())
	files := archiveFiles(t, tar.NewReader(stdout))
	require.NoError(t, export.Wait(), "%s", stderr.String())
	return files
}

func packageFiles(t *testing.T, packageName string) map[string]entry {
	t.Helper()
	dir := os.Getenv("IMAGE_PACKAGES_DIR")
	require.NotEmpty(t, dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(testutil.RepoRoot(t), dir)
	}
	matches, err := filepath.Glob(filepath.Join(dir, packageName+"_*.deb"))
	require.NoError(t, err)
	require.Len(t, matches, 1)
	pkg, closePackage, err := deb.LoadFile(matches[0])
	require.NoError(t, err)
	defer closePackage()
	require.Equal(t, packageName, pkg.Control.Paragraph.Values["Package"])
	return archiveFiles(t, pkg.Data)
}
