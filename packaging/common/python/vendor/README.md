# The pyproto exporter chain

This directory contains four unpublished, pure-Python OpenTelemetry packages, developed in this repository:

- `opentelemetry-pyproto` — pure-Python protobuf encoding of the OTLP messages (no `google-protobuf` C extension), plus a shim exposing the encoding as a drop-in for `opentelemetry-proto` under the public `opentelemetry.proto` module path.
- `opentelemetry-exporter-otlp-pyproto-common` — shared OTLP encoders on top of `opentelemetry-pyproto`.
- `opentelemetry-exporter-otlp-pyproto-http` — the OTLP/HTTP exporter, transporting via `urllib` (no HTTP client dependency).
- `opentelemetry-exporter-otlp-pyproto-grpc` — the OTLP/gRPC exporter. It transports over its `_pygrpc/` subpackage, a pure-Python (stdlib-only) HTTP/2 and gRPC unary client, so it has no `grpcio` (or any C-extension) dependency. Its `api.py` presents the small grpc-python surface (`Compression`, `StatusCode`, `RpcError`, `insecure_channel`/`secure_channel`/`ssl_channel_credentials`) that `exporter.py` and the generated stubs consume. See `docs/plans/2026-07-14-001-feat-pyproto-grpc-without-grpcio-plan.md`.

They are the reason the Python auto-instrumentation bundle works on any manylinux target without compiled extensions.

## Drop-in design

Each package keeps its implementation under a private module path (`opentelemetry._proto`, `opentelemetry.exporter.otlp._proto.*`) and additionally ships a shim that re-exports it under the corresponding public path of the standard protobuf-based packages (`opentelemetry.proto`, `opentelemetry.exporter.otlp.proto.*`).
The exporters register the **standard** entry points (`otlp_proto_http`, `otlp_proto_grpc`), not pyproto-specific ones.

Consequences for this repository:

- `sitecustomize.py` defaults the exporter selection to `otlp_proto_http`.
- Declarative configuration works out of the box: the SDK's file configurator resolves the `otlp_http` exporter type through the public `opentelemetry.exporter.otlp.proto.http` module path, which the shim provides.
- The real `opentelemetry-exporter-otlp-proto-http` (and `opentelemetry-proto`) must **not** be bundled alongside: both distributions ship files at the same public module paths and clobber each other.

## Origin and destination

The packages were originally imported from [ocelotl/opentelemetry-python](https://github.com/ocelotl/opentelemetry-python) (a fork of [open-telemetry/opentelemetry-python](https://github.com/open-telemetry/opentelemetry-python)), branch `pyproto`, commit [`7755bc3a4d1d8c089d7528d33638e4757369f97d`](https://github.com/ocelotl/opentelemetry-python/tree/7755bc3a4d1d8c089d7528d33638e4757369f97d) (2026-07-10).
Since then they are developed **here**: this repository is the source of truth for the whole chain for the foreseeable future, and the fork is history, not a sync source.
The `_pygrpc` transport and its `tests_pygrpc/` suite were authored in this repository from the start.

The plan of record remains to donate the chain to `open-telemetry/opentelemetry-python` and consume a pinned PyPI release — see the TODO in [requirements.txt](../requirements.txt).
Until that happens, keeping the code here makes builds reproducible, changes reviewable in this repository, and avoids any build-time dependency on personal forks.

The code is copyrighted by The OpenTelemetry Authors and licensed under Apache-2.0, like this repository.

## Build integration

The build installs the three shipped packages from source in the second pass of `downloadPythonAgent` (`packaging/builder/download.go`), with `--no-deps`; their only dependencies beyond each other (`opentelemetry-api`, `opentelemetry-sdk`) are pinned in `requirements.txt`.
The gRPC exporter is installed the same way and ships alongside them, transporting over `_pygrpc` with no `grpcio` dependency.

## Testing

The test suites run in throwaway venvs, without containers:

```sh
make pyproto-unit-tests
```

Two venvs, because the suites' `conftest.py` guards make opposing demands:

- The **drop-in venv** installs only the pyproto packages (editable), so their shims own the public module paths, exactly like in the shipped bundle; it runs the http and grpc exporter unit tests and the `_pygrpc` protocol tests (`tests_pygrpc/`, cross-checked against the reference `hpack` package).
- The **equivalence venv** adds the real protobuf-based packages, which then own the shared public module paths; it runs the encoding tests and the equivalence suites, which compare the pure-Python encoding (private `_proto` paths) against the real `google-protobuf` one (public paths).

The guards detect which side owns a path by looking for "pyproto" in resolved file paths, which is also why the venv directory names must not contain that substring, and why the packages install editable.

Benchmarks in `opentelemetry-pyproto/tests` run through `pytest-benchmark` with `--benchmark-disable`, executing each benchmark once as a plain test.

The `_pygrpc` transport is additionally exercised end to end against `testutil/otelsink`, whose grpc-go server is the same stack as the Collector's OTLP receiver:

```sh
make pyproto-grpc-integration-tests
```

The shipped-bundle scenario — the shims owning the public paths — is covered end to end by the Python E2E suite (`make integration-test-deb-python`), which exercises the exporter through the otel-sink.

## Rules

- Keep the versions self-consistent: the four packages pin each other at their common development version (currently `1.44.0.dev`).
- Preserve the drop-in property: the public shims and the entry points must keep matching the standard protobuf-based packages' surface, or environment-variable and declarative configuration silently break.
- Keep changes upstreamable: OpenTelemetry Authors copyright, Apache-2.0, no dependencies beyond `opentelemetry-api`/`opentelemetry-sdk` and the standard library.
- Run all three test layers before shipping changes: `make pyproto-unit-tests`, `make pyproto-grpc-integration-tests`, and `make integration-test-deb-python`.
