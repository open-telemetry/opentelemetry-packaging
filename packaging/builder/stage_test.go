// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goreleaser/nfpm/v2/files"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stageFixture stages a component whose payload is written by the test itself,
// so no upstream artifact is downloaded.
func stageFixture(t *testing.T, contents files.Contents) (root string, lines []string) {
	t.Helper()

	root = t.TempDir()
	filelistDir := t.TempDir()

	comp := Component{
		Name:        "fixture",
		PackageName: "opentelemetry-fixture",
		Description: "fixture",
		ContentsFunc: func(Config) (files.Contents, func(), error) {
			return contents, func() {}, nil
		},
	}

	require.NoError(t, Stage(testConfig(t, "1.2.3"), comp, root, filelistDir))

	data, err := os.ReadFile(filepath.Join(filelistDir, "fixture.files"))
	require.NoError(t, err)

	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return root, lines
}

// writeSource creates a payload file the staging can copy from.
func writeSource(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestStageCopiesPayloadWithPackageMode(t *testing.T) {
	src := writeSource(t, "libotelinject.so", "payload")

	root, lines := stageFixture(t, files.Contents{
		regularFile(src, injectorInstallDir+"/libotelinject.so", 0o755),
	})

	staged := filepath.Join(root, injectorInstallDir, "libotelinject.so")
	info, err := os.Stat(staged)
	require.NoError(t, err)
	// The source is 0600: the mode declared for the package is what must land.
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	body, err := os.ReadFile(staged)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(body))

	assert.Contains(t, lines, "%attr(0755,root,root) "+injectorInstallDir+"/libotelinject.so")
}

func TestStageMarksConfigFilesNoReplace(t *testing.T) {
	src := writeSource(t, "injector.conf", "key=value\n")

	_, lines := stageFixture(t, files.Contents{
		configFile(src, injectorConfigDir+"/injector.conf"),
	})

	assert.Contains(t, lines,
		"%config(noreplace) %attr(0644,root,root) "+injectorConfigDir+"/injector.conf")
}

// The project must declare its own directories, and must not claim any that
// belong to the distribution.
func TestStageOwnsOnlyItsOwnDirectories(t *testing.T) {
	src := writeSource(t, "README.md", "docs\n")

	_, lines := stageFixture(t, files.Contents{
		regularFile(src, javaInstallDir+"/opentelemetry-javaagent.jar", 0o644),
		regularFile(src, "/usr/share/man/man8/opentelemetry-java.8.gz", 0o644),
		regularFile(src, "/usr/share/doc/opentelemetry-java-autoinstrumentation/README.md", 0o644),
		directory(injectorConfigDir + "/conf.d"),
	})

	dirs := map[string]bool{}
	for _, line := range lines {
		if after, ok := strings.CutPrefix(line, "%dir "); ok {
			dirs[after[strings.LastIndex(after, " ")+1:]] = true
		}
	}

	// Owned: the installation, configuration, and per-package documentation trees.
	assert.True(t, dirs[installDir], "the install root must be owned")
	assert.True(t, dirs[javaInstallDir])
	assert.True(t, dirs[configDir])
	assert.True(t, dirs[injectorConfigDir+"/conf.d"])
	assert.True(t, dirs["/usr/share/doc/opentelemetry-java-autoinstrumentation"])

	// Distribution-owned: claiming these would conflict with filesystem or man-db.
	for _, distroDir := range []string{
		"/usr", "/usr/lib", "/etc", "/usr/share", "/usr/share/doc",
		"/usr/share/man", "/usr/share/man/man8",
	} {
		assert.False(t, dirs[distroDir], "%s belongs to the distribution and must not be owned", distroDir)
	}
}

func TestIsOwnedDir(t *testing.T) {
	owned := []string{
		installDir,
		installDir + "/",
		installDir + "/injector",
		configDir + "/injector/conf.d/",
		"/usr/share/doc/opentelemetry",
		"/usr/share/doc/opentelemetry-python-autoinstrumentation/",
	}
	for _, dir := range owned {
		assert.True(t, isOwnedDir(dir), "%s should be owned", dir)
	}

	distro := []string{
		"/", "/usr", "/usr/lib", "/usr/lib/", "/etc", "/usr/share",
		"/usr/share/doc", "/usr/share/doc/", "/usr/share/man/man8",
		"/usr/lib/systemd",
	}
	for _, dir := range distro {
		assert.False(t, isOwnedDir(dir), "%s should not be owned", dir)
	}
}

// The staged tree and the file list come from one traversal, so rpmbuild's
// unpackaged-files check verifies the builder rather than a hand-kept list.
func TestStageFileListCoversEveryStagedFile(t *testing.T) {
	payloadDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(payloadDir, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(payloadDir, "top.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(payloadDir, "nested", "deep.txt"), []byte("b"), 0o644))

	src := writeSource(t, "otel-config.yaml", "config\n")

	root, lines := stageFixture(t, files.Contents{
		tree(payloadDir, dotnetInstallDir),
		configFile(src, dotnetConfigDir+"/otel-config.yaml"),
	})

	listed := map[string]bool{}
	for _, line := range lines {
		listed[line[strings.LastIndex(line, " ")+1:]] = true
	}

	var staged []string
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		staged = append(staged, "/"+filepath.ToSlash(rel))
		return nil
	}))

	require.NotEmpty(t, staged)
	for _, path := range staged {
		assert.True(t, listed[path], "staged file %s is missing from the file list", path)
	}
	// The tree expanded into its individual files.
	assert.True(t, listed[dotnetInstallDir+"/nested/deep.txt"])
}

func TestStageRejectsUnsupportedContentType(t *testing.T) {
	comp := Component{
		Name:        "fixture",
		PackageName: "opentelemetry-fixture",
		ContentsFunc: func(Config) (files.Contents, func(), error) {
			return files.Contents{
				{Source: "/dev/null", Destination: installDir + "/ghost", Type: files.TypeRPMGhost},
			}, func() {}, nil
		},
	}

	err := Stage(testConfig(t, "1.2.3"), comp, t.TempDir(), t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content type")
}

func TestFilelistPath(t *testing.T) {
	assert.Equal(t, "/usr/lib/opentelemetry", filelistPath("/usr/lib/opentelemetry/"))
	assert.Equal(t, "/etc/opentelemetry/injector.conf", filelistPath("/etc/opentelemetry/injector.conf"))
	// A literal percent sign would otherwise be read as a macro, and whitespace
	// would split one path into several entries.
	assert.Equal(t, "/usr/lib/opentelemetry/100%%", filelistPath("/usr/lib/opentelemetry/100%"))
	assert.Equal(t, `"/usr/lib/opentelemetry/a b"`, filelistPath("/usr/lib/opentelemetry/a b"))
}
