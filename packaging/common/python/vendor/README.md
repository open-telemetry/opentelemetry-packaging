# Vendored pyproto exporter chain

This directory contains vendored copies of three unpublished, pure-Python OpenTelemetry packages:

- `opentelemetry-pyproto` — pure-Python protobuf encoding of the OTLP messages (no `google-protobuf` C extension).
- `opentelemetry-exporter-otlp-pyproto-common` — shared OTLP encoders on top of `opentelemetry-pyproto`.
- `opentelemetry-exporter-otlp-pyproto-http` — the OTLP/HTTP exporter, registered under the `otlp_pyproto_http` entry points and transporting via `urllib` (no HTTP client dependency).

They are the reason the Python auto-instrumentation bundle works on any manylinux target without compiled extensions.

## Provenance

Copied from [ocelotl/opentelemetry-python](https://github.com/ocelotl/opentelemetry-python) (a fork of [open-telemetry/opentelemetry-python](https://github.com/open-telemetry/opentelemetry-python)):

- Branch: `no-requests`.
- Commit: [`613710161555d13cd271efe5541305c8f3fb5692`](https://github.com/ocelotl/opentelemetry-python/tree/613710161555d13cd271efe5541305c8f3fb5692) (2026-07-09).
- Copied content: `src/`, `pyproject.toml`, and `README.rst` of each package; the upstream test suites are not copied (they require the fork's workspace tooling, and the exporter is covered end to end by this repository's Python E2E suite).

The code is copyrighted by The OpenTelemetry Authors and licensed under Apache-2.0, like this repository.

## Why vendored

The packages are not yet published to PyPI, and the plan of record is to donate them to `open-telemetry/opentelemetry-python` and consume a pinned release — see the TODO in [requirements.txt](../requirements.txt).
Until then, a vendored copy pins the exact code the package ships, keeps builds reproducible, and removes the build-time dependency on a personal fork branch that can change or disappear (the previous `git+…@no-requests` requirements tracked a moving branch).

The build installs these directories from source in the second pass of `downloadPythonAgent` (`packaging/builder/download.go`), with `--no-deps`; their only dependencies beyond each other (`opentelemetry-api`, `opentelemetry-sdk`) are pinned in `requirements.txt`.

## Rules

- **Do not edit the vendored files locally.**
  Fixes go to the upstream fork (or, once donated, to `open-telemetry/opentelemetry-python`) and arrive here through a sync.
- Keep the versions self-consistent: the three packages pin each other at their common development version (currently `1.43.0.dev`).

## Sync procedure

To import upstream changes:

1. Pick the new fork commit to sync to and review the diff since the commit recorded above, limited to the three package directories.
2. Re-copy `src/`, `pyproject.toml`, and `README.rst` of each package over the corresponding directory here (delete the old directory contents first so removed files do not linger).
3. Update the commit SHA and date in the "Provenance" section of this file.
4. Rebuild and test: `make deb-package-python` followed by the Python E2E and unit suites (`make integration-test-deb-python`, `make python-unit-tests`).
