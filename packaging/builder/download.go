// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
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

// githubAPIBaseURL is the base of the GitHub REST API, overridable in tests.
var githubAPIBaseURL = "https://api.github.com"

// fetchGitHubAssetDigest returns the sha256 digest GitHub itself computed
// for a release asset when it was uploaded, via the Releases API. This lets
// a download be verified without depending on the upstream project
// publishing its own checksum manifest — GitHub attaches this digest to
// every asset of every public release regardless.
//
// A GITHUB_TOKEN in the environment is sent as a bearer token to raise the
// otherwise very low (60/hour, shared across the whole CI runner IP range)
// unauthenticated rate limit; it is not required for public repositories.
func fetchGitHubAssetDigest(owner, repo, tag, assetName string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/tags/%s", githubAPIBaseURL, owner, repo, tag)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "opentelemetry-packaging")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching release metadata for %s/%s@%s: %w", owner, repo, tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching release metadata for %s/%s@%s: HTTP %d", owner, repo, tag, resp.StatusCode)
	}

	var release struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding release metadata for %s/%s@%s: %w", owner, repo, tag, err)
	}
	for _, a := range release.Assets {
		if a.Name != assetName {
			continue
		}
		digest, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || digest == "" {
			return "", fmt.Errorf("release asset %s has no sha256 digest", assetName)
		}
		return digest, nil
	}
	return "", fmt.Errorf("release %s/%s@%s has no asset named %s", owner, repo, tag, assetName)
}

// pypiBaseURL is the base of PyPI's per-release JSON API, overridable in
// tests.
var pypiBaseURL = "https://pypi.org/pypi"

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

// downloadInjector fetches libotelinject.so from GitHub releases and verifies
// it against the sha256 digest GitHub itself published for that asset.
func downloadInjector(cfg Config, dest string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "injector", "release.txt"))
	if err != nil {
		return err
	}
	const owner, repo = "open-telemetry", "opentelemetry-injector"
	assetName := fmt.Sprintf("libotelinject_%s.so", cfg.Arch)

	want, err := fetchGitHubAssetDigest(owner, repo, tag, assetName)
	if err != nil {
		return fmt.Errorf("fetching asset digest: %w", err)
	}

	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, tag, assetName)
	if err := downloadFile(url, dest); err != nil {
		return err
	}
	return verifyFileSHA256(dest, want)
}

// downloadJavaAgent fetches the Java agent JAR from GitHub releases and
// verifies it against the sha256 digest GitHub itself published for that
// asset.
func downloadJavaAgent(cfg Config, dest string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", "java", "release.txt"))
	if err != nil {
		return err
	}
	const owner, repo = "open-telemetry", "opentelemetry-java-instrumentation"
	const assetName = "opentelemetry-javaagent.jar"

	want, err := fetchGitHubAssetDigest(owner, repo, tag, assetName)
	if err != nil {
		return fmt.Errorf("fetching asset digest: %w", err)
	}

	url := fmt.Sprintf("https://github.com/%s/%s/releases/download/%s/%s", owner, repo, tag, assetName)
	if err := downloadFile(url, dest); err != nil {
		return err
	}
	return verifyFileSHA256(dest, want)
}

// npmRegistryBaseURL is the base of the npm registry's package metadata API,
// overridable in tests.
var npmRegistryBaseURL = "https://registry.npmjs.org"

// fetchNpmIntegrity fetches the Subresource Integrity string npm's registry
// itself published for a package version's tarball (dist.integrity, e.g.
// "sha512-<base64>") — the same trust model downloadDotnetAgent and
// downloadPythonAgent use for their upstream registries: the expected digest
// is fetched fresh from the registry for the resolved version, not stored in
// this repo.
func fetchNpmIntegrity(name, version string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s", npmRegistryBaseURL, name, version)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching npm metadata for %s@%s: %w", name, version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching npm metadata for %s@%s: HTTP %d", name, version, resp.StatusCode)
	}

	var meta struct {
		Dist struct {
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", fmt.Errorf("decoding npm metadata for %s@%s: %w", name, version, err)
	}
	if meta.Dist.Integrity == "" {
		return "", fmt.Errorf("npm metadata for %s@%s has no dist.integrity", name, version)
	}
	return meta.Dist.Integrity, nil
}

