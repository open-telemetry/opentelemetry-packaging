// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package builder

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/goreleaser/nfpm/v2/files"
)

// Architectures are the target architectures package builds support. Callers
// that accept an architecture from configuration or a flag validate against
// this rather than keeping their own copy of the list.
var Architectures = []string{"amd64", "arm64"}

// SupportedArchitecture reports whether arch is one of Architectures.
func SupportedArchitecture(arch string) bool {
	return slices.Contains(Architectures, arch)
}

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
	PackageName: "opentelemetry-injector",
	Description: injectorDescription,
	Relations: Relations{
		Provides: []string{"opentelemetry-injector1"},
	},
	PostInstall:  "postinstall-injector.sh",
	PreRemove:    "preuninstall-injector.sh",
	ContentsFunc: injectorContents,
}

// Java is the opentelemetry-java-autoinstrumentation package component.
var Java = Component{
	Name:        "java",
	PackageName: "opentelemetry-java-autoinstrumentation",
	Description: javaDescription,
	Noarch:      true,
	Relations: Relations{
		Provides: []string{"opentelemetry-java-autoinstrumentation1"},
		Suggests: []string{"opentelemetry-injector1"},
	},
	ContentsFunc: javaContents,
}

// Nodejs is the opentelemetry-nodejs-autoinstrumentation package component.
var Nodejs = Component{
	Name:        "nodejs",
	PackageName: "opentelemetry-nodejs-autoinstrumentation",
	Description: nodejsDescription,
	Noarch:      true,
	Relations: Relations{
		Provides: []string{"opentelemetry-nodejs-autoinstrumentation1"},
		Suggests: []string{"opentelemetry-injector1"},
	},
	ContentsFunc: nodejsContents,
}

// Dotnet is the opentelemetry-dotnet-autoinstrumentation package component.
var Dotnet = Component{
	Name:        "dotnet",
	PackageName: "opentelemetry-dotnet-autoinstrumentation",
	Description: dotnetDescription,
	Relations: Relations{
		Provides: []string{"opentelemetry-dotnet-autoinstrumentation1"},
		Suggests: []string{"opentelemetry-injector1"},
	},
	ContentsFunc: dotnetContents,
}

// Python is the opentelemetry-python-autoinstrumentation package component.
var Python = Component{
	Name:        "python",
	PackageName: "opentelemetry-python-autoinstrumentation",
	Description: pythonDescription,
	// Not Noarch: the bundled wheels may carry compiled C extensions, so the
	// package is architecture-specific.
	Relations: Relations{
		Provides: []string{"opentelemetry-python-autoinstrumentation1"},
		Suggests: []string{"opentelemetry-injector1"},
	},
	ContentsFunc: pythonContents,
}

// Meta is the opentelemetry metapackage component.
var Meta = Component{
	Name:        "meta",
	PackageName: "opentelemetry",
	Description: metaDescription,
	Noarch:      true,
	Relations: Relations{
		Depends: []string{"opentelemetry-injector1"},
		Recommends: []string{
			"opentelemetry-java-autoinstrumentation1",
			"opentelemetry-nodejs-autoinstrumentation1",
			"opentelemetry-dotnet-autoinstrumentation1",
			"opentelemetry-python-autoinstrumentation1",
		},
	},
	ContentsFunc: metaContents,
}

const (
	injectorDescription = "OpenTelemetry LD_PRELOAD-based automatic instrumentation injector"
	javaDescription     = "OpenTelemetry Java Auto-Instrumentation Agent"
	nodejsDescription   = "OpenTelemetry Node.js Auto-Instrumentation"
	dotnetDescription   = "OpenTelemetry .NET Automatic Instrumentation"
	pythonDescription   = "OpenTelemetry Python Auto-Instrumentation"
	metaDescription     = "OpenTelemetry Auto-Instrumentation Suite (metapackage)"
)

func injectorContents(cfg Config) (files.Contents, func(), error) {
	staging, err := os.MkdirTemp("", "otel-injector-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	soPath := filepath.Join(staging, "libotelinject.so")
	if err := downloadInjector(cfg, soPath); err != nil {
		return nil, cleanup, fmt.Errorf("downloading injector: %w", err)
	}

	manPath, err := componentManPage(cfg, staging, "injector")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	return files.Contents{
		regularFile(soPath, injectorInstallDir+"/libotelinject.so", 0o755),
		configFile(filepath.Join(commonDir, "injector", "injector.conf"), injectorConfigDir+"/injector.conf"),
		configFile(filepath.Join(commonDir, "injector", "default_env.conf"), injectorConfigDir+"/default_env.conf"),
		directory(injectorConfigDir + "/conf.d"),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-injector.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "injector", "README.md"), "/usr/share/doc/opentelemetry-injector/README.md", 0o644),
	}, cleanup, nil
}

