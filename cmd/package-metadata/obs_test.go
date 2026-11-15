// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// obsRepo is a git repository holding the version pins of every component,
// whose commits carry the times the tests give them.
type obsRepo struct {
	t *testing.T
}

func newOBSRepo(t *testing.T) obsRepo {
	t.Helper()
	t.Chdir(t.TempDir())
	r := obsRepo{t}
	r.git("", "init", "--quiet")
	writeFixture(t, "packaging/common/injector/release.txt", "v0.11.0\n")
	writeFixture(t, "packaging/common/java/release.txt", "v2.32.0\n")
	writeFixture(t, "packaging/common/nodejs/release.txt", "0.81.1\n")
	writeFixture(t, "packaging/common/dotnet/release.txt", "v1.18.0\n")
	writeFixture(t, "packaging/common/python/requirements.txt", "opentelemetry-distro==0.66b2\n")
	writeFixture(t, "packaging/common/meta/release.txt", "0.0.3\n")
	writeFixture(t, "packaging/builder/components.go", "package builder\n")
	r.commit("1000000000", "Package inputs")
	return r
}

func (r obsRepo) git(date string, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=OBS tests", "GIT_AUTHOR_EMAIL=obs@example.invalid",
		"GIT_COMMITTER_NAME=OBS tests", "GIT_COMMITTER_EMAIL=obs@example.invalid")
	if date != "" {
		cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE=@"+date+" +0000", "GIT_COMMITTER_DATE=@"+date+" +0000")
	}
	data, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "%s", data)
	return strings.TrimSpace(string(data))
}

// commit records the working tree at the given time and returns the commit.
func (r obsRepo) commit(date, message string) string {
	r.t.Helper()
	r.git("", "add", ".")
	r.git(date, "commit", "--quiet", "-m", message)
	return r.git("", "rev-parse", "HEAD")
}

func runOBSPlan(t *testing.T, base, selection string) []obsPackage {
	t.Helper()
	var out bytes.Buffer
	require.NoError(t, obsPlan(&out, "packaging", base, selection))
	value, ok := strings.CutPrefix(strings.TrimSpace(out.String()), "packages=")
	require.True(t, ok, out.String())
	var packages []obsPackage
	require.NoError(t, json.Unmarshal([]byte(value), &packages))
	return packages
}

func components(packages []obsPackage) []string {
	names := make([]string, 0, len(packages))
	for _, pkg := range packages {
		names = append(names, pkg.Component)
	}
	return names
}

func TestOBSPlanVersionsEachPackageByItsPin(t *testing.T) {
	newOBSRepo(t)
	require.Equal(t, []obsPackage{
		{Component: "injector", Package: "opentelemetry-injector", Version: "0.11.0", SourceDateEpoch: "1000000000"},
		{Component: "java", Package: "opentelemetry-java-autoinstrumentation", Version: "2.32.0", SourceDateEpoch: "1000000000"},
		{Component: "nodejs", Package: "opentelemetry-nodejs-autoinstrumentation", Version: "0.81.1", SourceDateEpoch: "1000000000"},
		{Component: "dotnet", Package: "opentelemetry-dotnet-autoinstrumentation", Version: "1.18.0", SourceDateEpoch: "1000000000"},
		{Component: "python", Package: "opentelemetry-python-autoinstrumentation", Version: "0.66b2", SourceDateEpoch: "1000000000"},
		{Component: "meta", Package: "opentelemetry", Version: "0.0.3", SourceDateEpoch: "1000000000"},
	}, runOBSPlan(t, "", ""))
}

// A component's source date moves only with its own inputs, so regenerating an
// untouched component reproduces its last upload byte for byte.
func TestOBSPlanSourceDateFollowsTheComponentsInputs(t *testing.T) {
	repo := newOBSRepo(t)
	writeFixture(t, "packaging/common/java/release.txt", "v2.33.0\n")
	repo.commit("1100000000", "Bump Java")
	writeFixture(t, "README.md", "unrelated\n")
	repo.commit("1200000000", "Unrelated")

	epochs := map[string]string{}
	for _, pkg := range runOBSPlan(t, "", "all") {
		epochs[pkg.Component] = pkg.SourceDateEpoch
	}
	require.Equal(t, "1100000000", epochs["java"])
	require.Equal(t, "1000000000", epochs["injector"])

	writeFixture(t, "packaging/builder/components.go", "package builder // changed\n")
	repo.commit("1300000000", "Shared change")
	for _, pkg := range runOBSPlan(t, "", "all") {
		require.Equal(t, "1300000000", pkg.SourceDateEpoch, pkg.Component)
	}
}

func TestOBSPlanSelectsChangesSinceBase(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		want       []string
	}{
		{"unrelated", "README.md", []string{}},
		{"one pin", "packaging/common/java/release.txt", []string{"java"}},
		{"python requirements", "packaging/common/python/requirements.txt", []string{"python"}},
		{"metapackage", "packaging/common/meta/release.txt", []string{"meta"}},
		{"component config", "packaging/common/injector/otel-injector.conf", []string{"injector"}},
		{"builder", "packaging/builder/debspec.go", []string{"injector", "java", "nodejs", "dotnet", "python", "meta"}},
		{"DEB source generator", "cmd/build-spec/deb/main.go", []string{"injector", "java", "nodejs", "dotnet", "python", "meta"}},
		// The workflow and the tools around it decide what is uploaded, not
		// what it contains.
		{"workflow", ".github/workflows/obs.yml", []string{}},
		{"planner", "cmd/package-metadata/obs.go", []string{}},
		{"images", "packaging/images/config.json", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newOBSRepo(t)
			base := repo.git("", "rev-parse", "HEAD")
			// The pins must stay parseable: the plan reads every version.
			content := "changed\n"
			switch {
			case strings.HasSuffix(tc.path, "release.txt"):
				content = "9.9.9\n"
			case strings.HasSuffix(tc.path, "requirements.txt"):
				content = "opentelemetry-distro==0.67b0\n"
			}
			writeFixture(t, tc.path, content)
			repo.commit("1100000000", "Change")
			require.Equal(t, tc.want, components(runOBSPlan(t, base, "")))
		})
	}
}

func TestOBSPlanSelectionWinsOverBase(t *testing.T) {
	repo := newOBSRepo(t)
	base := repo.git("", "rev-parse", "HEAD")
	writeFixture(t, "packaging/common/java/release.txt", "v2.33.0\n")
	repo.commit("1100000000", "Bump Java")
	require.Equal(t, []string{"meta", "injector"}, components(runOBSPlan(t, base, " meta, injector ")))
}

func TestOBSPlanRejectsBadSelections(t *testing.T) {
	newOBSRepo(t)
	for _, selection := range []string{"bogus", "java,java", "java,"} {
		t.Run(selection, func(t *testing.T) {
			require.Error(t, obsPlan(&bytes.Buffer{}, "packaging", "", selection))
		})
	}
}
