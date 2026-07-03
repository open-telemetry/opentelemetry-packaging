---
date: 2026-07-03
topic: otel-sink
---

# otel-sink: file-backed OTLP sink and Go assertion API for integration tests

## Summary

Add an in-process Go "otel-sink" for the integration tests: it receives OTLP over gRPC and HTTP, writes one file per signal (traces, metrics, logs), and exposes a Go API to query and wait on that telemetry using the official OTLP protobuf types.
Each test tags its telemetry with a unique resource attribute so parallel tests never cross-contaminate.
It replaces today's console-exporter-plus-string-matching approach and becomes the foundation the later VM and injector-activation tests assert against.

## Problem Frame

The integration tests never send telemetry over the wire.
Every language workload uses the console exporter and writes spans to stdout; the tests then copy a log file out of the container and run `assert.Contains` on the raw JSON (see `packaging/tests/deb/python/python_test.go` and `testutil.WaitForFileContaining`).
String matching on serialized JSON is brittle, order-sensitive, and can't distinguish a real span attribute from an incidental substring.

The assertion structs in `testutil/testutil.go` are hand-rolled and lossy: traces only, and `AttributeValue` models just `stringValue`/`intValue` — no bool, double, array, or kvlist, and no metrics or logs at all.
As the effort moves toward a shared VM where multiple tests' telemetry lands in one place, "which test produced this span?" has no answer today.
The sink resolves all three at once: real wire export, lossless typed assertions, and per-test attribution.

## Key Decisions

- **In-process Go OTLP receiver, not a real Collector.** It fits the repo's pure-Go, no-external-tooling ethos, keeps telemetry as typed structs for lossless assertions, and gives trivial per-test isolation. We are testing the agents and injector, not the Collector, so Collector fidelity buys little.
- **Files are the source of truth for assertions.** The receiver writes per-signal files and the assertion API reads them. This yields one assertion code path that works identically whether the receiver runs in-process (container tests) or as a host process a VM exports to later. Any in-memory retention is only a polling optimization.
- **Official OTLP protobuf Go types replace the hand-rolled structs.** Assertions parse into `go.opentelemetry.io/proto/otlp/...` types — lossless across all value kinds and all three signals. The structs in `testutil/testutil.go` are retired.
- **Per-test identity via a dedicated `test.id` resource attribute.** Each workload sets a unique `test.id` (test name plus a short random suffix) through `OTEL_RESOURCE_ATTRIBUTES`; the sink scopes every query to it. `service.name` stays realistic for assertions, and the mechanism still works when one shared sink serves a VM.
- **Incremental delivery, sink-first.** The sink lands in one commit, validated end to end with Python as the first proof language. Remaining languages migrate one per commit off the console exporter onto the sink.

## Requirements

### Receiver

- R1. Provide an in-process Go OTLP receiver accepting both gRPC (port 4317) and HTTP (port 4318) protobuf.
- R2. Each test instantiates its own receiver on an ephemeral port, with lifecycle bound to the test (started at test start, torn down on cleanup).

### Signal capture and file output

- R3. The receiver captures all three signals: traces, metrics, and logs.
- R4. It writes one file per signal; each line is a single OTLP Export request serialized as protojson (JSONL). These files are the durable source of truth for assertions.
- R5. Files are written to a per-test directory so parallel tests' files never collide.

### Test isolation

- R6. Each workload stamps a unique `test.id` resource attribute, derived from the test name plus a short random suffix, via `OTEL_RESOURCE_ATTRIBUTES`.
- R7. Every query through the assertion API is scoped to the owning test's `test.id`; telemetry from other tests is invisible even when the sink is shared.

### Assertion API

- R8. Assertion helpers parse the per-signal files into official OTLP protobuf Go types, replacing the hand-rolled structs in `testutil/testutil.go`.
- R9. Provide query helpers for all three signals — traces (by resource attribute, scope, span name, kind, attributes), metrics (by name, type, datapoint attributes), logs (by body, severity, attributes) — lossless across all attribute value kinds.
- R10. Provide a wait/poll primitive that blocks until telemetry matching a matcher arrives or a timeout elapses, replacing substring polling.

### Workloads

- R11. Test workloads export through a real OTLP exporter (http/protobuf) instead of the console exporter, and each workload emits all three signals.
- R12. Every language exercised against the sink has one test per signal (traces, metrics, logs) so all API helpers are covered.

## Key Flows

- F1. Sink-backed test lifecycle.
  - **Trigger:** A test starts.
  - **Steps:** The test starts its receiver on an ephemeral port and derives a unique `test.id`; the workload is launched with the OTLP endpoint and `test.id` in its environment; the workload generates activity and exports traces/metrics/logs; the receiver appends each signal to its per-signal file; the test waits until the expected telemetry is present, then asserts against the typed API scoped to its `test.id`.
  - **Outcome:** Assertions run on lossless, test-scoped telemetry read from the per-signal files.
  - **Covers:** R1, R2, R4, R6, R7, R10.

## Acceptance Examples

- AE1. Asynchronous arrival.
  - **Covers R10.** Given a workload that flushes on a batch schedule, when the test waits for a span matching a name, then the wait primitive returns it once it arrives and fails only after the timeout — no fixed sleep.
- AE2. Parallel isolation.
  - **Covers R7.** Given two tests running in parallel against their own receivers, when each queries for its spans, then each sees only telemetry carrying its own `test.id`.
- AE3. Lossless per-signal query.
  - **Covers R9.** Given a workload that emits a histogram metric and a log record, when the test queries by metric name and by log severity, then it reads the histogram datapoints and the log body with correct typed values — not a substring match.

## Scope Boundaries

- The VM harness and injector-driven Python activation tests — this sink is their enabler and lands first; they are the next effort.
- A real OpenTelemetry Collector as the sink, and any Collector-configuration testing.
- Migrating all four language tests in this first commit; migration is incremental, one language per commit after the sink lands.

## Dependencies / Assumptions

- Container-to-host reachability: a workload running in a container must reach the receiver's port on the host (`host.docker.internal`, and Podman-on-macOS quirks). This is the main implementation risk; the exact mechanism is deferred to planning.
- A dependency on the official OTLP protobuf Go module is added for the typed assertion layer.
- Workloads flush on a batch schedule (the tests already set `OTEL_BSP_SCHEDULE_DELAY`); the wait primitive must tolerate export delay.

## Outstanding Questions

Deferred to planning:

- The concrete container-to-host networking mechanism, verified across Docker and Podman (including macOS).
- Whether negative assertions ("no telemetry of kind X") are in the first API and, if so, what settle window they use.
- Whether the receiver retains parsed telemetry in memory as a polling fast path, or the assertion API always re-reads files.
