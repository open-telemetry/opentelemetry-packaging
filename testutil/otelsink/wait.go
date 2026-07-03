// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otelsink

import (
	"testing"
	"time"
)

// pollInterval is how often the wait helpers re-read the signal files.
const pollInterval = 200 * time.Millisecond

// WaitForTraces polls until cond is satisfied by the current traces view or the
// timeout elapses, in which case it fails the test. It returns the view that
// satisfied cond. Use this instead of fixed sleeps: exporters flush on a batch
// schedule, so telemetry arrives asynchronously.
func (s *Sink) WaitForTraces(t *testing.T, timeout time.Duration, cond func(*Traces) bool) *Traces {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		tr := s.Traces(t)
		if cond(tr) {
			return tr
		}
		if time.Now().After(deadline) {
			t.Fatalf("otelsink: timed out after %s waiting for traces (test.id=%s, %d spans seen)", timeout, s.testID, tr.Len())
		}
		time.Sleep(pollInterval)
	}
}

// WaitForMetrics polls until cond is satisfied by the current metrics view or the
// timeout elapses, in which case it fails the test.
func (s *Sink) WaitForMetrics(t *testing.T, timeout time.Duration, cond func(*Metrics) bool) *Metrics {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		m := s.Metrics(t)
		if cond(m) {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("otelsink: timed out after %s waiting for metrics (test.id=%s, %d metrics seen)", timeout, s.testID, m.Len())
		}
		time.Sleep(pollInterval)
	}
}

// WaitForLogs polls until cond is satisfied by the current logs view or the
// timeout elapses, in which case it fails the test.
func (s *Sink) WaitForLogs(t *testing.T, timeout time.Duration, cond func(*Logs) bool) *Logs {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		l := s.Logs(t)
		if cond(l) {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatalf("otelsink: timed out after %s waiting for logs (test.id=%s, %d records seen)", timeout, s.testID, l.Len())
		}
		time.Sleep(pollInterval)
	}
}

// NonEmpty is a convenience condition: at least one item is present.
func NonEmpty[T interface{ Len() int }](v T) bool { return v.Len() > 0 }
