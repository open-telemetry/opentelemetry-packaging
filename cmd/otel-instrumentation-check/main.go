// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

// otel-instrumentation-check is a post-install sanity check for the
// OpenTelemetry auto-instrumentation system packages. It confirms, without a
// running OTLP receiver and without restarting an application, that the
// injector is active and that the installed language agents are wired up
// correctly.
//
// The injector is not a daemon: it is a shared library loaded into every
// process through /etc/ld.so.preload, so there is no systemd unit whose status
// could report "instrumentation is on". This command instead inspects the same
// files the injector and its package lifecycle scripts read (/etc/ld.so.preload,
// the conf.d/ drop-ins, and default_env.conf) and reports what a newly started
// process would actually pick up.
//
// Usage: otel-instrumentation-check
//
// Exit codes: 0 the injector is active, at least one language agent is
// registered and present, and no running process needs a restart; 1 an
// installation problem was found (details are printed to stdout); 2 usage error;
// 3 the installation is correct but one or more already-running processes started
// before the package was installed and must be restarted before they are
// instrumented. Code 3 is distinct from 0 so a provisioning script can detect
// the "needs restart" case and act on it (for example, restart the affected
// systemd units) without treating it as an installation failure.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const usage = "usage: otel-instrumentation-check\n\n" +
	"Verifies that the OpenTelemetry injector is active and that the installed\n" +
	"language auto-instrumentation agents are wired up. Takes no arguments and\n" +
	"needs no OTLP receiver.\n\n" +
	"Exit codes:\n" +
	"  0  the injector is active, an agent is present, and no running process\n" +
	"     needs a restart\n" +
	"  1  an installation problem was found\n" +
	"  2  usage error\n" +
	"  3  the installation is correct but one or more already-running processes\n" +
	"     started before install and must be restarted to be instrumented"

// Exit codes. 0 (success) is Go's implicit default and is not named here.
const (
	exitInstallProblem = 1 // an installation problem was found
	exitUsage          = 2 // usage error
	exitNeedsRestart   = 3 // install OK, but running processes must be restarted
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help":
			fmt.Println(usage)
			return
		default:
			fmt.Fprintln(os.Stderr, usage)
			os.Exit(exitUsage)
		}
	}

	results, ok := checkInstallation(systemLayout())
	processResults, needsRestart := describeRunningProcesses(scanRunningProcesses(systemProcLayout()))
	results = append(results, processResults...)

	fmt.Println("OpenTelemetry auto-instrumentation post-install check")
	fmt.Println()
	for _, r := range results {
		fmt.Println(r.format())
	}
	fmt.Println()

	// An installation problem outranks a needs-restart: if the injector is not
	// wired up, restarting a process would not help, so report the install
	// failure first.
	if !ok {
		fmt.Println("Result: FAIL. See the items marked [fail] above.")
		os.Exit(exitInstallProblem)
	}
	if needsRestart {
		fmt.Println("Result: ACTION NEEDED. The injector is active, but some already-running " +
			"processes started before install and must be restarted to be instrumented; see the [warn] items above.")
		os.Exit(exitNeedsRestart)
	}
	fmt.Println("Result: PASS. The injector is active; newly started processes will be instrumented.")
}

// layout locates the files the check inspects. The defaults are the FHS paths
// the packages install to; tests point the fields at a temporary tree.
type layout struct {
	preloadFile  string // /etc/ld.so.preload
	injectorLib  string // the injector shared library the preload file must list
	injectorConf string // injector.conf, which may relocate the default env file
	confDir      string // conf.d/, holding one drop-in per installed language
	defaultEnv   string // default_env.conf, the OTEL_* variables applied to all agents
}

// systemLayout returns the layout for a real installation.
func systemLayout() layout {
	return layout{
		preloadFile:  "/etc/ld.so.preload",
		injectorLib:  "/usr/lib/opentelemetry/injector/libotelinject.so",
		injectorConf: "/etc/opentelemetry/injector/injector.conf",
		confDir:      "/etc/opentelemetry/injector/conf.d",
		defaultEnv:   "/etc/opentelemetry/injector/default_env.conf",
	}
}

// status is the severity of a single check result.
type status int

const (
	statusOK status = iota
	statusInfo
	statusWarn
	statusFail
)

// checkResult is one line of the report.
type checkResult struct {
	status  status
	message string
}

// format renders a result as a grep-friendly, fixed-width-marker line.
func (r checkResult) format() string {
	marker := map[status]string{
		statusOK:   "[ ok ]",
		statusInfo: "[info]",
		statusWarn: "[warn]",
		statusFail: "[fail]",
	}[r.status]
	return marker + " " + r.message
}

