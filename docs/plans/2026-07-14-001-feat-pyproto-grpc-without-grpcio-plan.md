# Plan: pure-Python gRPC transport for the pyproto OTLP/gRPC exporter

## Status

Phases 0–4 implemented (2026-07-15). `_pygrpc` (HPACK, HTTP/2 framing, unary client) lives in the pyproto-grpc package with 63 unit tests in `tests_pygrpc/`; `exporter.py` runs on it via the `api.py` shim (grpcio dropped from `pyproject.toml`); the exporter is in `requirements.txt` and ships in the bundle (built `.deb` verified free of grpcio and google-protobuf); `sitecustomize.py` accepts `grpc` (the default when protocol is unset) and `http/protobuf`; `otel-config-check` accepts `otlp_grpc`.
Validated end to end: `make pyproto-grpc-integration-tests` against otelsink (grpc-go), the deb and rpm `TestPythonGRPC` E2E scenarios, and the installed package exporting over gRPC+TLS to Dash0's production ingress (`SpanExportResult.SUCCESS`) in both env-var and declarative-config modes.
Remaining: phase 5 (upstream `_pygrpc` with the pyproto donation). http/json encoding is still a separate follow-up (item 20).

## Goal

Remove the `grpcio` dependency from `opentelemetry-exporter-otlp-pyproto-grpc` so the whole pyproto exporter chain is pure Python, and then ship the gRPC exporter in the Python auto-instrumentation bundle.
This is item 19 of the PR #18 follow-up list (`docs/notes/2026-07-10-pr18-review-todo.md`).

## Context and constraints

- The pyproto chain exists so the bundle works on any manylinux target without compiled extensions; `grpcio` is a C extension and breaks that property.
- The whole pyproto chain is developed in this repository (`packaging/common/python/vendor/`) for the foreseeable future; upstreaming happens later, as part of the donation to `open-telemetry/opentelemetry-python`.
- gRPC runs over HTTP/2 (binary framing, HPACK header compression, trailers). `urllib` speaks HTTP/1.1 and cannot carry it; "on top of urllib" in practice means "on top of the standard library": `socket`, `ssl` (ALPN `h2` is supported by stdlib), `zlib` for gzip compression, and hand-written HTTP/2 framing.
- gRPC-Web (an HTTP/1.1-compatible envelope) is rejected: plain OTLP/gRPC servers, including the Collector, do not accept it without a proxy, so it would not be a drop-in.
- Only unary calls are needed (OTLP export services are unary), which shrinks the HTTP/2 client to a manageable subset: no server push, no long-lived streams, no priorities.

## grpcio surface to replace

The exporter uses a narrow slice of grpcio (`exporter.py`):

- `insecure_channel` / `secure_channel` / `ssl_channel_credentials` / `ChannelCredentials` — connection setup, TLS with optional custom CA, client cert and key.
- Generated `*ServiceStub` classes — one unary call per signal with `timeout=` and `metadata=`.
- `Compression` — none or gzip.
- `RpcError` + `StatusCode` — error handling and the retryable-status set (`CANCELLED`, `DEADLINE_EXCEEDED`, `RESOURCE_EXHAUSTED`, `ABORTED`, `OUT_OF_RANGE`, `UNAVAILABLE`, `DATA_LOSS`).

There is no RetryInfo parsing and no streaming; the retry policy is driven purely by the status code.

## Dependency strategy (decision needed)

1. **Stdlib-only, hand-rolled (recommended).**
   Implement minimal HTTP/2 framing and an HPACK codec in a private module.
   No new distributions land in the bundle, so the sitecustomize version-conflict guard gains no new conflict surface — important because the obvious alternative (`h2`/`hpack` from PyPI) is commonly present in applications via `httpx`/`hypercorn`, and a bundled copy would collide with application pins.
2. **Fallback: embed the `hpack` codec.**
   HPACK (RFC 7541, including Huffman and the dynamic table on the decode side) is the riskiest part.
   If the hand-rolled codec stalls, embed the MIT-licensed `hpack` sources inside the private `_proto` namespace (not as a separate distribution) and hand-roll only the framing and connection layer.
3. **Rejected: depend on `h2`/`hpack` from PyPI.**
   Pure-Python and portable, but adds top-level distributions that conflict with application packages, and adds a dependency discussion to the upstream donation.

HPACK scope note: the encoder can legally use only static-table and literal-never-indexed encodings (no dynamic table, no Huffman), which is trivial; the decoder must be complete because the server chooses its own encodings.

## Architecture

New private package inside the grpc exporter at `opentelemetry/exporter/otlp/_proto/grpc/_pygrpc/`, developed like the rest of the chain in `packaging/common/python/vendor/opentelemetry-exporter-otlp-pyproto-grpc/`:

- `frames.py` — HTTP/2 frame encode/decode (SETTINGS, HEADERS, DATA, WINDOW_UPDATE, RST_STREAM, GOAWAY, PING).
- `hpack.py` — encoder (static-table subset) and full decoder (dynamic table, Huffman).
- `connection.py` — socket + TLS (ALPN `h2`) or cleartext with HTTP/2 prior knowledge for `insecure=True`; connection preface, SETTINGS exchange, flow-control accounting, GOAWAY handling with one reconnect.
- `client.py` — `unary_call(service, method, request_bytes, metadata, timeout, compression)`: gRPC length-prefixed message framing, `grpc-timeout` header, gzip via `zlib`, response DATA reassembly, trailers (including trailers-only responses), returns `(status_code, message, response_bytes)`.
- `api.py` — the drop-in shims the exporter consumes: `StatusCode`, `RpcError`, `Compression`, `secure_channel`/`insecure_channel`/`ssl_channel_credentials` equivalents keeping the exporter diff minimal.

