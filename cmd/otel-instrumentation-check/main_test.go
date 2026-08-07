// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installedLayout builds a fully valid installation under a temp directory:
// the injector library present, listed in ld.so.preload, and a Python agent
// registered with its files on disk. Individual tests then break one piece to
// exercise a failure mode.
func installedLayout(t *testing.T) layout {
	t.Helper()
	root := t.TempDir()

	libDir := filepath.Join(root, "usr/lib/opentelemetry/injector")
	require.NoError(t, os.MkdirAll(libDir, 0o755))
	injectorLib := filepath.Join(libDir, "libotelinject.so")
	require.NoError(t, os.WriteFile(injectorLib, []byte("so"), 0o755))

	preloadFile := filepath.Join(root, "etc/ld.so.preload")
	require.NoError(t, os.MkdirAll(filepath.Dir(preloadFile), 0o755))
	require.NoError(t, os.WriteFile(preloadFile, []byte(injectorLib+"\n"), 0o644))

	confDir := filepath.Join(root, "etc/opentelemetry/injector/conf.d")
	require.NoError(t, os.MkdirAll(confDir, 0o755))

	pythonPrefix := filepath.Join(root, "usr/lib/opentelemetry/python")
	require.NoError(t, os.MkdirAll(filepath.Join(pythonPrefix, "glibc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "python.conf"),
		[]byte("python_auto_instrumentation_agent_path_prefix="+pythonPrefix+"\n"), 0o644))

	injectorConf := filepath.Join(root, "etc/opentelemetry/injector/injector.conf")
	defaultEnv := filepath.Join(root, "etc/opentelemetry/injector/default_env.conf")
	require.NoError(t, os.WriteFile(injectorConf,
		[]byte("all_auto_instrumentation_agents_env_path="+defaultEnv+"\n"), 0o644))
	require.NoError(t, os.WriteFile(defaultEnv, []byte("## defaults\n"), 0o644))

	return layout{
		preloadFile:  preloadFile,
		injectorLib:  injectorLib,
		injectorConf: injectorConf,
		confDir:      confDir,
		defaultEnv:   defaultEnv,
	}
}

// messages joins every result message so tests can assert on substrings
// regardless of ordering details.
func messages(results []checkResult) string {
	var b strings.Builder
	for _, r := range results {
		b.WriteString(r.format())
		b.WriteByte('\n')
	}
	return b.String()
}

func TestHealthyInstallationPasses(t *testing.T) {
	results, ok := checkInstallation(installedLayout(t))
	assert.True(t, ok, "a complete installation must pass:\n%s", messages(results))
	assert.Contains(t, messages(results), "injector active")
	assert.Contains(t, messages(results), "Python agent registered")
}

func TestMissingInjectorLibraryFails(t *testing.T) {
	l := installedLayout(t)
	require.NoError(t, os.Remove(l.injectorLib))
	results, ok := checkInstallation(l)
	assert.False(t, ok)
	assert.Contains(t, messages(results), "injector library missing")
}

func TestInjectorNotInPreloadFails(t *testing.T) {
	l := installedLayout(t)
	require.NoError(t, os.WriteFile(l.preloadFile, []byte("/opt/other/lib.so\n"), 0o644))
	results, ok := checkInstallation(l)
	assert.False(t, ok)
	assert.Contains(t, messages(results), "not listed in")
}

// TestPreloadEntrySharingALineIsMatched pins that a preload file with several
// whitespace-separated entries on one line still matches the injector, the way
// the dynamic linker and the postinstall script treat it.
func TestPreloadEntrySharingALineIsMatched(t *testing.T) {
	l := installedLayout(t)
	require.NoError(t, os.WriteFile(l.preloadFile,
		[]byte("/opt/other/lib.so "+l.injectorLib+"\n"), 0o644))
	results, ok := checkInstallation(l)
	assert.True(t, ok, messages(results))
}

func TestNoAgentsRegisteredFails(t *testing.T) {
	l := installedLayout(t)
	require.NoError(t, os.Remove(filepath.Join(l.confDir, "python.conf")))
	results, ok := checkInstallation(l)
	assert.False(t, ok)
	assert.Contains(t, messages(results), "no language agents registered")
}

func TestRegisteredAgentWithMissingFilesFails(t *testing.T) {
	l := installedLayout(t)
	// Point the drop-in at a prefix whose glibc/ subdirectory does not exist.
	require.NoError(t, os.WriteFile(filepath.Join(l.confDir, "python.conf"),
		[]byte("python_auto_instrumentation_agent_path_prefix=/nonexistent/python\n"), 0o644))
	results, ok := checkInstallation(l)
	assert.False(t, ok)
	assert.Contains(t, messages(results), "its files are missing")
}

// TestAllLanguageAgentsRecognized pins that every conf.d key a language package
// installs is understood and resolves to the right on-disk path.
func TestAllLanguageAgentsRecognized(t *testing.T) {
	l := installedLayout(t)
	root := filepath.Dir(filepath.Dir(l.preloadFile)) // root/etc/ld.so.preload -> root

	javaJar := filepath.Join(root, "usr/lib/opentelemetry/java/opentelemetry-javaagent.jar")
	require.NoError(t, os.MkdirAll(filepath.Dir(javaJar), 0o755))
	require.NoError(t, os.WriteFile(javaJar, []byte("jar"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(l.confDir, "java.conf"),
		[]byte("jvm_auto_instrumentation_agent_path="+javaJar+"\n"), 0o644))

	nodeEntry := filepath.Join(root, "usr/lib/opentelemetry/nodejs/register.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(nodeEntry), 0o755))
	require.NoError(t, os.WriteFile(nodeEntry, []byte("js"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(l.confDir, "nodejs.conf"),
		[]byte("nodejs_auto_instrumentation_agent_path="+nodeEntry+"\n"), 0o644))

	dotnetPrefix := filepath.Join(root, "usr/lib/opentelemetry/dotnet")
	require.NoError(t, os.MkdirAll(filepath.Join(dotnetPrefix, "glibc"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(l.confDir, "dotnet.conf"),
		[]byte("dotnet_auto_instrumentation_agent_path_prefix="+dotnetPrefix+"\n"), 0o644))

	count, results := checkRegisteredAgents(l.confDir)
	msg := messages(results)
	assert.Equal(t, 4, count, msg)
	for _, language := range []string{"Java", "Node.js", ".NET", "Python"} {
		assert.Contains(t, msg, language+" agent registered")
	}
}

func TestDeclarativeConfigReported(t *testing.T) {
	l := installedLayout(t)
	configPath := filepath.Join(t.TempDir(), "otel-config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("file_format: \"1.0\"\n"), 0o644))
	require.NoError(t, os.WriteFile(l.defaultEnv,
		[]byte("OTEL_CONFIG_FILE="+configPath+"\n"), 0o644))

	results, ok := checkInstallation(l)
	assert.True(t, ok, messages(results))
	assert.Contains(t, messages(results), "declarative configuration active")
}

func TestMissingDeclarativeConfigWarnsButDoesNotFail(t *testing.T) {
	l := installedLayout(t)
	require.NoError(t, os.WriteFile(l.defaultEnv,
		[]byte("OTEL_CONFIG_FILE=/etc/opentelemetry/does-not-exist.yaml\n"), 0o644))

	results, ok := checkInstallation(l)
	assert.True(t, ok, "a missing OTEL_CONFIG_FILE is a warning, not an install defect")
	assert.Contains(t, messages(results), "is set but the file is missing")
}

func TestCustomEndpointReported(t *testing.T) {
	l := installedLayout(t)
	require.NoError(t, os.WriteFile(l.defaultEnv,
		[]byte("OTEL_EXPORTER_OTLP_ENDPOINT=https://otlp.example.com\n"), 0o644))
	results, _ := checkInstallation(l)
	assert.Contains(t, messages(results), "https://otlp.example.com")
}

func TestDefaultEndpointReportedWhenUnset(t *testing.T) {
	results, _ := checkInstallation(installedLayout(t))
	assert.Contains(t, messages(results), "localhost:4317")
}

func TestParseKeyValueFileSkipsCommentsAndBlanks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf")
	require.NoError(t, os.WriteFile(path, []byte(
		"# a comment\n\n## prose\nKEY = value \n#OTHER=commented\nLAST=x=y\n"), 0o644))
	got := parseKeyValueFile(path)
	assert.Equal(t, "value", got["KEY"])
	assert.Equal(t, "x=y", got["LAST"], "only the first = splits key from value")
	_, hasComment := got["#OTHER"]
	assert.False(t, hasComment)
}
