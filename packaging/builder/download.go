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
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "injector-release.txt"))
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/open-telemetry/opentelemetry-injector/releases/download/%s/libotelinject_%s.so", tag, cfg.Arch)
	return downloadFile(url, dest)
}

// downloadJavaAgent fetches the Java agent JAR from GitHub releases.
func downloadJavaAgent(cfg Config, dest string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "java-agent-release.txt"))
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
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "nodejs-agent-release.txt"))
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

// downloadDotnetAgent fetches the .NET auto-instrumentation for both glibc
// and musl. The glibc archive is extracted fully, then its native library
// directory is moved under a glibc/ prefix to match the layout expected by
// the OpenTelemetry injector. Only the native library directory from the
// musl archive is extracted, placed under a musl/ prefix.
func downloadDotnetAgent(cfg Config, destDir string) error {
	tag, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "dotnet-agent-release.txt"))
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

	// Download musl archive and extract only the native library directory,
	// placing it under musl/ to match the injector's expected layout.
	muslPkg := fmt.Sprintf("opentelemetry-dotnet-instrumentation-linux-musl-%s.zip", dotnetArch)
	muslURL := fmt.Sprintf("%s/%s/%s", baseURL, tag, muslPkg)
	muslZip, err := os.CreateTemp("", "otel-dotnet-musl-*.zip")
	if err != nil {
		return err
	}
	muslZip.Close()
	muslZipPath := muslZip.Name()
	defer os.Remove(muslZipPath)
	if err := downloadFile(muslURL, muslZipPath); err != nil {
		return err
	}

	muslNativeDir := fmt.Sprintf("linux-musl-%s/", dotnetArch)
	muslDest := filepath.Join(destDir, "musl")
	if err := os.MkdirAll(muslDest, 0o755); err != nil {
		return fmt.Errorf("creating musl dir: %w", err)
	}
	if err := extractZipPrefix(muslZipPath, muslDest, muslNativeDir); err != nil {
		return fmt.Errorf("extracting musl native dir: %w", err)
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

// extractZipPrefix extracts only files whose name starts with prefix.
func extractZipPrefix(zipPath, destDir, prefix string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, prefix) {
			continue
		}
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
