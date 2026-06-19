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
	dotnetDescription   = "OpenTelemetry .NET Automatic Instrumentation (glibc + musl)"
	metaDescription     = "OpenTelemetry Auto-Instrumentation Suite (metapackage)"
)

func injectorInfo(cfg Config, format string) (*nfpm.Info, error) {
	// Download libotelinject.so to a staging directory.
	staging, err := os.MkdirTemp("", "otel-injector-*")
	if err != nil {
		return nil, err
	}
	// Caller (Build) will package and we're done; staging is temp.

	soPath := filepath.Join(staging, "libotelinject.so")
	if err := downloadInjector(cfg, soPath); err != nil {
		return nil, fmt.Errorf("downloading injector: %w", err)
	}

	manPath, err := generateManPage(cfg, "injector")
	if err != nil {
		return nil, err
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

	return info, nil
}

func javaInfo(cfg Config, format string) (*nfpm.Info, error) {
	staging, err := os.MkdirTemp("", "otel-java-*")
	if err != nil {
		return nil, err
	}

	jarPath := filepath.Join(staging, "opentelemetry-javaagent.jar")
	if err := downloadJavaAgent(cfg, jarPath); err != nil {
		return nil, fmt.Errorf("downloading Java agent: %w", err)
	}

	manPath, err := generateManPage(cfg, "java")
	if err != nil {
		return nil, err
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

	return info, nil
}

func nodejsInfo(cfg Config, format string) (*nfpm.Info, error) {
	staging, err := os.MkdirTemp("", "otel-nodejs-*")
	if err != nil {
		return nil, err
	}

	if err := downloadNodejsAgent(cfg, staging); err != nil {
		return nil, fmt.Errorf("downloading Node.js agent: %w", err)
	}

	manPath, err := generateManPage(cfg, "nodejs")
	if err != nil {
		return nil, err
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
		configFile(filepath.Join(commonDir, "nodejs", "otel-config.yaml"), nodejsConfigDir+"/otel-config.yaml"),
		regularFile(filepath.Join(commonDir, "nodejs", "injector.conf"), injectorConfigDir+"/conf.d/nodejs.conf", 0o644),
		regularFile(manPath, "/usr/share/man/man8/opentelemetry-nodejs.8.gz", 0o644),
		regularFile(filepath.Join(commonDir, "nodejs", "README.md"), "/usr/share/doc/opentelemetry-nodejs-autoinstrumentation/README.md", 0o644),
	}

	return info, nil
}

func dotnetInfo(cfg Config, format string) (*nfpm.Info, error) {
	staging, err := os.MkdirTemp("", "otel-dotnet-*")
	if err != nil {
		return nil, err
	}

	dotnetDir := filepath.Join(staging, "dotnet")
	if err := os.MkdirAll(dotnetDir, 0o755); err != nil {
		return nil, err
	}
	if err := downloadDotnetAgent(cfg, dotnetDir); err != nil {
		return nil, fmt.Errorf("downloading .NET agent: %w", err)
	}

	manPath, err := generateManPage(cfg, "dotnet")
	if err != nil {
		return nil, err
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

	return info, nil
}

func metaInfo(cfg Config, format string) (*nfpm.Info, error) {
	// The metapackage has no files of its own besides a README.
	staging, err := os.MkdirTemp("", "otel-meta-*")
	if err != nil {
		return nil, err
	}

	readmePath := filepath.Join(staging, "README")
	if err := os.WriteFile(readmePath, []byte("OpenTelemetry Auto-Instrumentation Suite\n"), 0o644); err != nil {
		return nil, err
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
	}
	info.Overridables.Contents = files.Contents{
		regularFile(readmePath, "/usr/share/doc/opentelemetry/README", 0o644),
	}

	return info, nil
}

// generateManPage expands @VERSION@ and @DATE@ placeholders in a man page
// template and compresses with gzip. Returns the path to the gzipped file.
func generateManPage(cfg Config, component string) (string, error) {
	var templateName string
	switch component {
	case "injector":
		templateName = "opentelemetry-injector.8.tmpl"
	case "java":
		templateName = "opentelemetry-java.8.tmpl"
	case "nodejs":
		templateName = "opentelemetry-nodejs.8.tmpl"
	case "dotnet":
		templateName = "opentelemetry-dotnet.8.tmpl"
	default:
		return "", fmt.Errorf("unknown component for man page: %s", component)
	}

	templatePath := filepath.Join(cfg.PackagingDir, "common", component, templateName)
	tmplData, err := os.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("reading man page template: %w", err)
	}

	content := string(tmplData)
	content = strings.ReplaceAll(content, "@VERSION@", cfg.Version)
	content = strings.ReplaceAll(content, "@DATE@", time.Now().Format("January 2006"))

	outPath := filepath.Join(os.TempDir(), templateName[:len(templateName)-5]+".gz")
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
