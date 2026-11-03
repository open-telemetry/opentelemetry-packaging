// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonABITag(t *testing.T) {
	for version, want := range map[string]string{
		"3.10": "cp310",
		"3.11": "cp311",
		"3.9":  "cp39",
	} {
		if got := pythonABITag(version); got != want {
			t.Errorf("pythonABITag(%q) = %q, want %q", version, got, want)
		}
	}
}

func writeWheels(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPartitionWheelsByInterpreterSharesOnlyWhatEveryInterpreterResolved(t *testing.T) {
	caches := []interpreterCache{
		{pythonVersion: "3.10", downloadDir: writeWheels(t,
			"opentelemetry_sdk-1.44.0-py3-none-any.whl",
			"psutil-7.2.2-cp36-abi3-manylinux_2_28_x86_64.whl",
			"rpds_py-0.30.0-cp310-cp310-manylinux_2_17_x86_64.whl",
		)},
		{pythonVersion: "3.11", downloadDir: writeWheels(t,
			"opentelemetry_sdk-1.44.0-py3-none-any.whl",
			"psutil-7.2.2-cp36-abi3-manylinux_2_28_x86_64.whl",
			"rpds_py-2026.9.1-cp311-cp311-manylinux_2_17_x86_64.whl",
		)},
	}

	shared, perVersion, err := partitionWheelsByInterpreter(caches)
	if err != nil {
		t.Fatal(err)
	}

	wantShared := []string{
		"opentelemetry_sdk-1.44.0-py3-none-any.whl",
		"psutil-7.2.2-cp36-abi3-manylinux_2_28_x86_64.whl",
	}
	if got := baseNames(shared); !equalStrings(got, wantShared) {
		t.Errorf("shared = %v, want %v", got, wantShared)
	}

	for version, want := range map[string]string{
		"3.10": "rpds_py-0.30.0-cp310-cp310-manylinux_2_17_x86_64.whl",
		"3.11": "rpds_py-2026.9.1-cp311-cp311-manylinux_2_17_x86_64.whl",
	} {
		got := baseNames(perVersion[version])
		if !equalStrings(got, []string{want}) {
			t.Errorf("perVersion[%q] = %v, want [%s]", version, got, want)
		}
	}
}

func TestPartitionWheelsByInterpreterTakesSharedPathsFromTheFirstCache(t *testing.T) {
	first := writeWheels(t, "wrapt-2.5.0-py3-none-any.whl")
	caches := []interpreterCache{
		{pythonVersion: "3.10", downloadDir: first},
		{pythonVersion: "3.11", downloadDir: writeWheels(t, "wrapt-2.5.0-py3-none-any.whl")},
	}

	shared, _, err := partitionWheelsByInterpreter(caches)
	if err != nil {
		t.Fatal(err)
	}
	if len(shared) != 1 || filepath.Dir(shared[0]) != first {
		t.Errorf("shared = %v, want a single path under %s", shared, first)
	}
}

func TestPartitionWheelsByInterpreterRejectsAnEmptyCacheList(t *testing.T) {
	if _, _, err := partitionWheelsByInterpreter(nil); err == nil {
		t.Error("expected an error for an empty cache list, got nil")
	}
}

func TestWriteSitecustomizeRewritesTheSupportedInterpreterList(t *testing.T) {
	source := filepath.Join(t.TempDir(), "sitecustomize.py")
	const body = "x = 1\n" +
		"supported_python_versions = [\"3.99\"]  # supported-python-versions\n" +
		"y = 2\n"
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "sitecustomize.py")
	if err := writeSitecustomize(source, dest, []string{"3.10", "3.11", "3.13"}); err != nil {
		t.Fatal(err)
	}

	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	want := "supported_python_versions = [\"3.10\", \"3.11\", \"3.13\"]  # supported-python-versions\n"
	if !strings.Contains(string(written), want) {
		t.Errorf("written sitecustomize.py does not contain %q:\n%s", want, written)
	}
	if !strings.HasPrefix(string(written), "x = 1\n") || !strings.HasSuffix(string(written), "y = 2\n") {
		t.Errorf("writeSitecustomize altered lines outside the marker:\n%s", written)
	}
}

func TestWriteSitecustomizeKeepsASingleInterpreterAList(t *testing.T) {
	source := filepath.Join(t.TempDir(), "sitecustomize.py")
	if err := os.WriteFile(source,
		[]byte("supported_python_versions = [\"3.10\", \"3.11\"]  # supported-python-versions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "sitecustomize.py")
	if err := writeSitecustomize(source, dest, []string{"3.11"}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(written)); got != "supported_python_versions = [\"3.11\"]  # supported-python-versions" {
		t.Errorf("got %q, want a single-element list", got)
	}
}

func TestWriteSitecustomizeFailsWithoutExactlyOneMarker(t *testing.T) {
	for name, body := range map[string]string{
		"no marker": "supported_python_versions = [\"3.10\"]\n",
		"two markers": "supported_python_versions = [\"3.10\"]  # supported-python-versions\n" +
			"supported_python_versions = [\"3.11\"]  # supported-python-versions\n",
	} {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "sitecustomize.py")
			if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "sitecustomize.py")
			if err := writeSitecustomize(source, dest, []string{"3.11"}); err == nil {
				t.Error("expected an error, got nil")
			}
		})
	}
}

func TestWriteSitecustomizeRejectsANonThreeMajorVersion(t *testing.T) {
	source := filepath.Join(t.TempDir(), "sitecustomize.py")
	if err := os.WriteFile(source,
		[]byte("supported_python_versions = [\"3.10\"]  # supported-python-versions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "sitecustomize.py")
	if err := writeSitecustomize(source, dest, []string{"4.0"}); err == nil {
		t.Error("expected an error for a non-3.x version, got nil")
	}
}

func writeDistInfo(t *testing.T, dir, name, version string) {
	t.Helper()
	distInfo := filepath.Join(dir, name+"-"+version+".dist-info")
	if err := os.MkdirAll(distInfo, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := "Metadata-Version: 2.1\nName: " + name + "\nVersion: " + version + "\n"
	if err := os.WriteFile(filepath.Join(distInfo, "METADATA"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateAllDependenciesLetsTheInterpreterDirectoryWin(t *testing.T) {
	shared := t.TempDir()
	writeDistInfo(t, shared, "opentelemetry_sdk", "1.44.0")
	writeDistInfo(t, shared, "rpds_py", "2026.9.1")

	interpreter := t.TempDir()
	writeDistInfo(t, interpreter, "rpds_py", "0.30.0")

	output := filepath.Join(t.TempDir(), "all-dependencies.txt")
	if err := generateAllDependencies([]string{shared, interpreter}, output); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	want := "opentelemetry_sdk==1.44.0\nrpds_py==0.30.0\n"
	if string(data) != want {
		t.Errorf("got:\n%s\nwant:\n%s", data, want)
	}
}

func baseNames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	return names
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