The exporter keeps its public API (endpoint, headers, timeout, compression, certificate options, `insecure`) and its entry points; `pyproject.toml` drops the `grpcio` dependency rows.

## Testing strategy

The reference server for every integration-level test is this repository's `testutil/otelsink`.
It embeds `google.golang.org/grpc` (grpc-go) and the `go.opentelemetry.io/proto/otlp` collector service definitions — the same gRPC stack and OTLP service surface as the OpenTelemetry Collector's OTLP receiver — so passing against `otelsink` is passing against the actual Collector code path, not against a lookalike.

Three test layers, from cheapest to most end-to-end:

1. **Protocol unit tests (`tests_pygrpc/`, pure Python, no server).**
   HPACK against the RFC 7541 appendix C vectors and cross-checked against the reference `hpack` package; frame codec round-trips; gRPC message framing; status and trailer parsing; error-to-retryable mapping.
2. **Transport integration tests (host-side, no containers).**
   The Go harness (`make pyproto-grpc-integration-tests`, `packaging/tests/pyprotogrpc/`) starts `otelsink` in-process, drives the exporter against `sink.GRPCEndpoint()`, and asserts on what the sink actually received.
   This layer also carries the failure-mode matrix (trailers-only responses, non-OK `grpc-status`, GOAWAY mid-call, RST_STREAM, flow-control exhaustion on payloads larger than the initial 64 KiB window, DATA split at max frame size, deadline expiry, gzip round-trip) — where a failure mode cannot be provoked through `otelsink`, a misbehaving stub server in the harness fills the gap, but acceptance is always defined against `otelsink`.
3. **Transport equivalence (host-side).**
   Export identical telemetry through the pure-Python client and through the real grpcio exporter (test venv only) to `otelsink`, and compare the received protos — parity measured at the server, not at the encoder.

Green against `otelsink` is the merge bar.

## Phases

### Phase 0: spikes (cheap de-risking)

- Confirm stdlib `ssl` ALPN `h2` negotiation against the Collector and Dash0 ingress from the oldest supported interpreter (3.10).
- Confirm cleartext prior-knowledge HTTP/2 against `otelsink`'s gRPC listener (Go gRPC servers accept prior knowledge).
- Wire-capture one grpcio export and replay the frames by hand to validate the framing understanding.

### Phase 1: HPACK codec

- Encoder and decoder with unit tests against the RFC 7541 appendix C test vectors, plus round-trip property tests against captured server header blocks.

### Phase 2: framing and unary client

- Frame codec unit tests (round-trip, malformed input).
- Build the `otelsink` harness (testing strategy layer 2) and develop the connection and client code against it from the start.
- Cover the failure-mode matrix through the harness, with stub servers only where `otelsink` cannot provoke the failure.

### Phase 3: exporter integration

- Swap the grpcio imports for `_pygrpc.api`; keep the retryable-status mapping table.
- Adapt the existing 78 unit tests (channel mocks become transport mocks) and keep the encoding equivalence tests unchanged.
- Run the transport equivalence layer (testing strategy layer 3): pure client versus grpcio exporter, compared at `otelsink`.

### Phase 4: ship it

- Drop the "not shipped" caveat from `vendor/README.md`.
- Add `./vendor/opentelemetry-exporter-otlp-pyproto-grpc` to `requirements.txt`.
- Relax the sitecustomize protocol guard to accept `grpc` again, and update `otel-config-check` to accept `otlp_grpc`.
- Wire `pyproto-grpc-integration-tests` into the CI unit-tests job alongside `pyproto-unit-tests`.
- E2E: new Python scenario exporting via `OTEL_EXPORTER_OTLP_PROTOCOL=grpc` to `otelsink`'s gRPC listener (deb and rpm), plus a declarative-config variant once `otlp_grpc` validates.
- Update the package README, man page, `all-dependencies` expectations, metadata tests, CONTRIBUTING, and the design doc dependency notes.
- Manual live smoke against Dash0's gRPC ingress (repeat of the 2026-07-14 probe, now without grpcio in the venv).

### Phase 5: upstreaming

- The `_pygrpc` module rides along with the eventual pyproto donation to `open-telemetry/opentelemetry-python`; the test-vector suites and the otelsink-backed integration evidence carry the review.

## Risks

- **HPACK decoder correctness** — highest risk; mitigated by RFC vectors, captured-traffic tests, and the embed-`hpack` fallback.
- **Flow control deadlocks** — unary-only and modest payload sizes keep this small; tests must still cover a payload larger than the initial 64 KiB window.
- **Server diversity** — Go gRPC (Collector, otelsink), C-core servers, and cloud load balancers differ in SETTINGS and GOAWAY behavior; the interop matrix in phases 2 and 4 covers Go and one real ingress, and anything exotic is post-ship hardening.
- **Behavioral parity with grpcio** — deadline semantics and status mapping are covered by porting the existing unit tests rather than rewriting them.

## Effort estimate

Roughly 1.5k–2k lines of implementation (HPACK is about half) plus 2–3k lines of tests, all in this repository.
Phase 4 is the ship-it wiring; phase 5 waits on the donation timeline.
