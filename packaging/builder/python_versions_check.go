// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CheckSupportedPythonVersions returns one problem per supported Python
// version the package can no longer be built for, so that a dependency bump
// that drops an interpreter fails with a message naming
// supported_python_versions.json instead of a pip error deep in a package build.
//
// A version is a problem when pip cannot resolve the PyPI requirements for it
// on one of archs, using the same flags downloadPythonAgent uses, or when the
// requires-python of a vendored source requirement excludes it. Resolution is a
// pip --dry-run, so nothing is downloaded beyond the metadata pip needs.
func CheckSupportedPythonVersions(packagingDir string, archs []string) ([]string, error) {
	requirementsFile := filepath.Join(packagingDir, "common", "python", "requirements.txt")
	pypiReqs, sourceReqs, err := splitRequirements(requirementsFile)
	if err != nil {
		return nil, fmt.Errorf("reading requirements: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "otel-python-versions-check-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	pypiReqFile := filepath.Join(tmpDir, "requirements-pypi.txt")
	if err := os.WriteFile(pypiReqFile, []byte(strings.Join(pypiReqs, "\n")+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("writing PyPI requirements file: %w", err)
	}

	problems, err := checkSourceRequiresPython(sourceReqs, SupportedPythonVersions)
	if err != nil {
		return nil, err
	}

	python := pythonExecutable()
	for _, arch := range archs {
		platforms, err := manylinuxPlatforms(arch)
		if err != nil {
			return nil, err
		}
		for _, pythonVersion := range SupportedPythonVersions {
			fmt.Printf("  Resolving Python requirements (linux/%s, py%s)\n", arch, pythonVersion)
			args := []string{
				"-m", "pip", "install",
				"--dry-run", "--ignore-installed", "--quiet",
				"--target", filepath.Join(tmpDir, "target"),
			}
			args = append(args, pipPlatformArgs(pythonVersion, platforms)...)
			args = append(args, "-r", pypiReqFile)
			if err := runPip(python, args); err != nil {
				problems = append(problems, fmt.Sprintf(
					"Python %s: pip cannot resolve requirements.txt for linux/%s: %v",
					pythonVersion, arch, err))
			}
		}
	}
	return problems, nil
}

var requiresPythonPattern = regexp.MustCompile(`(?m)^requires-python\s*=\s*"([^"]*)"`)

var requiresPythonFloorPattern = regexp.MustCompile(`^>=\s*3\.([0-9]+)$`)

// checkSourceRequiresPython returns one problem per supported Python version
// that a source requirement's pyproject.toml excludes. The builder installs
// source requirements with --no-deps from the host interpreter, so pip never
// checks their requires-python against the target versions on its own. Only
// the ">=3.N" form is understood; anything else is an error, so that a
// specifier this check cannot evaluate is never silently accepted.
func checkSourceRequiresPython(sourceReqs, pythonVersions []string) ([]string, error) {
	var problems []string
	for _, req := range sourceReqs {
		pyproject := filepath.Join(req, "pyproject.toml")
		data, err := os.ReadFile(pyproject)
		if os.IsNotExist(err) {
			// git+ requirements have no local pyproject.toml to read.
			continue
		}
		if err != nil {
			return nil, err
		}
		match := requiresPythonPattern.FindSubmatch(data)
		if match == nil {
			continue
		}
		specifier := strings.TrimSpace(string(match[1]))
		floor := requiresPythonFloorPattern.FindStringSubmatch(specifier)
		if floor == nil {
			return nil, fmt.Errorf(
				"%s: requires-python %q is not of the form \">=3.N\" this check understands",
				pyproject, specifier)
		}
		floorMinor, _ := strconv.Atoi(floor[1])
		for _, pythonVersion := range pythonVersions {
			minor, err := strconv.Atoi(strings.TrimPrefix(pythonVersion, "3."))
			if err != nil {
				return nil, fmt.Errorf("unsupported Python version %q, want \"3.N\"", pythonVersion)
			}
			if minor < floorMinor {
				problems = append(problems, fmt.Sprintf(
					"Python %s: %s requires-python is %q",
					pythonVersion, filepath.Base(req), specifier))
			}
		}
	}
	return problems, nil
}
