// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writePyproject(t *testing.T, dir, name, body string) string {
	t.Helper()
	project := filepath.Join(dir, name)
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "pyproject.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return project
}

func TestCheckSourceRequiresPython(t *testing.T) {
	dir := t.TempDir()
	admitsAll := writePyproject(t, dir, "admits-all", "[project]\nname = \"a\"\nrequires-python = \">=3.10\"\n")
	raised := writePyproject(t, dir, "raised", "[project]\nname = \"b\"\nrequires-python = \">=3.12\"\n")
	undeclared := writePyproject(t, dir, "undeclared", "[project]\nname = \"c\"\n")
	gitReq := "git+https://example.com/repo.git"

	problems, err := checkSourceRequiresPython(
		[]string{admitsAll, raised, undeclared, gitReq},
		[]string{"3.10", "3.11", "3.12", "3.13"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		`Python 3.10: raised requires-python is ">=3.12"`,
		`Python 3.11: raised requires-python is ">=3.12"`,
	}
	if !reflect.DeepEqual(problems, want) {
		t.Errorf("problems = %q, want %q", problems, want)
	}
}

func TestCheckSourceRequiresPythonRejectsUnknownSpecifier(t *testing.T) {
	project := writePyproject(t, t.TempDir(), "excludes", "[project]\nrequires-python = \">=3.9,!=3.10.*\"\n")

	_, err := checkSourceRequiresPython([]string{project}, []string{"3.10"})
	if err == nil || !strings.Contains(err.Error(), `">=3.9,!=3.10.*"`) {
		t.Errorf("err = %v, want an error naming the specifier", err)
	}
}

func TestCheckSourceRequiresPythonAcceptsVendoredPackages(t *testing.T) {
	_, sourceReqs, err := splitRequirements(filepath.Join("..", "common", "python", "requirements.txt"))
	if err != nil {
		t.Fatal(err)
	}
	problems, err := checkSourceRequiresPython(sourceReqs, SupportedPythonVersions)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Errorf("vendored packages exclude supported Python versions:\n%s", strings.Join(problems, "\n"))
	}
}
