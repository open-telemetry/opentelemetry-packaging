// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
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

// fetchChecksums downloads a sha256sum(1)-style manifest (lines of
// "<hex digest>  <filename>", optionally with a "*" binary-mode marker before
// the filename) and returns it as a map from filename to lowercase hex digest.
func fetchChecksums(url string) (map[string]string, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}

	sums := make(map[string]string)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		digest := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		sums[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}
	return sums, nil
}

// verifyFileSHA256 hashes the file at path and returns an error if it does
// not match the expected lowercase hex digest.
func verifyFileSHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing %s: %w", path, err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != strings.ToLower(want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", path, got, want)
	}
	return nil
}

// fetchBytes downloads a URL and returns its full body.
func fetchBytes(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// verifyDetachedSignature checks that sigData is a valid, armored OpenPGP
// detached signature over the file at path, made by the key in the armored
// keyring at keyringPath, and that the signing key's fingerprint matches
// wantFingerprint (guards against the keyring file being swapped for a
// different, still-technically-valid key).
func verifyDetachedSignature(path, keyringPath, wantFingerprint string, sigData []byte) error {
	keyringFile, err := os.Open(keyringPath)
	if err != nil {
		return err
	}
	defer keyringFile.Close()

	keyring, err := openpgp.ReadArmoredKeyRing(keyringFile)
	if err != nil {
		return fmt.Errorf("reading keyring %s: %w", keyringPath, err)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	signer, err := openpgp.CheckArmoredDetachedSignature(keyring, f, bytes.NewReader(sigData), nil)
	if err != nil {
		return fmt.Errorf("verifying signature for %s: %w", path, err)
	}

	fingerprint := strings.ToUpper(hex.EncodeToString(signer.PrimaryKey.Fingerprint))
	if fingerprint != wantFingerprint {
		return fmt.Errorf("signature for %s was made by unexpected key %s (want %s)", path, fingerprint, wantFingerprint)
	}
	return nil
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

// javaReleaseKeyFingerprint is the OpenTelemetry Java release-signing key
// pinned in packaging/common/java/release-key.asc (RSA 2048, UID "OpenTelemetry
// Java", no expiration or revocation set as of pinning).
//
// The project does not publish this fingerprint through any OpenTelemetry-
// controlled channel (no KEYS file, no docs page); it is only discoverable on
// public keyservers because Maven Central's Sonatype Central Portal requires
// keyserver presence before accepting signed uploads. Trust here is
// trust-on-first-use: this fingerprint was confirmed to have signed
// opentelemetry-javaagent.jar consistently across releases spanning roughly
// two years (v2.10.0, v2.20.1, v2.30.0) before being pinned.
const javaReleaseKeyFingerprint = "3F05DDA9F317301E927136D417A27CE7A60FF5F0"

// downloadJavaAgent fetches the Java agent JAR from GitHub releases and
// verifies it against the detached GPG signature ("*.jar.asc") the release
// also publishes, checked against the pinned key above.
func downloadJavaAgent(cfg Config, dest string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "java", "release.txt"))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/download/%s/opentelemetry-javaagent.jar", tag)
	if err := downloadFile(url, dest); err != nil {
		return err
	}

	sigData, err := fetchBytes(url + ".asc")
	if err != nil {
		return fmt.Errorf("fetching signature: %w", err)
	}

	keyringPath := filepath.Join(cfg.PackagingDir, "common", "java", "release-key.asc")
	if err := verifyDetachedSignature(dest, keyringPath, javaReleaseKeyFingerprint, sigData); err != nil {
		return err
	}

	return nil
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

	// Every release publishes a checksums.txt alongside the archives; verify
	// the downloaded artifact against it before extracting.
	checksumsURL := fmt.Sprintf("%s/%s/checksums.txt", baseURL, tag)
	checksums, err := fetchChecksums(checksumsURL)
	if err != nil {
		return fmt.Errorf("fetching checksums: %w", err)
	}

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
	want, ok := checksums[glibcPkg]
	if !ok {
		return fmt.Errorf("no checksum published for %s in %s", glibcPkg, checksumsURL)
	}
	if err := verifyFileSHA256(glibcZipPath, want); err != nil {
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

func extractZipFile(f *zip.File, destDir string) (retErr error) {
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
	// Mirror downloadFile: never leave a partially-written file behind on a
	// failed copy or close (disk full, I/O error).
	defer func() {
		closeErr := out.Close()
		if retErr == nil {
			retErr = closeErr
		}
		if retErr != nil {
			os.Remove(target)
		}
	}()

	_, retErr = io.Copy(out, rc)
	return retErr
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
// into PyPI requirements and source requirements. Comments and blank lines are
// skipped. Source requirements — VCS (git+) entries and local paths (./…,
// e.g. the packages vendored under vendor/) — must be built from source and
// therefore cannot be fetched with pip's cross-platform binary-only download;
// they are installed in a separate pass. Local paths are returned as absolute
// paths (pip would otherwise resolve them against the process working
// directory, not the requirements file).
func splitRequirements(requirementsFile string) (pypi, source []string, err error) {
	data, err := os.ReadFile(requirementsFile)
	if err != nil {
		return nil, nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		switch {
		case strings.Contains(trimmed, "git+"):
			source = append(source, trimmed)
		case strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../"):
			source = append(source, filepath.Join(filepath.Dir(requirementsFile), trimmed))
		default:
			pypi = append(pypi, trimmed)
		}
	}
	return pypi, source, nil
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
//  2. Source requirements (unpublished pure-Python packages, vendored under
//     vendor/ or on a git branch) are built from source with --no-deps into a
//     separate directory, then merged in. Their dependencies must therefore be
//     present among the PyPI requirements.
//
// When all requirements are published to PyPI (no git+ or local-path entries),
// pass 2 is a no-op and pass 1 installs everything.
func downloadPythonAgent(cfg Config, destDir string) error {
	requirementsFile := filepath.Join(cfg.PackagingDir, "common", "python", "requirements.txt")

	pypiReqs, sourceReqs, err := splitRequirements(requirementsFile)
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

	// Pass 2: source requirements built from source (pure-Python, host-agnostic).
	if len(sourceReqs) > 0 {
		fmt.Printf("  Installing %d Python OTel package(s) from source\n", len(sourceReqs))

		sourceDir, err := os.MkdirTemp("", "otel-python-source-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(sourceDir)

		pass2Args := []string{
			"-m", "pip", "install",
			"--target", sourceDir,
			"--no-compile",
			"--quiet",
			"--no-deps",
		}
		pass2Args = append(pass2Args, sourceReqs...)

		if err := runPip(python, pass2Args); err != nil {
			return err
		}

		// Merge the source-built packages into the main bundle. The pyproto
		// packages add new paths under the opentelemetry/ namespace and new
		// dist-info dirs, so this is a pure overlay with no file collisions.
		if err := mergeTree(sourceDir, destDir); err != nil {
			return fmt.Errorf("merging source-built packages: %w", err)
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
