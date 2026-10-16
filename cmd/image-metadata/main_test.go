// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-telemetry/opentelemetry-packaging/packaging/images"
	"github.com/stretchr/testify/require"
)

func TestMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, component, pin, version string
		invalid                       bool
	}{
		{name: "injector", component: "injector", pin: "# renovate: datasource=github-releases\n\nv1.2.3\n", version: "1.2.3"},
		{name: "java prerelease", component: "java", pin: "2.0.0-rc.1", version: "2.0.0-rc.1"},
		{name: "missing pin", component: "java", pin: "# comment", invalid: true},
		{name: "multiple pins", component: "java", pin: "v1.0.0\nv2.0.0", invalid: true},
		{name: "unsafe version", component: "java", pin: "1.0.0\ntag=wrong", invalid: true},
		{name: "invalid version", component: "java", pin: "latest", invalid: true},
		{name: "long tag", component: "java", pin: "v1.0.0-" + strings.Repeat("a", 125), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			pinDir := filepath.Join(root, "common", tc.component)
			require.NoError(t, os.MkdirAll(pinDir, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(pinDir, "release.txt"), []byte(tc.pin), 0o644))
			config, err := os.ReadFile("../../packaging/images/config.json")
			require.NoError(t, err)
			require.NoError(t, os.Mkdir(filepath.Join(root, "images"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(root, "images", "config.json"), config, 0o644))
			var out bytes.Buffer
			err = run(&out, root, tc.component, "")
			if tc.invalid {
				require.Error(t, err)
				require.Empty(t, out.String())
				return
			}
			require.NoError(t, err)
			cfg, err := images.Load(root)
			require.NoError(t, err)
			pkg, err := cfg.Package(tc.component)
			require.NoError(t, err)
			require.Equal(t, "version="+tc.version+"\npackage="+pkg.PackageName()+"\n", out.String())
			for field, expected := range map[string]string{
				"version": tc.version,
				"package": pkg.PackageName(),
			} {
				out.Reset()
				require.NoError(t, run(&out, root, tc.component, field))
				require.Equal(t, expected+"\n", out.String())
			}
			require.Error(t, run(&out, root, tc.component, "unknown"))
		})
	}
}

func TestConfigFields(t *testing.T) {
	cfg, err := images.Load("../../packaging")
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, run(&out, "../../packaging", "all", "components"))
	require.Len(t, strings.Fields(out.String()), len(cfg.Packages))
	require.Error(t, run(&out, "../../packaging", "../java", ""))
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func newImageRepo(t *testing.T) func(...string) string {
	t.Helper()
	config, err := os.ReadFile("../../packaging/images/config.json")
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Image tests", "GIT_AUTHOR_EMAIL=images@example.invalid",
			"GIT_COMMITTER_NAME=Image tests", "GIT_COMMITTER_EMAIL=images@example.invalid")
		data, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", data)
		return strings.TrimSpace(string(data))
	}
	git("init", "--quiet")
	writeFixture(t, "packaging/images/config.json", string(config))
	writeFixture(t, "packaging/common/java/release.txt", "v2.32.0\n")
	writeFixture(t, "packaging/common/injector/release.txt", "v0.11.0\n")
	git("add", ".")
	git("update-ref", "HEAD", git("commit-tree", git("write-tree"), "-m", "Image inputs"))
	return git
}

func TestPlanFromNewerRef(t *testing.T) {
	cfg, err := images.Load("../../packaging")
	require.NoError(t, err)
	for _, tc := range []struct {
		name, path string
		want       []images.Package
	}{
		{"unchanged", "", []images.Package{}},
		{"unrelated commit", "README.md", []images.Package{}},
		{"newer injector inputs", "packaging/common/injector/release.txt", []images.Package{{Component: "injector"}}},
		{"newer Java inputs", "packaging/common/java/release.txt", []images.Package{{Component: "java"}}},
		{"newer shared inputs", "packaging/images/Dockerfile", cfg.Packages},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := newImageRepo(t)
			base := git("rev-parse", "HEAD")
			newer := base
			if tc.path != "" {
				writeFixture(t, tc.path, "newer inputs\n")
				git("add", ".")
				newer = git("commit-tree", git("write-tree"), "-p", base, "-m", "Newer main")
				git("reset", "--hard", base)
			}
			git("update-ref", "refs/remotes/origin/main", newer)
			git("fetch", "--no-tags", "--depth=1", ".", "refs/remotes/origin/main")

			var out bytes.Buffer
			require.NoError(t, planFromRef(&out, "packaging", "FETCH_HEAD"))
			outputs := map[string]string{}
			for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
				key, value, ok := strings.Cut(line, "=")
				require.True(t, ok)
				outputs[key] = value
			}
			var superseded []images.Package
			require.NoError(t, json.Unmarshal([]byte(outputs["packages"]), &superseded))
			require.Equal(t, tc.want, superseded)
		})
	}
}

