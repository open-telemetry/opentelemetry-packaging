// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/goreleaser/nfpm/v2"
	"github.com/goreleaser/nfpm/v2/files"
)

// Installation paths (FHS-compliant).
const (
	installDir         = "/usr/lib/opentelemetry"
	configDir          = "/etc/opentelemetry"
	injectorInstallDir = installDir + "/injector"
	injectorConfigDir  = configDir + "/injector"
	javaInstallDir     = installDir + "/java"
	javaConfigDir      = configDir + "/java"
	nodejsInstallDir   = installDir + "/nodejs"
	nodejsConfigDir    = configDir + "/nodejs"
	dotnetInstallDir   = installDir + "/dotnet"
	dotnetConfigDir    = configDir + "/dotnet"
	pythonInstallDir   = installDir + "/python"
	pythonConfigDir    = configDir + "/python"
)

// Injector is the opentelemetry-injector package component.
var Injector = Component{
	Name:        "injector",
	Description: injectorDescription,
	InfoFunc:    injectorInfo,
}

// Java is the opentelemetry-java-autoinstrumentation package component.
var Java = Component{
	Name:        "java",
	Description: javaDescription,
	InfoFunc:    javaInfo,
}

// Nodejs is the opentelemetry-nodejs-autoinstrumentation package component.
var Nodejs = Component{
	Name:        "nodejs",
	Description: nodejsDescription,
	InfoFunc:    nodejsInfo,
}

// Dotnet is the opentelemetry-dotnet-autoinstrumentation package component.
var Dotnet = Component{
	Name:        "dotnet",
	Description: dotnetDescription,
	InfoFunc:    dotnetInfo,
}

// Python is the opentelemetry-python-autoinstrumentation package component.
var Python = Component{
	Name:        "python",
	Description: pythonDescription,
	InfoFunc:    pythonInfo,
}

// Meta is the opentelemetry metapackage component.
var Meta = Component{
	Name:        "meta",
	Description: metaDescription,
	InfoFunc:    metaInfo,
}

const (
	injectorDescription = "OpenTelemetry LD_PRELOAD-based automatic instrumentation injector"
	javaDescription     = "OpenTelemetry Java Auto-Instrumentation Agent"
	nodejsDescription   = "OpenTelemetry Node.js Auto-Instrumentation"
	dotnetDescription   = "OpenTelemetry .NET Automatic Instrumentation"
	pythonDescription   = "OpenTelemetry Python Auto-Instrumentation"
	metaDescription     = "OpenTelemetry Auto-Instrumentation Suite (metapackage)"
)

func injectorInfo(cfg Config, format string) (*nfpm.Info, func(), error) {
	staging, err := os.MkdirTemp("", "otel-injector-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	soPath := filepath.Join(staging, "libotelinject.so")
	if err := downloadInjector(cfg, soPath); err != nil {
		return nil, cleanup, fmt.Errorf("downloading injector: %w", err)
	}

	manPath, err := generateManPage(cfg, staging, "injector")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	info := commonInfo(cfg, "opentelemetry-injector", injectorDescription, cfg.Arch)
	info.Overridables.Provides = []string{"opentelemetry-injector1"}
	info.Overridables.Scripts = nfpm.Scripts{
		PostInstall: filepath.Join(commonDir, "scripts", "postinstall-injector.sh"),
		PreRemove:   filepath.Join(commonDir, "scripts", "preuninstall-injector.sh"),
	}
	info.Overridables.Contents = files.Contents{
		regularFile(soPath, injectorInstallDir+"/libotelinject.so", 0o755),
		configFile(filepath.Join(commonDir, "injector", "injector.conf"), injectorConfigDir+"/injector.conf"),
		configFile(filepath.Join(commonDir, "injector", "default_env.conf"), injectorConfigDir+"/default_env.conf"),
		directory(injectorConfigDir + "/conf.d"),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-injector.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "injector", "README.md"), "/usr/share/doc/opentelemetry-injector/README.md", 0o644),
	}

	return info, cleanup, nil
}

