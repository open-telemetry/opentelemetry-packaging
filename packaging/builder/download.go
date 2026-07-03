// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/zip"
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// npmTimeout is the maximum duration for a single npm subprocess.
const npmTimeout = 10 * time.Minute

// httpClient is used for all artifact downloads. It sets a generous timeout
// to avoid hanging indefinitely on stalled upstream responses.
var httpClient = &http.Client{Timeout: 10 * time.Minute}

// readReleaseVersion reads a pinned version from a release file.
// Lines starting with "#" and blank lines are skipped.
// A leading "v" is NOT stripped (callers decide).
func readReleaseVersion(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var version string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		version = line
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if version == "" {
		return "", fmt.Errorf("no version found in %s", path)
	}
	return version, nil
}

// downloadFile fetches a URL and writes it to dest. On any error after the
// file is created, the partial file is removed.
func downloadFile(url, dest string) (retErr error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	fmt.Printf("  Downloading %s\n", url)
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := f.Close()
		if retErr == nil {
			retErr = closeErr
		}
		if retErr != nil {
			os.Remove(dest)
		}
	}()
	_, retErr = io.Copy(f, resp.Body)
	return retErr
}

// downloadInjector fetches libotelinject.so from GitHub releases.
func downloadInjector(cfg Config, dest string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "injector", "release.txt"))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/open-telemetry/opentelemetry-injector/releases/download/%s/libotelinject_%s.so", tag, cfg.Arch)
	return downloadFile(url, dest)
}

// downloadJavaAgent fetches the Java agent JAR from GitHub releases.
func downloadJavaAgent(cfg Config, dest string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "java", "release.txt"))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/download/%s/opentelemetry-javaagent.jar", tag)
	return downloadFile(url, dest)
}

// downloadNodejsAgent fetches the Node.js auto-instrumentation from npm.
// This shells out to npm because the npm registry protocol and package
// installation logic (with native dependencies) is non-trivial.
func downloadNodejsAgent(cfg Config, destDir string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "nodejs", "release.txt"))
	if err != nil {
		return err
	}
	ver := strings.TrimPrefix(tag, "v")

	nodejsDir := filepath.Join(destDir, "nodejs")
	if err := os.MkdirAll(nodejsDir, 0o755); err != nil {
		return err
	}

	fmt.Printf("  Installing @opentelemetry/auto-instrumentations-node@%s via npm\n", ver)

	npmEnv := append(os.Environ(), "NPM_CONFIG_UPDATE_NOTIFIER=false")

	// npm pack + npm install to get a clean node_modules tree.
	// Both commands use a context timeout to avoid hanging on a stuck registry.
	packCtx, packCancel := context.WithTimeout(context.Background(), npmTimeout)
	defer packCancel()

	packCmd := exec.CommandContext(packCtx, "npm", "--loglevel=warn", "pack",
		fmt.Sprintf("@opentelemetry/auto-instrumentations-node@%s", ver))
	packCmd.Dir = nodejsDir
	packCmd.Env = npmEnv
	if out, err := packCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm pack failed: %s\n%w", string(out), err)
	}

	// Find the tarball (npm pack outputs the filename).
	tgzMatches, _ := filepath.Glob(filepath.Join(nodejsDir, "*.tgz"))
	if len(tgzMatches) == 0 {
		return fmt.Errorf("npm pack did not produce a .tgz in %s", nodejsDir)
	}
	tgz := tgzMatches[0]

	installCtx, installCancel := context.WithTimeout(context.Background(), npmTimeout)
	defer installCancel()

	installCmd := exec.CommandContext(installCtx, "npm", "--loglevel=warn", "--no-fund",
		"install", "--ignore-scripts", "--global=false", filepath.Base(tgz))
	installCmd.Dir = nodejsDir
	installCmd.Env = npmEnv
	if out, err := installCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm install failed: %s\n%w", string(out), err)
	}

	os.Remove(tgz)
	return nil
}