// checkInstallation runs every check against the given layout and reports the
// per-item results together with an overall pass/fail. The install passes when
// the injector library is present, the preload file lists it, and at least one
// language agent is registered with its files present on disk.
func checkInstallation(l layout) (results []checkResult, ok bool) {
	injectorPresent := isRegularFile(l.injectorLib)
	if injectorPresent {
		results = append(results, checkResult{statusOK, "injector library present: " + l.injectorLib})
	} else {
		results = append(results, checkResult{statusFail,
			"injector library missing: " + l.injectorLib + " (reinstall the opentelemetry-injector package)"})
	}

	active := preloadListsInjector(l.preloadFile, l.injectorLib)
	if active {
		results = append(results, checkResult{statusOK, "injector active in " + l.preloadFile})
	} else {
		results = append(results, checkResult{statusFail,
			"injector not listed in " + l.preloadFile + "; new processes are NOT instrumented"})
	}

	agentCount, agentResults := checkRegisteredAgents(l.confDir)
	results = append(results, agentResults...)

	// injector.conf may relocate the default environment file; honor it before
	// reporting the telemetry destination.
	if envPath := parseKeyValueFile(l.injectorConf)["all_auto_instrumentation_agents_env_path"]; envPath != "" {
		l.defaultEnv = envPath
	}
	results = append(results, describeTelemetryDestination(l.defaultEnv)...)

	ok = injectorPresent && active && agentCount > 0
	return results, ok
}

// preloadListsInjector reports whether the preload file lists the injector
// library. It field-splits every line on whitespace, matching how the dynamic
// linker and the postinstall script treat /etc/ld.so.preload: several entries
// may share a line, and only an exact path field counts.
func preloadListsInjector(preloadFile, injectorLib string) bool {
	data, err := os.ReadFile(preloadFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		for _, field := range strings.Fields(line) {
			if field == injectorLib {
				return true
			}
		}
	}
	return false
}

// agentKey describes a conf.d setting that registers a language agent, how its
// value resolves to an on-disk path, and the language it names.
type agentKey struct {
	key      string
	language string
	// prefix is true when the value is a path prefix under which the injector
	// resolves the libc-specific subdirectory (glibc on every DEB/RPM target),
	// rather than a direct path to the agent file.
	prefix bool
}

// knownAgentKeys lists the conf.d settings this check understands, mirroring
// the drop-in files each language package installs.
var knownAgentKeys = []agentKey{
	{"jvm_auto_instrumentation_agent_path", "Java", false},
	{"nodejs_auto_instrumentation_agent_path", "Node.js", false},
	{"dotnet_auto_instrumentation_agent_path_prefix", ".NET", true},
	{"python_auto_instrumentation_agent_path_prefix", "Python", true},
}

// checkRegisteredAgents inspects the conf.d drop-ins and reports, per language,
// whether the registered agent path exists on disk. It returns the number of
// agents found present, so the caller can require at least one.
func checkRegisteredAgents(confDir string) (count int, results []checkResult) {
	drops, err := filepath.Glob(filepath.Join(confDir, "*.conf"))
	if err != nil || len(drops) == 0 {
		return 0, []checkResult{{statusFail,
			"no language agents registered in " + confDir +
				" (install a language package, e.g. opentelemetry-python-autoinstrumentation)"}}
	}
	sort.Strings(drops)

	for _, drop := range drops {
		settings := parseKeyValueFile(drop)
		base := filepath.Base(drop)
		for _, ak := range knownAgentKeys {
			value, present := settings[ak.key]
			if !present {
				continue
			}
			resolved := value
			if ak.prefix {
				// The injector prepends the libc subdirectory; glibc is the only
				// flavor these packages ship for DEB and RPM targets.
				resolved = filepath.Join(value, "glibc")
			}
			if pathExists(resolved) {
				count++
				results = append(results, checkResult{statusOK,
					ak.language + " agent registered (" + base + "): " + resolved})
			} else {
				results = append(results, checkResult{statusFail,
					ak.language + " agent registered (" + base + ") but its files are missing: " + resolved})
			}
		}
	}

	if len(results) == 0 {
		results = append(results, checkResult{statusFail,
			"no recognized language agents registered in " + confDir})
	}
	return count, results
}

// describeTelemetryDestination reports where a newly instrumented process would
// send its telemetry, so the user knows what to expect without standing up a
// backend. It never fails the check: the destination is a configuration choice,
// not an installation defect.
func describeTelemetryDestination(defaultEnv string) []checkResult {
	env := parseKeyValueFile(defaultEnv)
	var results []checkResult

	if configFile, declarative := env["OTEL_CONFIG_FILE"]; declarative {
		if isRegularFile(configFile) {
			results = append(results, checkResult{statusOK,
				"declarative configuration active: OTEL_CONFIG_FILE=" + configFile +
					" (validate it with otel-config-check)"})
		} else {
			results = append(results, checkResult{statusWarn,
				"OTEL_CONFIG_FILE=" + configFile + " is set but the file is missing;" +
					" instrumentation falls back to environment-variable configuration"})
		}
	}

	endpoint, set := env["OTEL_EXPORTER_OTLP_ENDPOINT"]
	if !set {
		endpoint = "http://localhost:4317 (gRPC) or http://localhost:4318 (HTTP), the default"
	}
	results = append(results, checkResult{statusInfo,
		"telemetry destination: " + endpoint +
			". No OTLP receiver is needed for this check, but one must be reachable for telemetry to arrive."})
	return results
}

// parseKeyValueFile reads a KEY=VALUE file (injector.conf, default_env.conf, or
// a conf.d drop-in), skipping blank lines and comments. Later entries win, as
// they do for the injector.
func parseKeyValueFile(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		out[strings.TrimSpace(line[:eq])] = strings.TrimSpace(line[eq+1:])
	}
	return out
}

// isRegularFile reports whether path is an existing regular file.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// pathExists reports whether path exists (of any type).
func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
