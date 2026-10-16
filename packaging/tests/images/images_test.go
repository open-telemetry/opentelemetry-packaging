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
	command := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, engine, args...).CombinedOutput()
		require.NoError(t, err, "%s %v: %s", engine, args, out)
		return strings.TrimSpace(string(out))
	}

	expected := packageFiles(t, pkg.PackageName())
	container := command("create", "--platform", "linux/"+arch, image)
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
	for name := range actual {
		for _, excluded := range []string{"/etc/ld.so.preload", "/var/lib/dpkg", "/tmp/package.deb"} {
			require.False(t, name == excluded || strings.HasPrefix(name, excluded+"/"), "unexpected installation state %s", name)
		}
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