// downloadDotnetAgent fetches the .NET auto-instrumentation (glibc) and extracts
// it under a glibc/ prefix, matching the layout the OpenTelemetry injector
// expects (<prefix>/<libc>). Only glibc is bundled: musl-based distros (Alpine)
// use apk, which this project does not build, so the injector never resolves a
// musl/ path on any supported (deb/rpm) target.
func downloadDotnetAgent(cfg Config, destDir string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "dotnet", "release.txt"))
	if err != nil {
		return err
	}

	var dotnetArch string
	switch cfg.Arch {
	case "amd64":
		dotnetArch = "x64"
	case "arm64":
		dotnetArch = "arm64"
	default:
		return fmt.Errorf("unsupported architecture for .NET: %s", cfg.Arch)
	}

	baseURL := "https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation/releases/download"

	// Download and extract glibc archive into a glibc/ subdirectory.
	// The injector expects all glibc files (managed DLLs and native library)
	// under <prefix>/glibc/.
	glibcPkg := fmt.Sprintf("opentelemetry-dotnet-instrumentation-linux-glibc-%s.zip", dotnetArch)
	glibcURL := fmt.Sprintf("%s/%s/%s", baseURL, tag, glibcPkg)
	glibcZip, err := os.CreateTemp("", "otel-dotnet-glibc-*.zip")
	if err != nil {
		return err
	}
	glibcZip.Close()
	glibcZipPath := glibcZip.Name()
	defer os.Remove(glibcZipPath)
	if err := downloadFile(glibcURL, glibcZipPath); err != nil {
		return err
	}
	glibcDest := filepath.Join(destDir, "glibc")
	if err := os.MkdirAll(glibcDest, 0o755); err != nil {
		return fmt.Errorf("creating glibc dir: %w", err)
	}
	if err := extractZip(glibcZipPath, glibcDest); err != nil {
		return fmt.Errorf("extracting glibc archive: %w", err)
	}

	return nil
}

// extractZip extracts all files from a zip archive into destDir.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if err := extractZipFile(f, destDir); err != nil {
			return err
		}
	}
	return nil
}

func extractZipFile(f *zip.File, destDir string) error {
	target := filepath.Join(destDir, f.Name)

	// Prevent zip slip.
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
		return fmt.Errorf("illegal zip entry path: %s", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, f.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

// pipTimeout is the maximum duration for a pip subprocess.
const pipTimeout = 10 * time.Minute

// Target Python interpreter the bundled wheels are fetched for. The bundle
// contains version-specific compiled C extensions (e.g. wrapt), so it is tied
// to one Python minor version; consumers must run this interpreter version.
// Keep this in sync with the Python version used by the integration tests
// (packaging/tests/{deb,rpm}/python/Dockerfile).
const (
	targetPythonVersion = "3.11"
	targetPythonABI     = "cp311"
)

// pythonExecutable returns the Python interpreter used to drive pip. It prefers
// "python3" and falls back to "python". The interpreter only runs pip itself;
// the target Python version for the bundled wheels is pinned independently via
// pip's --python-version/--abi flags, so the host interpreter version is
// irrelevant to the produced package.
func pythonExecutable() string {
	if path, err := exec.LookPath("python3"); err == nil {
		return path
	}
	return "python"
}

// manylinuxPlatforms returns the manylinux platform tags pip should accept for
// the given target architecture. We avoid host-platform wheels entirely so the
// produced package is correct regardless of the build host's OS/arch.
func manylinuxPlatforms(arch string) ([]string, error) {
	var machine string
	switch arch {
	case "amd64":
		machine = "x86_64"
	case "arm64":
		machine = "aarch64"
	default:
		return nil, fmt.Errorf("unsupported architecture for Python: %s", arch)
	}
	return []string{
		"manylinux2014_" + machine,
		"manylinux_2_17_" + machine,
		"manylinux_2_28_" + machine,
		"manylinux1_" + machine,
	}, nil
}

// splitRequirements reads a pip requirements file and partitions its entries
// into PyPI requirements and VCS (git+) requirements. Comments and blank lines
// are skipped. VCS requirements (e.g. unpublished packages installed from a git
// branch) must be built from source and therefore cannot be fetched with pip's
// cross-platform binary-only download; they are installed in a separate pass.
func splitRequirements(requirementsFile string) (pypi, vcs []string, err error) {
	data, err := os.ReadFile(requirementsFile)
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "git+") {
			vcs = append(vcs, trimmed)
		} else {
			pypi = append(pypi, trimmed)
		}
	}
	return pypi, vcs, nil
}