// verifyFileSRI hashes the file at path and returns an error if it does not
// match the expected Subresource Integrity string (e.g. "sha512-<base64>"),
// the format npm's registry publishes tarball digests in.
func verifyFileSRI(path, integrity string) error {
	algo, wantB64, ok := strings.Cut(integrity, "-")
	if !ok {
		return fmt.Errorf("malformed integrity string: %q", integrity)
	}

	var h hash.Hash
	switch algo {
	case "sha512":
		h = sha512.New()
	case "sha256":
		h = sha256.New()
	default:
		return fmt.Errorf("unsupported integrity algorithm %q", algo)
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hashing %s: %w", path, err)
	}

	got := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if got != wantB64 {
		return fmt.Errorf("integrity mismatch for %s: got %s-%s, want %s", path, algo, got, integrity)
	}
	return nil
}

// downloadNodejsAgent fetches the Node.js auto-instrumentation from npm.
// This shells out to npm because the npm registry protocol and package
// installation logic (with native dependencies) is non-trivial. npm's own
// installer already verifies every tarball it downloads (including
// transitive dependencies) against the registry-published integrity as part
// of its normal operation, failing loudly on a mismatch; the explicit check
// below covers the one file npm's installer doesn't verify itself — the
// tarball "npm pack" writes to disk — against the same registry-published
// integrity, before it is ever installed.
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

	const npmPkg = "@opentelemetry/auto-instrumentations-node"
	want, err := fetchNpmIntegrity(npmPkg, ver)
	if err != nil {
		return fmt.Errorf("fetching npm integrity: %w", err)
	}
	if err := verifyFileSRI(tgz, want); err != nil {
		return err
	}

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

// parsePackageFilename extracts the PyPI project name and version from a
// downloaded wheel (PEP 427/600) or sdist (PEP 625) filename, so a file with
// no other attached metadata can be looked up against PyPI's per-release
// file index. Both formats start with "<name>-<version>", with the name
// normalized ('-', '.' collapsed to '_'); everything after the version is
// build/interpreter/platform tags (wheels) or the ".tar.gz" suffix (sdists).
func parsePackageFilename(filename string) (name, version string, err error) {
	switch {
	case strings.HasSuffix(filename, ".whl"):
		parts := strings.Split(strings.TrimSuffix(filename, ".whl"), "-")
		if len(parts) < 5 {
			return "", "", fmt.Errorf("malformed wheel filename: %s", filename)
		}
		name, version = parts[0], parts[1]
	case strings.HasSuffix(filename, ".tar.gz"):
		base := strings.TrimSuffix(filename, ".tar.gz")
		idx := strings.LastIndex(base, "-")
		if idx < 0 {
			return "", "", fmt.Errorf("malformed sdist filename: %s", filename)
		}
		name, version = base[:idx], base[idx+1:]
	default:
		return "", "", fmt.Errorf("unrecognized package file type: %s", filename)
	}
	// PyPI's JSON API resolves any of the project's registered name spellings,
	// but the filename form always uses underscores; converting back to
	// hyphens matches how PyPI project names are conventionally written.
	return strings.ReplaceAll(name, "_", "-"), version, nil
}

// pypiRelease is the subset of PyPI's "/pypi/<name>/<version>/json" response
// needed to look up a published file's digest.
type pypiRelease struct {
	Urls []struct {
		Filename string `json:"filename"`
		Digests  struct {
			SHA256 string `json:"sha256"`
		} `json:"digests"`
	} `json:"urls"`
}