func TestPublication(t *testing.T) {
	const pin = "packaging/common/java/release.txt"
	const dockerfile = "packaging/images/Dockerfile"
	for _, tc := range []struct {
		name    string
		commits []map[string]string
		publish bool
		merge   bool
		invalid bool
	}{
		{name: "unchanged", publish: true},
		{name: "unrelated commit", commits: []map[string]string{{"README.md": "docs"}}, publish: true},
		{name: "different component", commits: []map[string]string{{"packaging/common/injector/release.txt": "v0.12.0"}}, publish: true},
		{name: "same tag with newer component inputs", commits: []map[string]string{{"packaging/common/java/README.md": "new docs"}}, publish: false},
		{name: "same tag with newer shared inputs", commits: []map[string]string{{dockerfile: "new construction"}}, publish: false},
		{name: "same tag without v prefix", commits: []map[string]string{{pin: "2.32.0"}}, publish: false},
		{name: "newer version does not suppress older tag", commits: []map[string]string{{pin: "v2.33.0"}}, publish: true},
		{name: "shared changes for newer tag do not suppress older tag", commits: []map[string]string{{pin: "v2.33.0"}, {dockerfile: "new construction"}}, publish: true},
		{name: "same-tag rebuild before newer version still supersedes", commits: []map[string]string{{dockerfile: "new construction"}, {pin: "v2.33.0"}}, publish: false},
		{name: "unmerged PR history does not supersede", commits: []map[string]string{{dockerfile: "new construction"}, {pin: "v2.33.0"}}, merge: true, publish: true},
		{name: "rollback to same tag with newer inputs", commits: []map[string]string{{pin: "v2.33.0"}, {dockerfile: "new construction"}, {pin: "v2.32.0\n"}}, publish: false},
		{name: "invalid newer pin fails closed", commits: []map[string]string{{pin: "invalid"}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := newImageRepo(t)
			base := git("rev-parse", "HEAD")
			newer := base
			for _, files := range tc.commits {
				for path, content := range files {
					writeFixture(t, path, content)
				}
				git("add", ".")
				newer = git("commit-tree", git("write-tree"), "-p", newer, "-m", "Newer main")
			}
			if tc.merge {
				newer = git("commit-tree", git("write-tree"), "-p", base, "-p", newer, "-m", "Merge PR")
			}
			git("reset", "--hard", base)
			git("update-ref", "refs/remotes/origin/main", newer)
			git("fetch", "--no-tags", ".", "refs/remotes/origin/main")

			var out bytes.Buffer
			err := checkPublication(&out, "packaging", "java", "FETCH_HEAD")
			if tc.invalid {
				require.Error(t, err)
				require.Empty(t, out.String())
				return
			}
			require.NoError(t, err)
			if tc.publish {
				require.Equal(t, "publish=true\n", out.String())
			} else {
				require.Equal(t, "publish=false\n", out.String())
			}
		})
	}
}

func TestPublicationMissingRef(t *testing.T) {
	newImageRepo(t)
	var out bytes.Buffer
	require.Error(t, checkPublication(&out, "packaging", "java", "refs/heads/missing"))
	require.Empty(t, out.String())
}

func TestPlan(t *testing.T) {
	cfg, err := images.Load("../../packaging")
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		changed  []string
		packages int
	}{
		{"all", nil, len(cfg.Packages)},
		{"one pin", []string{"packaging/common/" + cfg.Packages[0].Component + "/release.txt"}, 1},
		{"no changes", []string{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, plan(&out, cfg, tc.changed))
			outputs := map[string]string{}
			for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
				key, value, ok := strings.Cut(line, "=")
				require.True(t, ok)
				outputs[key] = value
			}
			var matrix struct {
				Include []images.Target `json:"include"`
			}
			require.NoError(t, json.Unmarshal([]byte(outputs["matrix"]), &matrix))
			require.Len(t, matrix.Include, tc.packages*len(cfg.Architectures))
			var packages []images.Package
			require.NoError(t, json.Unmarshal([]byte(outputs["packages"]), &packages))
			require.Len(t, packages, tc.packages)
			require.Equal(t, cfg.Platforms(), outputs["platforms"])
			if tc.packages == 0 {
				require.Equal(t, "[]", outputs["packages"])
			}
		})
	}
}