// downloadPythonAgent installs Python auto-instrumentation packages into destDir.
// The packages are defined by packaging/common/python/requirements.txt.
//
// Installation happens in two passes so the resulting package is correct for the
// target Linux architecture and Python version regardless of the build host:
//
//  1. PyPI requirements are installed binary-only, pinned to manylinux wheels for
//     the target arch and to the target Python version/ABI. This prevents the
//     build host's OS (e.g. macOS) and Python version from leaking compiled
//     extensions into the package.
//  2. VCS requirements (unpublished pure-Python packages on a git branch) are
//     built from source with --no-deps into a separate directory, then merged in.
//     Their dependencies must therefore be present among the PyPI requirements.
//
// When all requirements are published to PyPI (no git+ entries), pass 2 is a
// no-op and pass 1 installs everything.
func downloadPythonAgent(cfg Config, destDir string) error {
	requirementsFile := filepath.Join(cfg.PackagingDir, "common", "python", "requirements.txt")

	pypiReqs, vcsReqs, err := splitRequirements(requirementsFile)
	if err != nil {
		return fmt.Errorf("reading requirements: %w", err)
	}

	platforms, err := manylinuxPlatforms(cfg.Arch)
	if err != nil {
		return err
	}

	python := pythonExecutable()

	// Pass 1: PyPI requirements as cross-platform manylinux wheels.
	fmt.Printf("  Installing Python OTel packages (PyPI, linux/%s, py%s) into %s\n", cfg.Arch, targetPythonVersion, destDir)

	pypiReqFile := filepath.Join(filepath.Dir(destDir), "requirements-pypi.txt")
	if err := os.WriteFile(pypiReqFile, []byte(strings.Join(pypiReqs, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing PyPI requirements file: %w", err)
	}

	pass1Args := []string{
		"-m", "pip", "install",
		"--target", destDir,
		"--no-compile",
		"--quiet",
		"--only-binary=:all:",
		"--python-version", targetPythonVersion,
		"--implementation", "cp",
		"--abi", targetPythonABI,
		"--abi", "abi3",
		"--abi", "none",
	}
	for _, p := range platforms {
		pass1Args = append(pass1Args, "--platform", p)
	}
	pass1Args = append(pass1Args, "-r", pypiReqFile)

	if err := runPip(python, pass1Args); err != nil {
		return err
	}

	// Pass 2: VCS requirements built from source (pure-Python, host-agnostic).
	if len(vcsReqs) > 0 {
		fmt.Printf("  Installing %d Python OTel package(s) from VCS sources\n", len(vcsReqs))

		vcsDir, err := os.MkdirTemp("", "otel-python-vcs-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(vcsDir)

		pass2Args := []string{
			"-m", "pip", "install",
			"--target", vcsDir,
			"--no-compile",
			"--quiet",
			"--no-deps",
		}
		pass2Args = append(pass2Args, vcsReqs...)

		if err := runPip(python, pass2Args); err != nil {
			return err
		}

		// Merge the VCS packages into the main bundle. The pyproto packages add
		// new paths under the opentelemetry/ namespace and new dist-info dirs,
		// so this is a pure overlay with no file collisions.
		if err := mergeTree(vcsDir, destDir); err != nil {
			return fmt.Errorf("merging VCS packages: %w", err)
		}
	}

	return nil
}

// runPip runs "python -m pip ..." with a timeout and returns a descriptive error
// on failure.
func runPip(python string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pipTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Env = append(os.Environ(), "PIP_DISABLE_PIP_VERSION_CHECK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pip install failed: %s\n%w", string(out), err)
	}
	return nil
}

// mergeTree recursively copies the contents of src into dst, creating
// directories as needed and overwriting existing files.
func mergeTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target)
	})
}

// copyFile copies src to dst, creating dst with the same permissions as src.
func copyFile(src, dst string) (retErr error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() {
		closeErr := out.Close()
		if retErr == nil {
			retErr = closeErr
		}
	}()

	_, retErr = io.Copy(out, in)
	return retErr
}

// generateAllDependencies walks installDir for *.dist-info/METADATA files, parses the
// Name and Version fields, and writes a sorted list of "name==version" requirement
// strings to outputPath. sitecustomize.py reads this file at runtime to detect version
// conflicts between the bundled packages and the application's own dependencies.
func generateAllDependencies(installDir, outputPath string) error {
	entries, err := os.ReadDir(installDir)
	if err != nil {
		return err
	}

	var lines []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), ".dist-info") {
			continue
		}
		metadataPath := filepath.Join(installDir, entry.Name(), "METADATA")
		data, err := os.ReadFile(metadataPath)
		if err != nil {
			continue
		}
		name, version := parseMetadata(string(data))
		if name != "" && version != "" {
			lines = append(lines, fmt.Sprintf("%s==%s", name, version))
		}
	}

	sort.Strings(lines)
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}
	return os.WriteFile(outputPath, []byte(content), 0o644)
}

// parseMetadata extracts the Name and Version from a PEP 566 METADATA file (RFC 822 format).
func parseMetadata(data string) (name, version string) {
	for _, line := range strings.Split(data, "\n") {
		if name != "" && version != "" {
			break
		}
		if rest, ok := strings.CutPrefix(line, "Name: "); ok {
			name = strings.TrimSpace(rest)
		} else if rest, ok := strings.CutPrefix(line, "Version: "); ok {
			version = strings.TrimSpace(rest)
		}
	}
	return name, version
}
