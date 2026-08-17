// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// This file answers the question issue #61 ultimately asks: of the applications
// running right now, which are already being monitored and which are not (and
// therefore must be restarted to pick up the instrumentation).
//
// The injector is the shared library listed in /etc/ld.so.preload. The dynamic
// linker reads that file only at process startup, so a process started before
// the package was installed never mapped the injector and never will until it
// restarts. The ground-truth, restart-detecting signal is therefore whether
// libotelinject.so is currently mapped into the process address space, which is
// readable at /proc/<pid>/maps. This does not require a restart, an OTLP
// receiver, or the injector's own logs.
//
// Semantic limitation: a mapped libotelinject.so proves the injector ran in the
// process, not that the language SDK finished attaching and is healthy. The
// injector could have run and the agent could still have failed to initialize
// for a runtime-specific reason. This scan is therefore the best signal
// available today, but it is a proxy for "monitored", not a guarantee. The
// report wording says so, and a future per-process SDK health mechanism would be
// needed for a definitive answer.
//
// Runtime-detection limitation: a process is matched to a runtime by a substring
// of its command name (/proc/<pid>/comm) and argv[0] (/proc/<pid>/cmdline). This
// is intentionally simple. It can misfire (a wrapper binary literally named
// "java-foo" matches "java") or miss an interpreter reached only through exec
// with an unrelated argv[0]. The report notes this so a surprising entry is
// understood as a detection artifact, not a defect.

// procState is how a running candidate process relates to the injector.
type procState int

const (
	// procMonitored: the injector library is mapped into the process, so it
	// started after the package was installed and is being instrumented.
	procMonitored procState = iota
	// procNeedsRestart: the process runs a supported runtime but the injector
	// is not mapped, so it started before install and must be restarted.
	procNeedsRestart
)

// runningProcess is one candidate process the scan considered.
type runningProcess struct {
	pid     int
	comm    string // the process's command name, for a human-readable report
	runtime string // the supported runtime detected (Java, Node.js, .NET, Python)
	state   procState
}

// procScan is the outcome of walking /proc: the classified candidate processes,
// whether /proc itself was unreadable (so the scan could not run at all), and
// whether at least one candidate process could not be read for lack of
// permission (so the report can suggest running as root for full coverage).
type procScan struct {
	processes      []runningProcess
	procUnreadable bool // /proc could not be listed; the scan did not run
	permissionGaps bool // a candidate's maps could not be read (needs root)
}

// procLayout locates the /proc tree and the injector library name the scan
// looks for. Tests point procRoot at a fake /proc built under a temp directory.
type procLayout struct {
	procRoot    string // /proc
	injectorLib string // /usr/lib/opentelemetry/injector/libotelinject.so
}

// systemProcLayout returns the process-scan layout for a real system.
func systemProcLayout() procLayout {
	return procLayout{
		procRoot:    "/proc",
		injectorLib: "/usr/lib/opentelemetry/injector/libotelinject.so",
	}
}

// runtimeMatchers maps a supported runtime to the substrings that identify it
// in a process's command name or executable path. These mirror the runtimes the
// injector itself detects and instruments.
var runtimeMatchers = []struct {
	runtime string
	needles []string
}{
	{"Java", []string{"java"}},
	{"Node.js", []string{"node", "nodejs"}},
	{".NET", []string{"dotnet"}},
	{"Python", []string{"python"}},
}

// scanRunningProcesses walks /proc and returns every process running a
// supported runtime, classified as already-monitored or needing a restart. It
// skips processes it cannot read (the process exited mid-scan, or its maps are
// owned by another user and this check is not running as root): the scan is
// best-effort by design and must never fail the overall check because of an
// unreadable process it does not own. A permission-denied skip is recorded so
// the report can advise running as root for full coverage.
func scanRunningProcesses(l procLayout) procScan {
	entries, err := os.ReadDir(l.procRoot)
	if err != nil {
		return procScan{procUnreadable: true}
	}

	var scan procScan
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue // not a PID directory
		}

		runtime, ok := detectRuntime(l.procRoot, pid)
		if !ok {
			continue // not a supported runtime; not our concern
		}

		mapped, err := injectorMapped(l.procRoot, pid, l.injectorLib)
		if errors.Is(err, os.ErrPermission) {
			// The process is a supported runtime, but its maps are owned by
			// another user and cannot be read without root. Record the gap so
			// the report can suggest root, and skip classifying this process
			// rather than mislabel it as needing a restart.
			scan.permissionGaps = true
			continue
		}
		state := procNeedsRestart
		if mapped {
			state = procMonitored
		}
		scan.processes = append(scan.processes, runningProcess{
			pid:     pid,
			comm:    readComm(l.procRoot, pid),
			runtime: runtime,
			state:   state,
		})
	}

	sort.Slice(scan.processes, func(i, j int) bool {
		return scan.processes[i].pid < scan.processes[j].pid
	})
	return scan
}