func javaInfo(cfg Config, format string) (*nfpm.Info, func(), error) {
	staging, err := os.MkdirTemp("", "otel-java-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	jarPath := filepath.Join(staging, "opentelemetry-javaagent.jar")
	if err := downloadJavaAgent(cfg, jarPath); err != nil {
		return nil, cleanup, fmt.Errorf("downloading Java agent: %w", err)
	}

	manPath, err := generateManPage(cfg, staging, "java")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	info := commonInfo(cfg, "opentelemetry-java-autoinstrumentation", javaDescription, "all")
	if format == "rpm" {
		info.Arch = "noarch"
	}
	info.Overridables.Provides = []string{"opentelemetry-java-autoinstrumentation1"}
	info.Overridables.Suggests = []string{"opentelemetry-injector1"}
	info.Overridables.Contents = files.Contents{
		regularFile(jarPath, javaInstallDir+"/opentelemetry-javaagent.jar", 0o644),
		configFile(filepath.Join(commonDir, "java", "otel-config.yaml"), javaConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "java", "injector.conf"), injectorConfigDir+"/conf.d/java.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-java.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "java", "README.md"), "/usr/share/doc/opentelemetry-java-autoinstrumentation/README.md", 0o644),
	}

	return info, cleanup, nil
}

func nodejsInfo(cfg Config, format string) (*nfpm.Info, func(), error) {
	staging, err := os.MkdirTemp("", "otel-nodejs-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	if err := downloadNodejsAgent(cfg, staging); err != nil {
		return nil, cleanup, fmt.Errorf("downloading Node.js agent: %w", err)
	}

	manPath, err := generateManPage(cfg, staging, "nodejs")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	info := commonInfo(cfg, "opentelemetry-nodejs-autoinstrumentation", nodejsDescription, "all")
	if format == "rpm" {
		info.Arch = "noarch"
	}
	info.Overridables.Provides = []string{"opentelemetry-nodejs-autoinstrumentation1"}
	info.Overridables.Suggests = []string{"opentelemetry-injector1"}
	info.Overridables.Contents = files.Contents{
		tree(filepath.Join(staging, "nodejs"), nodejsInstallDir),
		regularFile(filepath.Join(commonDir, "nodejs", "register.js"), nodejsInstallDir+"/register.js", 0o644),
		configFile(filepath.Join(commonDir, "nodejs", "otel-config.yaml"), nodejsConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "nodejs", "injector.conf"), injectorConfigDir+"/conf.d/nodejs.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-nodejs.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "nodejs", "README.md"), "/usr/share/doc/opentelemetry-nodejs-autoinstrumentation/README.md", 0o644),
	}

	return info, cleanup, nil
}

func dotnetInfo(cfg Config, format string) (*nfpm.Info, func(), error) {
	staging, err := os.MkdirTemp("", "otel-dotnet-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	dotnetDir := filepath.Join(staging, "dotnet")
	if err := os.MkdirAll(dotnetDir, 0o755); err != nil {
		return nil, cleanup, err
	}
	if err := downloadDotnetAgent(cfg, dotnetDir); err != nil {
		return nil, cleanup, fmt.Errorf("downloading .NET agent: %w", err)
	}

	manPath, err := generateManPage(cfg, staging, "dotnet")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	info := commonInfo(cfg, "opentelemetry-dotnet-autoinstrumentation", dotnetDescription, cfg.Arch)
	info.Overridables.Provides = []string{"opentelemetry-dotnet-autoinstrumentation1"}
	info.Overridables.Suggests = []string{"opentelemetry-injector1"}
	info.Overridables.Contents = files.Contents{
		tree(dotnetDir, dotnetInstallDir),
		configFile(filepath.Join(commonDir, "dotnet", "otel-config.yaml"), dotnetConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "dotnet", "injector.conf"), injectorConfigDir+"/conf.d/dotnet.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-dotnet.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "dotnet", "README.md"), "/usr/share/doc/opentelemetry-dotnet-autoinstrumentation/README.md", 0o644),
	}

	return info, cleanup, nil
}

func pythonInfo(cfg Config, format string) (*nfpm.Info, func(), error) {
	// sitecustomize.py invokes this validator when OTEL_CONFIG_FILE is set, so
	// that a broken declarative configuration self-deactivates instrumentation
	// instead of crashing the SDK's file configurator. It is cross-compiled
	// before the package build (Makefile target otel-config-check); fail fast
	// before the pip download when it is missing.
	if _, err := os.Stat(cfg.ConfigCheckBinary); err != nil {
		return nil, func() {}, fmt.Errorf(
			"otel-config-check binary not found at %q: build it with `make otel-config-check` or pass -config-check-binary: %w",
			cfg.ConfigCheckBinary, err)
	}

	staging, err := os.MkdirTemp("", "otel-python-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	pythonDir := filepath.Join(staging, "python")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		return nil, cleanup, err
	}
	if err := downloadPythonAgent(cfg, pythonDir); err != nil {
		return nil, cleanup, fmt.Errorf("downloading Python agent: %w", err)
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	// Copy sitecustomize.py into the package root alongside the installed packages.
	// Python executes this file automatically when the directory is on PYTHONPATH.
	if err := copyFile(
		filepath.Join(commonDir, "python", "sitecustomize.py"),
		filepath.Join(pythonDir, "sitecustomize.py"),
	); err != nil {
		return nil, cleanup, fmt.Errorf("copying sitecustomize.py: %w", err)
	}

	// Generate the dependency manifest from the installed dist-info directories.
	// sitecustomize.py reads this at runtime to detect version conflicts with the application.
	if err := generateAllDependencies(pythonDir, filepath.Join(pythonDir, "all-dependencies.txt")); err != nil {
		return nil, cleanup, fmt.Errorf("generating all-dependencies.txt: %w", err)
	}

	manPath, err := generateManPage(cfg, staging, "python")
	if err != nil {
		return nil, cleanup, err
	}

	// Python packages may include compiled C extensions, so the package is architecture-specific.
	info := commonInfo(cfg, "opentelemetry-python-autoinstrumentation", pythonDescription, cfg.Arch)
	info.Overridables.Provides = []string{"opentelemetry-python-autoinstrumentation1"}
	info.Overridables.Suggests = []string{"opentelemetry-injector1"}
	info.Overridables.Contents = files.Contents{
		// The injector resolves the agent path as <prefix>/<libc> (the same scheme
		// as .NET), so the bundle installs under a glibc/ subdirectory while the
		// conf.d prefix stays pythonInstallDir. The bundled wheels are glibc
		// manylinux; a musl/ variant would sit alongside for musl-based distros.
		tree(pythonDir, pythonInstallDir+"/glibc"),
		regularFile(cfg.ConfigCheckBinary, pythonInstallDir+"/otel-config-check", 0o755),
		configFile(filepath.Join(commonDir, "python", "otel-config.yaml"), pythonConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "python", "injector.conf"), injectorConfigDir+"/conf.d/python.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-python.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "python", "README.md"), "/usr/share/doc/opentelemetry-python-autoinstrumentation/README.md", 0o644),
		// The bundle redistributes files derived from the Dash0 operator; the
		// NOTICE at the repository root carries the attribution required by
		// Apache-2.0 and ships alongside the package documentation.
		regularFile(filepath.Join(filepath.Dir(cfg.PackagingDir), "NOTICE"), "/usr/share/doc/opentelemetry-python-autoinstrumentation/NOTICE", 0o644),
	}

	return info, cleanup, nil
}

func metaInfo(cfg Config, format string) (*nfpm.Info, func(), error) {
	staging, err := os.MkdirTemp("", "otel-meta-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	readmePath := filepath.Join(staging, "README")
	if err := os.WriteFile(readmePath, []byte("OpenTelemetry Auto-Instrumentation Suite\n"), 0o644); err != nil {
		return nil, cleanup, err
	}

	arch := "all"
	if format == "rpm" {
		arch = "noarch"
	}

	info := commonInfo(cfg, "opentelemetry", metaDescription, arch)
	info.Overridables.Depends = []string{"opentelemetry-injector1"}
	info.Overridables.Recommends = []string{
		"opentelemetry-java-autoinstrumentation1",
		"opentelemetry-nodejs-autoinstrumentation1",
		"opentelemetry-dotnet-autoinstrumentation1",
		"opentelemetry-python-autoinstrumentation1",
	}
	info.Overridables.Contents = files.Contents{
		regularFile(readmePath, "/usr/share/doc/opentelemetry/README", 0o644),
	}

	return info, cleanup, nil
}

// generateManPage expands @VERSION@ and @DATE@ placeholders in a man page
// template, compresses with gzip, and writes the result into stagingDir.
// Returns the path to the gzipped file.
func generateManPage(cfg Config, stagingDir, component string) (string, error) {
	templateName := fmt.Sprintf("opentelemetry-%s.8.tmpl", component)

	templatePath := filepath.Join(cfg.PackagingDir, "common", component, templateName)
	tmplData, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading man page template: %w", err)
	}

	content := string(tmplData)
	content = strings.ReplaceAll(content, "@VERSION@", cfg.Version)
	content = strings.ReplaceAll(content, "@DATE@", time.Now().Format("January 2006"))

	outPath := filepath.Join(stagingDir, templateName[:len(templateName)-5]+".gz")
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := gw.Write([]byte(content)); err != nil {
		return "", err
	}
	if err := gw.Close(); err != nil {
		return "", err
	}

	return outPath, nil
}