func javaContents(cfg Config) (files.Contents, func(), error) {
	staging, err := os.MkdirTemp("", "otel-java-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	jarPath := filepath.Join(staging, "opentelemetry-javaagent.jar")
	if err := downloadJavaAgent(cfg, jarPath); err != nil {
		return nil, cleanup, fmt.Errorf("downloading Java agent: %w", err)
	}

	manPath, err := componentManPage(cfg, staging, "java")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	return files.Contents{
		regularFile(jarPath, javaInstallDir+"/opentelemetry-javaagent.jar", 0o644),
		configFile(filepath.Join(commonDir, "java", "otel-config.yaml"), javaConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "java", "injector.conf"), injectorConfigDir+"/conf.d/java.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-java.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "java", "README.md"), "/usr/share/doc/opentelemetry-java-autoinstrumentation/README.md", 0o644),
	}, cleanup, nil
}

func nodejsContents(cfg Config) (files.Contents, func(), error) {
	staging, err := os.MkdirTemp("", "otel-nodejs-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	if err := downloadNodejsAgent(cfg, staging); err != nil {
		return nil, cleanup, fmt.Errorf("downloading Node.js agent: %w", err)
	}

	manPath, err := componentManPage(cfg, staging, "nodejs")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	return files.Contents{
		tree(filepath.Join(staging, "nodejs"), nodejsInstallDir),
		regularFile(filepath.Join(commonDir, "nodejs", "register.js"), nodejsInstallDir+"/register.js", 0o644),
		configFile(filepath.Join(commonDir, "nodejs", "otel-config.yaml"), nodejsConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "nodejs", "injector.conf"), injectorConfigDir+"/conf.d/nodejs.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-nodejs.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "nodejs", "README.md"), "/usr/share/doc/opentelemetry-nodejs-autoinstrumentation/README.md", 0o644),
	}, cleanup, nil
}

func dotnetContents(cfg Config) (files.Contents, func(), error) {
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

	manPath, err := componentManPage(cfg, staging, "dotnet")
	if err != nil {
		return nil, cleanup, err
	}

	commonDir := filepath.Join(cfg.PackagingDir, "common")

	return files.Contents{
		tree(dotnetDir, dotnetInstallDir),
		configFile(filepath.Join(commonDir, "dotnet", "otel-config.yaml"), dotnetConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "dotnet", "injector.conf"), injectorConfigDir+"/conf.d/dotnet.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-dotnet.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "dotnet", "README.md"), "/usr/share/doc/opentelemetry-dotnet-autoinstrumentation/README.md", 0o644),
	}, cleanup, nil
}

func pythonContents(cfg Config) (files.Contents, func(), error) {
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
	if err := writeSitecustomize(
		filepath.Join(commonDir, "python", "sitecustomize.py"),
		filepath.Join(pythonDir, "sitecustomize.py"),
		supportedPythonVersions,
	); err != nil {
		return nil, cleanup, fmt.Errorf("writing sitecustomize.py: %w", err)
	}

	// Generate the dependency manifest from the installed dist-info directories.
	// sitecustomize.py reads this at runtime to detect version conflicts with the application.
	for _, pythonVersion := range supportedPythonVersions {
		interpreterDir := filepath.Join(pythonDir, pythonABITag(pythonVersion))
		if err := generateAllDependencies(
			[]string{pythonDir, interpreterDir},
			filepath.Join(interpreterDir, "all-dependencies.txt"),
		); err != nil {
			return nil, cleanup, fmt.Errorf("generating all-dependencies.txt for Python %s: %w", pythonVersion, err)
		}
	}

	manPath, err := componentManPage(cfg, staging, "python")
	if err != nil {
		return nil, cleanup, err
	}

	return files.Contents{
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
		regularFile(cfg.noticeFile(), "/usr/share/doc/opentelemetry-python-autoinstrumentation/NOTICE", 0o644),
	}, cleanup, nil
}

func metaContents(cfg Config) (files.Contents, func(), error) {
	staging, err := os.MkdirTemp("", "otel-meta-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(staging) }

	readmePath := filepath.Join(staging, "README")
	if err := os.WriteFile(readmePath, []byte("OpenTelemetry Auto-Instrumentation Suite\n"), 0o644); err != nil {
		return nil, cleanup, err
	}

	return files.Contents{
		regularFile(readmePath, "/usr/share/doc/opentelemetry/README", 0o644),
	}, cleanup, nil
}

// manPageTemplate locates a component's man page template in this
// repository's layout: packaging/common/<component>/opentelemetry-<component>.8.tmpl.
func manPageTemplate(cfg Config, component string) string {
	return filepath.Join(cfg.PackagingDir, "common", component,
		fmt.Sprintf("opentelemetry-%s.8.tmpl", component))
}

// upstreamVersion returns the version of the upstream agent a component ships.
//
// This is deliberately not cfg.Version. That is the distribution packaging
// version, and the two move independently: the same agent is republished
// whenever the packaging itself changes, and package images version their DEBs
// by the upstream pin instead. A man page reporting cfg.Version therefore says
// something different about the same agent depending on which build produced
// it, which is what "opentelemetry-java-autoinstrumentation <version>" reads as
// to anyone running man(1).
func upstreamVersion(cfg Config, component string) (string, error) {
	if component == "python" {
		// Python ships no single upstream artifact: its payload is a set of
		// wheels pinned in requirements.txt. opentelemetry-distro is the
		// auto-instrumentation distribution itself -- the direct analogue of
		// the Java agent or the .NET tracer -- so its pin is the version to
		// report.
		return pinnedRequirement(
			filepath.Join(cfg.PackagingDir, "common", "python", "requirements.txt"),
			"opentelemetry-distro",
		)
	}
	version, err := readReleaseVersion(filepath.Join(cfg.PackagingDir, "common", component, "release.txt"))
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(version, "v"), nil
}

// pinnedRequirement returns the version a requirements file pins for name with
// the "name==version" form, ignoring any extras in brackets.
func pinnedRequirement(path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pkg, version, ok := strings.Cut(line, "==")
		if !ok {
			continue
		}
		if base, _, found := strings.Cut(pkg, "["); found {
			pkg = base
		}
		if strings.TrimSpace(pkg) == name {
			return strings.TrimSpace(version), nil
		}
	}
	return "", fmt.Errorf("no %s==<version> pin in %s", name, path)
}

// componentManPage renders a component's man page with its upstream version.
func componentManPage(cfg Config, stagingDir, component string) (string, error) {
	upstream, err := upstreamVersion(cfg, component)
	if err != nil {
		return "", fmt.Errorf("resolving upstream version for %s: %w", component, err)
	}
	return GenerateManPage(cfg, stagingDir, manPageTemplate(cfg, component),
		map[string]string{"@UPSTREAM_VERSION@": upstream})
}

// GenerateManPage expands @VERSION@ and @DATE@ placeholders in the man page
// template at templatePath, compresses it with gzip, and writes the result
// into stagingDir. It returns the path to the gzipped file.
//
// @VERSION@ is the packaging version. Callers that also want to report the
// version of the upstream software the package ships pass it through
// substitutions, which apply after the built-in placeholders; the
// OpenTelemetry components use it for @UPSTREAM_VERSION@.
//
// The template is addressed by path rather than derived from a component name,
// so a caller is free to name its templates whatever its packages are called.
// The output file keeps the template's own name minus the .tmpl suffix.
func GenerateManPage(cfg Config, stagingDir, templatePath string, substitutions ...map[string]string) (string, error) {
	templateName := filepath.Base(templatePath)
	if !strings.HasSuffix(templateName, ".tmpl") {
		return "", fmt.Errorf("man page template %q must end in .tmpl", templateName)
	}

	tmplData, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading man page template: %w", err)
	}

	content := string(tmplData)
	content = strings.ReplaceAll(content, "@VERSION@", cfg.Version)
	content = strings.ReplaceAll(content, "@DATE@", time.Now().Format("January 2006"))
	for _, set := range substitutions {
		for placeholder, value := range set {
			content = strings.ReplaceAll(content, placeholder, value)
		}
	}

	outPath := filepath.Join(stagingDir, strings.TrimSuffix(templateName, ".tmpl")+".gz")
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