// fetchPyPIDigest fetches the sha256 digest PyPI itself published for a
// given project release file, straight from PyPI's JSON API — the same
// trust model downloadDotnetAgent uses for the .NET checksums.txt: the
// expected digest is fetched fresh from the upstream registry for the
// resolved version, not stored in this repo, so a compromised or corrupted
// download can be caught without any local hash to maintain.
func fetchPyPIDigest(name, version, filename string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/json", pypiBaseURL, name, version)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetching PyPI metadata for %s==%s: %w", name, version, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching PyPI metadata for %s==%s: HTTP %d", name, version, resp.StatusCode)
	}

	var rel pypiRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("decoding PyPI metadata for %s==%s: %w", name, version, err)
	}
	for _, u := range rel.Urls {
		if u.Filename != filename {
			continue
		}
		if u.Digests.SHA256 == "" {
			return "", fmt.Errorf("PyPI metadata for %s has no sha256 digest", filename)
		}
		return u.Digests.SHA256, nil
	}
	return "", fmt.Errorf("PyPI metadata for %s==%s has no entry for %s", name, version, filename)
}

// verifyPyPIDownloads checks every file pip downloaded into dir against the
// sha256 digest PyPI published for that exact release file. Because the
// files are named after the package pip download actually resolved, this
// covers the full dependency closure (transitive dependencies included), not
// just the packages explicitly pinned in requirements.txt.
func verifyPyPIDownloads(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading downloaded packages: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		filename := e.Name()
		name, version, err := parsePackageFilename(filename)
		if err != nil {
			return err
		}
		want, err := fetchPyPIDigest(name, version, filename)
		if err != nil {
			return err
		}
		if err := verifyFileSHA256(filepath.Join(dir, filename), want); err != nil {
			return err
		}
	}
	return nil
}

// downloadPythonAgent installs Python auto-instrumentation packages into destDir.
// The packages are defined by packaging/common/python/requirements.txt.
//
// Installation happens in two passes so the resulting package is correct for the
// target Linux architecture and Python version regardless of the build host:
//
//  1. PyPI requirements are fetched binary-only, pinned to manylinux wheels for
//     the target arch and to the target Python version/ABI. This prevents the
//     build host's OS (e.g. macOS) and Python version from leaking compiled
//     extensions into the package. Every resolved file (pinned requirements and
//     their transitive dependencies alike) is downloaded to a local cache and
//     verified against PyPI's own published sha256 digest before anything is
//     installed; installation then proceeds --no-index from that verified
//     cache, so a mismatched file can never be silently installed.
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

	fmt.Printf("  Installing Python OTel packages (PyPI, linux/%s, py%s) into %s\n", cfg.Arch, targetPythonVersion, destDir)

	pypiReqFile := filepath.Join(filepath.Dir(destDir), "requirements-pypi.txt")
	if err := os.WriteFile(pypiReqFile, []byte(strings.Join(pypiReqs, "\n")+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing PyPI requirements file: %w", err)
	}

	platformArgs := []string{
		"--only-binary=:all:",
		"--python-version", targetPythonVersion,
		"--implementation", "cp",
		"--abi", targetPythonABI,
		"--abi", "abi3",
		"--abi", "none",
	}
	for _, p := range platforms {
		platformArgs = append(platformArgs, "--platform", p)
	}

	// Pass 1a: download the full resolved closure into a local cache and
	// verify it before anything is installed.
	downloadDir, err := os.MkdirTemp("", "otel-python-download-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(downloadDir)

	downloadArgs := []string{
		"-m", "pip", "download",
		"--dest", downloadDir,
		"--quiet",
	}
	downloadArgs = append(downloadArgs, platformArgs...)
	downloadArgs = append(downloadArgs, "-r", pypiReqFile)

	if err := runPip(python, downloadArgs); err != nil {
		return err
	}

	if err := verifyPyPIDownloads(downloadDir); err != nil {
		return fmt.Errorf("verifying downloaded Python packages: %w", err)
	}

	// Pass 1b: install from the verified local cache only. --no-index means
	// pip cannot fall back to re-fetching a file that was never checked.
	installArgs := []string{
		"-m", "pip", "install",
		"--target", destDir,
		"--no-compile",
		"--quiet",
		"--no-index",
		"--find-links", downloadDir,
	}
	installArgs = append(installArgs, platformArgs...)
	installArgs = append(installArgs, "-r", pypiReqFile)

	if err := runPip(python, installArgs); err != nil {
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
