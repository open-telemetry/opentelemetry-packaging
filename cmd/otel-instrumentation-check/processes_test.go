// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeProc writes a minimal /proc/<pid> directory: comm, cmdline (NUL-joined),
// and maps. It lets the scan run against a controlled process table without a
// real system.
func fakeProc(t *testing.T, procRoot string, pid int, comm string, argv []string, maps string) {
	t.Helper()
	dir := filepath.Join(procRoot, strconv.Itoa(pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte(comm+"\n"), 0o644))

	var cmdline []byte
	for _, a := range argv {
		cmdline = append(cmdline, []byte(a)...)
		cmdline = append(cmdline, 0)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), cmdline, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "maps"), []byte(maps), 0o644))
}

func TestScanClassifiesMonitoredAndNeedsRestart(t *testing.T) {
	procRoot := t.TempDir()
	injectorLib := "/usr/lib/opentelemetry/injector/libotelinject.so"

	// A Java process started after install: injector mapped -> monitored.
	fakeProc(t, procRoot, 100, "java", []string{"/usr/bin/java", "-jar", "app.jar"},
		"7f000000-7f001000 r-xp 00000000 00:00 0 "+injectorLib+"\n")
	// A Python process started before install: injector not mapped -> restart.
	fakeProc(t, procRoot, 200, "python3", []string{"/usr/bin/python3", "server.py"},
		"7f000000-7f001000 r-xp 00000000 00:00 0 /usr/lib/libc.so.6\n")
	// A non-runtime process: ignored entirely.
	fakeProc(t, procRoot, 300, "bash", []string{"/bin/bash"}, "")

	scan := scanRunningProcesses(procLayout{procRoot: procRoot, injectorLib: injectorLib})
	require.False(t, scan.procUnreadable)
	require.False(t, scan.permissionGaps)
	require.Len(t, scan.processes, 2, "only the two supported-runtime processes are considered")

	byPID := map[int]runningProcess{}
	for _, p := range scan.processes {
		byPID[p.pid] = p
	}
	require.Equal(t, procMonitored, byPID[100].state)
	require.Equal(t, "Java", byPID[100].runtime)
	require.Equal(t, procNeedsRestart, byPID[200].state)
	require.Equal(t, "Python", byPID[200].runtime)
}

func TestDescribeRunningProcessesReportsBothSetsAndSignalsRestart(t *testing.T) {
	scan := procScan{processes: []runningProcess{
		{pid: 100, comm: "java", runtime: "Java", state: procMonitored},
		{pid: 200, comm: "python3", runtime: "Python", state: procNeedsRestart},
	}}
	results, needsRestart := describeRunningProcesses(scan)
	require.True(t, needsRestart, "a process needing a restart must be signaled to the caller")
	msg := messages(results)
	require.Contains(t, msg, "1 monitored, 1 not monitored")
	require.Contains(t, msg, "NOT monitored: pid 200")
	require.Contains(t, msg, "restart it to instrument")
	require.Contains(t, msg, "monitored: pid 100")
}

func TestDescribeRunningProcessesAllMonitoredDoesNotSignalRestart(t *testing.T) {
	scan := procScan{processes: []runningProcess{
		{pid: 100, comm: "java", runtime: "Java", state: procMonitored},
	}}
	_, needsRestart := describeRunningProcesses(scan)
	require.False(t, needsRestart, "when every process is monitored, no restart is needed")
}

func TestScanSkipsUnreadableProcAndDoesNotPanic(t *testing.T) {
	scan := scanRunningProcesses(procLayout{procRoot: "/nonexistent-proc", injectorLib: "x"})
	require.True(t, scan.procUnreadable)
	require.Nil(t, scan.processes)
	results, needsRestart := describeRunningProcesses(scan)
	require.False(t, needsRestart)
	require.Contains(t, messages(results), "scan skipped")
}

func TestPermissionGapAddsRunAsRootHint(t *testing.T) {
	// A permission gap is reported to the user as a hint to re-run as root, and
	// it must not, on its own, count as a needs-restart.
	scan := procScan{permissionGaps: true}
	results, needsRestart := describeRunningProcesses(scan)
	require.False(t, needsRestart)
	require.Contains(t, messages(results), "run as root for full coverage")
}

// TestUnreadableMapsFileIsNotAPermissionGap pins that a process whose maps file
// is simply gone (the process exited mid-scan) is treated as not monitored,
// without raising the run-as-root hint reserved for true permission denials.
func TestUnreadableMapsFileIsNotAPermissionGap(t *testing.T) {
	procRoot := t.TempDir()
	injectorLib := "/usr/lib/opentelemetry/injector/libotelinject.so"

	// Build a supported-runtime process but leave out its maps file so reading
	// it fails with a not-exist error rather than a permission error.
	dir := filepath.Join(procRoot, "400")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "comm"), []byte("node\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cmdline"), []byte("node\x00"), 0o644))

	scan := scanRunningProcesses(procLayout{procRoot: procRoot, injectorLib: injectorLib})
	require.False(t, scan.permissionGaps, "a missing maps file is not a permission denial")
	require.Len(t, scan.processes, 1)
	require.Equal(t, procNeedsRestart, scan.processes[0].state)
}
