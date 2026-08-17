// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"crypto/sha256"
	"encoding/hex"
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

// testSignerPubKey and testSignerSignature are a throwaway keypair generated
// solely for these tests (not the real OpenTelemetry key): "gpg
// --quick-generate-key" then "gpg --detach-sign" over testSignedContent.
const testSignerFingerprint = "3192C43CCCB7A536E8A17681CD0D2FC35A396955"

const testSignedContent = "hello opentelemetry\n"

const testSignerPubKey = `-----BEGIN PGP PUBLIC KEY BLOCK-----

mQENBGp8Q/UBCAC/p//CdVtF1Acv1pHPEapaunkvMmS7h2N9pNEVRBN2P/VQdO7T
L/OzrUacsFPbFYFutvn+GbrgZO8mPUzTTsriZ3ZpMzVfx+i5vnv223R1ej1y6oeq
sT6+IOisb4qkYcx9Q1AhAO2Dcsx7G4gADRzWH/baWeOmscpmjFXpGX/M8epyo7n4
YjlPyd5rejRkhiRFNSdxbwLXv6+RW8KZDq4FMkW2S1E+yAYON8p7LYm2Q33xnSCs
+CusYj092I6rwFycnxSBhPznjWbVrNbNhvraVYZ23D7TwU8K9b4h3r54jw6sIRAr
gqUaAUtLJagUOtQqAM7zk85fO0TgDoBl0cE/ABEBAAG0HlRlc3QgU2lnbmVyIDx0
ZXN0QGV4YW1wbGUuY29tPokBVAQTAQoAPhYhBDGSxDzMt6U26KF2gc0NL8NaOWlV
BQJqfEP1AhsDBQkAAVGABQsJCAcCBhUKCQgLAgQWAgMBAh4BAheAAAoJEM0NL8Na
OWlV7z4IAIy1rwHpmN9ZhxLxLSwZOb++Kd2lRtfDw5AKDDnqCj42a00b4e4V5m39
C02Y6RqqyXic/7MYNNtRFQr2rJbLpxOU2iwlEQvUykEvVS28ZtnsNcjUhgBPRdGd
jA+jRiCnQ6AEc1Irpryf07m0WVnCCZ6SvPe2+kgp0u8+ext29ADXF72x81LROm1V
7R7zMtWjWiPXB8o919Ck9bkWmfQS+oMFfvEWIEg8GwK8VsyMlORAwyTmAakEXMFJ
79u4ocDPdMVD6XLE45CgnUDoxp/byH0l81F+7aRLkVqlIIhxdECT8uu3KwvOczwL
JgH18LuxZi/NF7P68CbamFP34H0EIVY=
=Q+f4
-----END PGP PUBLIC KEY BLOCK-----
`

const testSignerSignature = `-----BEGIN PGP SIGNATURE-----

iQEzBAABCgAdFiEEMZLEPMy3pTbooXaBzQ0vw1o5aVUFAmp8RAIACgkQzQ0vw1o5
aVVGXgf+PXOPfAfWIiGBDPfo20VzVGJ/D+HmWL5lEpFhvsBjiqhygN7OqKka3bj/
PBa8eIshjHBIIdUKcG1eHSvpx7BqZDmkE568D1EjfUzN2uVIkkrvrcxmrsy3hfNE
YTkEtTqCWdbk2DW26PQbpJLAul/fdsWFXdk6yv0qw5CGPE22blulyosyPvzIgBW0
wvqJhiHgCzZeihzlwhb3bmIF7u5fGpJfX31a3YTykeAaFvA+5HTWK012EFziKey3
7HRBPdOrSvgGpL+45Cvj1ulnNzZu2Gh4IVPtFgSyM4KqVXEJW0Gx/M/lCHzNv9cx
0Mxqw8e97PLIUXLS5H9Wa2WG2hj1gg==
=Tz8k
-----END PGP SIGNATURE-----
`

func TestVerifyDetachedSignature(t *testing.T) {
	dir := t.TempDir()

	keyringPath := filepath.Join(dir, "keyring.asc")
	if err := os.WriteFile(keyringPath, []byte(testSignerPubKey), 0o644); err != nil {
		t.Fatalf("writing keyring: %v", err)
	}

	signedPath := filepath.Join(dir, "artifact.bin")
	if err := os.WriteFile(signedPath, []byte(testSignedContent), 0o644); err != nil {
		t.Fatalf("writing signed artifact: %v", err)
	}

	t.Run("valid signature and matching fingerprint", func(t *testing.T) {
		if err := verifyDetachedSignature(signedPath, keyringPath, testSignerFingerprint, []byte(testSignerSignature)); err != nil {
			t.Errorf("verifyDetachedSignature: %v", err)
		}
	})

	t.Run("fingerprint mismatch", func(t *testing.T) {
		bogus := "0000000000000000000000000000000000000000000000000000000000000000000000000000"
		err := verifyDetachedSignature(signedPath, keyringPath, bogus, []byte(testSignerSignature))
		if err == nil {
			t.Fatal("verifyDetachedSignature: expected error on fingerprint mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "unexpected key") {
			t.Errorf("verifyDetachedSignature: error = %q, want it to mention the unexpected key", err)
		}
	})

	t.Run("tampered content", func(t *testing.T) {
		tamperedPath := filepath.Join(dir, "tampered.bin")
		if err := os.WriteFile(tamperedPath, []byte(testSignedContent+"tampered"), 0o644); err != nil {
			t.Fatalf("writing tampered artifact: %v", err)
		}
		if err := verifyDetachedSignature(tamperedPath, keyringPath, testSignerFingerprint, []byte(testSignerSignature)); err == nil {
			t.Error("verifyDetachedSignature: expected error on tampered content, got nil")
		}
	})

	t.Run("malformed signature", func(t *testing.T) {
		if err := verifyDetachedSignature(signedPath, keyringPath, testSignerFingerprint, []byte("not a signature")); err == nil {
			t.Error("verifyDetachedSignature: expected error on malformed signature, got nil")
		}
	})

	t.Run("missing keyring file", func(t *testing.T) {
		err := verifyDetachedSignature(signedPath, filepath.Join(dir, "missing-keyring.asc"), testSignerFingerprint, []byte(testSignerSignature))
		if err == nil {
			t.Error("verifyDetachedSignature: expected error for missing keyring file, got nil")
		}
	})

	t.Run("missing signed file", func(t *testing.T) {
		err := verifyDetachedSignature(filepath.Join(dir, "missing.bin"), keyringPath, testSignerFingerprint, []byte(testSignerSignature))
		if err == nil {
			t.Error("verifyDetachedSignature: expected error for missing signed file, got nil")
		}
	})
}

func TestFetchBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()

	data, err := fetchBytes(srv.URL + "/file")
	if err != nil {
		t.Fatalf("fetchBytes: %v", err)
	}
	if string(data) != "payload" {
		t.Errorf("fetchBytes returned %q, want %q", data, "payload")
	}
}

func TestFetchBytesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	if _, err := fetchBytes(srv.URL + "/file"); err == nil {
		t.Fatal("fetchBytes: expected error on HTTP 404, got nil")
	}
}
