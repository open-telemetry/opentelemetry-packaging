// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchChecksums(t *testing.T) {
	body := "" +
		"ef1b39f1379db463533f97ab5a1a3ebcb2bf92b219eed0aff8faf3a899c1ae9  opentelemetry-dotnet-instrumentation-linux-glibc-arm64.zip\n" +
		"a0c52a23366091d8cd8d62424806a6a96734aee869ee4da2fc21f7925fae538  opentelemetry-dotnet-instrumentation-linux-glibc-x64.zip\n" +
		"\n" +
		"DEADBEEF00000000000000000000000000000000000000000000000000000  *binary-mode-file.zip\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	sums, err := fetchChecksums(srv.URL + "/checksums.txt")
	if err != nil {
		t.Fatalf("fetchChecksums: %v", err)
	}

	want := map[string]string{
		"opentelemetry-dotnet-instrumentation-linux-glibc-arm64.zip": "ef1b39f1379db463533f97ab5a1a3ebcb2bf92b219eed0aff8faf3a899c1ae9",
		"opentelemetry-dotnet-instrumentation-linux-glibc-x64.zip":   "a0c52a23366091d8cd8d62424806a6a96734aee869ee4da2fc21f7925fae538",
		"binary-mode-file.zip": "deadbeef00000000000000000000000000000000000000000000000000000",
	}
	if len(sums) != len(want) {
		t.Fatalf("fetchChecksums returned %d entries, want %d: %v", len(sums), len(want), sums)
	}
	for name, digest := range want {
		if sums[name] != digest {
			t.Errorf("checksum for %s = %q, want %q", name, sums[name], digest)
		}
	}
}

func TestFetchChecksumsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := fetchChecksums(srv.URL + "/checksums.txt"); err == nil {
		t.Fatal("fetchChecksums: expected error on HTTP 404, got nil")
	}
}

func TestVerifyFileSHA256(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "artifact.bin")
	content := []byte("hello opentelemetry")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	t.Run("match", func(t *testing.T) {
		if err := verifyFileSHA256(path, digest); err != nil {
			t.Errorf("verifyFileSHA256: %v", err)
		}
	})

	t.Run("match is case-insensitive", func(t *testing.T) {
		if err := verifyFileSHA256(path, strings.ToUpper(digest)); err != nil {
			t.Errorf("verifyFileSHA256: %v", err)
		}
	})

	t.Run("mismatch", func(t *testing.T) {
		bogus := "0000000000000000000000000000000000000000000000000000000000000"
		if err := verifyFileSHA256(path, bogus); err == nil {
			t.Error("verifyFileSHA256: expected error on checksum mismatch, got nil")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if err := verifyFileSHA256(filepath.Join(dir, "missing.bin"), digest); err == nil {
			t.Error("verifyFileSHA256: expected error for missing file, got nil")
		}
	})
}

func TestFetchGitHubAssetDigest(t *testing.T) {
	const digest = "1457e82f86981ae7af77a06add85eae0c8e6cf0c91d58eb948d4e5af160cff6"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/open-telemetry/opentelemetry-injector/releases/tags/v0.11.0" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"assets":[
			{"name":"libotelinject_amd64.so","digest":"sha256:%s"},
			{"name":"libotelinject_arm64.so","digest":"sha256:deadbeef"}
		]}`, digest)
	}))
	defer srv.Close()

	origBase := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = origBase }()

	got, err := fetchGitHubAssetDigest("open-telemetry", "opentelemetry-injector", "v0.11.0", "libotelinject_amd64.so")
	if err != nil {
		t.Fatalf("fetchGitHubAssetDigest: %v", err)
	}
	if got != digest {
		t.Errorf("fetchGitHubAssetDigest = %q, want %q", got, digest)
	}
}

func TestFetchGitHubAssetDigestSendsToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer test-token")
		}
		fmt.Fprint(w, `{"assets":[{"name":"asset.bin","digest":"sha256:deadbeef"}]}`)
	}))
	defer srv.Close()

	origBase := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = origBase }()

	t.Setenv("GITHUB_TOKEN", "test-token")

	if _, err := fetchGitHubAssetDigest("owner", "repo", "v1.0.0", "asset.bin"); err != nil {
		t.Fatalf("fetchGitHubAssetDigest: %v", err)
	}
}

func TestFetchGitHubAssetDigestNoMatchingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"assets":[{"name":"other-asset.bin","digest":"sha256:deadbeef"}]}`)
	}))
	defer srv.Close()

	origBase := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = origBase }()

	if _, err := fetchGitHubAssetDigest("owner", "repo", "v1.0.0", "asset.bin"); err == nil {
		t.Error("fetchGitHubAssetDigest: expected error when no asset matches the name, got nil")
	}
}

func TestFetchGitHubAssetDigestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	origBase := githubAPIBaseURL
	githubAPIBaseURL = srv.URL
	defer func() { githubAPIBaseURL = origBase }()

	if _, err := fetchGitHubAssetDigest("owner", "repo", "v1.0.0", "asset.bin"); err == nil {
		t.Fatal("fetchGitHubAssetDigest: expected error on HTTP 404, got nil")
	}
}

func TestParsePackageFilename(t *testing.T) {
	tests := []struct {
		filename    string
		wantName    string
		wantVersion string
	}{
		{"opentelemetry_instrumentation-0.65b0-py3-none-any.whl", "opentelemetry-instrumentation", "0.65b0"},
		{"opentelemetry_instrumentation-0.65b0.tar.gz", "opentelemetry-instrumentation", "0.65b0"},
		{"protobuf-5.29.3-cp310-abi3-win_amd64.whl", "protobuf", "5.29.3"},
		{"typing_extensions-4.12.2-py3-none-any.whl", "typing-extensions", "4.12.2"},
	}
	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			name, version, err := parsePackageFilename(tt.filename)
			if err != nil {
				t.Fatalf("parsePackageFilename(%q): %v", tt.filename, err)
			}
			if name != tt.wantName || version != tt.wantVersion {
				t.Errorf("parsePackageFilename(%q) = (%q, %q), want (%q, %q)", tt.filename, name, version, tt.wantName, tt.wantVersion)
			}
		})
	}

	t.Run("unrecognized extension", func(t *testing.T) {
		if _, _, err := parsePackageFilename("package-1.0.0.zip"); err == nil {
			t.Error("parsePackageFilename: expected error for unrecognized extension, got nil")
		}
	})

	t.Run("malformed wheel", func(t *testing.T) {
		if _, _, err := parsePackageFilename("onlyname.whl"); err == nil {
			t.Error("parsePackageFilename: expected error for malformed wheel filename, got nil")
		}
	})
}

func TestFetchPyPIDigest(t *testing.T) {
	const filename = "opentelemetry_instrumentation-0.65b0-py3-none-any.whl"
	const digest = "ea967a72b9939b5fcfdad572753b4306c59dcb99e3f382d95dae04286805e137"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/opentelemetry-instrumentation/0.65b0/json" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"urls":[{"filename":%q,"digests":{"sha256":%q}}]}`, filename, digest)
	}))
	defer srv.Close()

	origBase := pypiBaseURL
	pypiBaseURL = srv.URL
	defer func() { pypiBaseURL = origBase }()

	got, err := fetchPyPIDigest("opentelemetry-instrumentation", "0.65b0", filename)
	if err != nil {
		t.Fatalf("fetchPyPIDigest: %v", err)
	}
	if got != digest {
		t.Errorf("fetchPyPIDigest = %q, want %q", got, digest)
	}
}

func TestFetchPyPIDigestNoMatchingFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"urls":[{"filename":"other-file.whl","digests":{"sha256":"deadbeef"}}]}`)
	}))
	defer srv.Close()

	origBase := pypiBaseURL
	pypiBaseURL = srv.URL
	defer func() { pypiBaseURL = origBase }()

	if _, err := fetchPyPIDigest("pkg", "1.0.0", "pkg-1.0.0-py3-none-any.whl"); err == nil {
		t.Error("fetchPyPIDigest: expected error when no url entry matches the filename, got nil")
	}
}

func TestFetchPyPIDigestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	origBase := pypiBaseURL
	pypiBaseURL = srv.URL
	defer func() { pypiBaseURL = origBase }()

	if _, err := fetchPyPIDigest("pkg", "1.0.0", "pkg-1.0.0-py3-none-any.whl"); err == nil {
		t.Fatal("fetchPyPIDigest: expected error on HTTP 404, got nil")
	}
}

func TestVerifyPyPIDownloads(t *testing.T) {
	const filename = "pkg-1.0.0-py3-none-any.whl"
	content := []byte("wheel contents")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg/1.0.0/json" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprintf(w, `{"urls":[{"filename":%q,"digests":{"sha256":%q}}]}`, filename, digest)
	}))
	defer srv.Close()

	origBase := pypiBaseURL
	pypiBaseURL = srv.URL
	defer func() { pypiBaseURL = origBase }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), content, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	if err := verifyPyPIDownloads(dir); err != nil {
		t.Errorf("verifyPyPIDownloads: %v", err)
	}
}

func TestVerifyPyPIDownloadsMismatch(t *testing.T) {
	const filename = "pkg-1.0.0-py3-none-any.whl"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"urls":[{"filename":%q,"digests":{"sha256":"0000000000000000000000000000000000000000000000000000000000000"}}]}`, filename)
	}))
	defer srv.Close()

	origBase := pypiBaseURL
	pypiBaseURL = srv.URL
	defer func() { pypiBaseURL = origBase }()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte("tampered contents"), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	if err := verifyPyPIDownloads(dir); err == nil {
		t.Error("verifyPyPIDownloads: expected error on checksum mismatch, got nil")
	}
}
