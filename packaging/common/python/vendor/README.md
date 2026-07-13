# Vendored pyproto exporter chain

This directory contains vendored copies of four unpublished, pure-Python OpenTelemetry packages:

- `opentelemetry-pyproto` — pure-Python protobuf encoding of the OTLP messages (no `google-protobuf` C extension), plus a shim exposing the encoding as a drop-in for `opentelemetry-proto` under the public `opentelemetry.proto` module path.
- `opentelemetry-exporter-otlp-pyproto-common` — shared OTLP encoders on top of `opentelemetry-pyproto`.
- `opentelemetry-exporter-otlp-pyproto-http` — the OTLP/HTTP exporter, transporting via `urllib` (no HTTP client dependency).
- `opentelemetry-exporter-otlp-pyproto-grpc` — the OTLP/gRPC exporter. **Vendored for completeness, not shipped**: it depends on `grpcio`, a C extension, which would break the pure-Python property of the bundle.

They are the reason the Python auto-instrumentation bundle works on any manylinux target without compiled extensions.

## Drop-in design

Each package keeps its implementation under a private module path (`opentelemetry._proto`, `opentelemetry.exporter.otlp._proto.*`) and additionally ships a shim that re-exports it under the corresponding public path of the standard protobuf-based packages (`opentelemetry.proto`, `opentelemetry.exporter.otlp.proto.*`).
The exporters register the **standard** entry points (`otlp_proto_http`, `otlp_proto_grpc`), not pyproto-specific ones.

Consequences for this repository:

- `sitecustomize.py` defaults the exporter selection to `otlp_proto_http`.
- Declarative configuration works out of the box: the SDK's file configurator resolves the `otlp_http` exporter type through the public `opentelemetry.exporter.otlp.proto.http` module path, which the shim provides.
- The real `opentelemetry-exporter-otlp-proto-http` (and `opentelemetry-proto`) must **not** be bundled alongside: both distributions ship files at the same public module paths and clobber each other.

## Provenance

Copied from [ocelotl/opentelemetry-python](https://github.com/ocelotl/opentelemetry-python) (a fork of [open-telemetry/opentelemetry-python](https://github.com/open-telemetry/opentelemetry-python)):

- Branch: `pyproto`.
- Commit: [`7755bc3a4d1d8c089d7528d33638e4757369f97d`](https://github.com/ocelotl/opentelemetry-python/tree/7755bc3a4d1d8c089d7528d33638e4757369f97d) (2026-07-10).
- Copied content: `src/`, `tests/`, `equivalence_tests/` (where present), `pyproject.toml`, and `README.rst` of each package.

The code is copyrighted by The OpenTelemetry Authors and licensed under Apache-2.0, like this repository.

## Why vendored

The packages are not yet published to PyPI, and the plan of record is to donate them to `open-telemetry/opentelemetry-python` and consume a pinned release — see the TODO in [requirements.txt](../requirements.txt).
Until then, a vendored copy pins the exact code the package ships, keeps builds reproducible, and removes the build-time dependency on a personal fork branch that can change or disappear.

The build installs the three shipped directories from source in the second pass of `downloadPythonAgent` (`packaging/builder/download.go`), with `--no-deps`; their only dependencies beyond each other (`opentelemetry-api`, `opentelemetry-sdk`) are pinned in `requirements.txt`.

## Testing

The vendored test suites run in a throwaway venv, without containers:

```sh
make pyproto-unit-tests
```

The target installs the vendored packages first and the real protobuf-based packages (`opentelemetry-proto`, `opentelemetry-exporter-otlp-proto-http`, `opentelemetry-exporter-otlp-proto-grpc`) afterwards, so the real packages own the shared public module paths.
The `conftest.py` guards in the upstream suites assert that ordering: the equivalence tests compare the pure-Python encoding (imported via the private `_proto` paths) against the real `google-protobuf` encoding (imported via the public paths), so the public paths must resolve to the real packages.

Benchmarks in `opentelemetry-pyproto/tests` run through `pytest-benchmark` with `--benchmark-disable`, executing each benchmark once as a plain test.

The shipped-bundle scenario — the shims owning the public paths — is covered end to end by the Python E2E suite (`make integration-test-deb-python`), which exercises the exporter through the otel-sink.

## Rules

- **Do not edit the vendored files locally.**
  Fixes go to the upstream fork (or, once donated, to `open-telemetry/opentelemetry-python`) and arrive here through a sync.
- Keep the versions self-consistent: the four packages pin each other at their common development version (currently `1.44.0.dev`).

## Sync procedure

To import upstream changes:

1. Pick the new fork commit to sync to and review the diff since the commit recorded above, limited to the four package directories.
2. Re-copy `src/`, `tests/`, `equivalence_tests/`, `pyproject.toml`, and `README.rst` of each package over the corresponding directory here (delete the old directory contents first so removed files do not linger).
3. Update the commit SHA and date in the "Provenance" section of this file.
4. Rebuild and test: `make pyproto-unit-tests`, `make python-unit-tests`, `make deb-package-python`, and `make integration-test-deb-python`.