// detectRuntime reports which supported runtime a process runs, if any, by
// matching its command name (/proc/<pid>/comm) and its argv[0]
// (/proc/<pid>/cmdline) against the known runtime needles. This is the simple,
// documented substring match described in the file header.
func detectRuntime(procRoot string, pid int) (runtime string, ok bool) {
	haystack := strings.ToLower(readComm(procRoot, pid))
	if argv0 := firstCmdlineField(procRoot, pid); argv0 != "" {
		haystack += "\x00" + strings.ToLower(filepath.Base(argv0))
	}
	for _, m := range runtimeMatchers {
		for _, needle := range m.needles {
			if strings.Contains(haystack, needle) {
				return m.runtime, true
			}
		}
	}
	return "", false
}

// injectorMapped reports whether the injector library is currently mapped into
// the process address space, which is the definitive proof that the process
// started with the injector preloaded and is therefore being instrumented. A
// permission error is returned to the caller (rather than swallowed) so the scan
// can distinguish "not mapped" from "could not be read without root".
func injectorMapped(procRoot string, pid int, injectorLib string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "maps"))
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return false, err
		}
		// The process exited mid-scan, or maps is otherwise gone; treat it as
		// not mapped without flagging a permission gap.
		return false, nil
	}
	return strings.Contains(string(data), injectorLib), nil
}

// readComm returns the process command name from /proc/<pid>/comm, or an empty
// string if it cannot be read.
func readComm(procRoot string, pid int) string {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// firstCmdlineField returns argv[0] from /proc/<pid>/cmdline, whose fields are
// NUL-separated, or an empty string if it cannot be read.
func firstCmdlineField(procRoot string, pid int) string {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	if i := strings.IndexByte(string(data), 0); i >= 0 {
		return string(data[:i])
	}
	return string(data)
}

// describeRunningProcesses turns the scan into report lines and reports whether
// any process needs a restart. It emits one summary line, then one line per
// process that needs a restart (the actionable set the user asked for), then one
// line per already-monitored process; when the scan could not read some
// processes without root it adds a hint to re-run as root. It never fails the
// overall check: a process that predates the install is a fact for the user to
// act on, not an installation defect (the caller maps needsRestart to a distinct
// exit code instead).
func describeRunningProcesses(scan procScan) (results []checkResult, needsRestart bool) {
	if scan.procUnreadable {
		return []checkResult{{statusInfo,
			"running-process scan skipped: /proc is not readable on this system"}}, false
	}

	var monitored, needRestart []runningProcess
	for _, p := range scan.processes {
		if p.state == procMonitored {
			monitored = append(monitored, p)
		} else {
			needRestart = append(needRestart, p)
		}
	}

	results = []checkResult{{statusInfo, fmt.Sprintf(
		"running processes on supported runtimes: %d monitored, %d not monitored (restart required). "+
			"A mapped injector proves the injector ran, not that the SDK is healthy; runtime detection is a best-effort name match.",
		len(monitored), len(needRestart))}}

	for _, p := range needRestart {
		results = append(results, checkResult{statusWarn, fmt.Sprintf(
			"NOT monitored: pid %d (%s, %s) started before install; restart it to instrument (e.g. systemctl restart the owning unit)",
			p.pid, p.comm, p.runtime)})
	}
	for _, p := range monitored {
		results = append(results, checkResult{statusOK, fmt.Sprintf(
			"monitored: pid %d (%s, %s)", p.pid, p.comm, p.runtime)})
	}

	if scan.permissionGaps {
		results = append(results, checkResult{statusInfo,
			"some processes could not be read; run as root for full coverage"})
	}

	return results, len(needRestart) > 0
}
