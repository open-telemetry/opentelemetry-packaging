# Contributing to OpenTelemetry Packaging

## Prerequisites

- **Go 1.26+** (package builds and tests are pure Go)
- **npm** (needed to fetch the Node.js auto-instrumentation agent from the npm registry)
- **Python 3 with pip** (needed to fetch the Python auto-instrumentation packages; invoked as `python3 -m pip`)
- **A container engine** (Podman or Docker — needed for local repository generation and integration tests)

No Ruby, FPM, or special Docker images are required to build packages.

## Repository layout

```
cmd/build-packages/          CLI entry point for building .deb and .rpm packages
cmd/otel-config-check/       Declarative-config validator shipped inside the Python package
packaging/
  builder/                   Go library that drives nfpm to create packages
    builder.go               Build orchestration, common metadata
    components.go            Per-component definitions (injector, java, nodejs, dotnet, python, meta)
    download.go              Upstream artifact download helpers
  common/                    Shared assets referenced by the builder
    otel-config.yaml         Declarative configuration file, shipped by every language package (valid as shipped)
    scripts/                 POSIX lifecycle scripts (postinstall, preuninstall)
    injector/                Config files, man page template, README, release.txt (version pin)
    java/                    "
    nodejs/                  " (plus register.js, the --require entry point with declarative-config support)
    dotnet/                  "
    python/                  Config, man page template, README, requirements.txt (version pins), sitecustomize.py (plus its unit tests)
      vendor/                The pyproto exporter chain, developed here with its test suites (unpublished pure-Python packages; see its README)
  repo/                      APT and YUM repository generation scripts
  tests/                     Integration tests
    metadata/                       Host-side metadata validation (no containers needed)
    pyprotogrpc/                    Pure-Python gRPC transport tests against the otel-sink (host-side)
    {python,java,nodejs,dotnet}/    Matrix E2E tests (deb+rpm × base images) asserting via the otel-sink
                                    (python/ also hosts the sitecustomize.py interpreter compatibility tests)
    lifecycle/                      Package lifecycle tests (preload scripts, config handling, install/remove scenarios)
    vendor/                         Vendor replacement tests (plus mkvendor/, the mock acme package builder)
    shared/                         Shared test application sources
testutil/                    Shared Go test helpers
  otelsink/                  In-process OTLP sink + typed assertion API for E2E tests
docs/design/                 Architecture and design documents
```

## How package builds work

Packages are created using the [nfpm](https://github.com/goreleaser/nfpm) Go library.
The `cmd/build-packages` program:

1. **Downloads upstream artifacts** from their respective release channels:
   - `libotelinject.so` from [opentelemetry-injector](https://github.com/open-telemetry/opentelemetry-injector) GitHub Releases
   - Java agent JAR from [opentelemetry-java-instrumentation](https://github.com/open-telemetry/opentelemetry-java-instrumentation) GitHub Releases
   - Node.js agent from npm (`@opentelemetry/auto-instrumentations-node`)
   - .NET agent from [opentelemetry-dotnet-instrumentation](https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation) GitHub Releases (glibc only; musl-based distributions use apk, which this project does not build)
   - Python packages via `pip`, as defined by `packaging/common/python/requirements.txt`

   The Python package bundles compiled C extensions, so its wheels are fetched
   for a fixed target architecture and Python version (`targetPythonVersion` in
   `download.go`) rather than for the build host. PyPI requirements are installed
   binary-only (manylinux wheels for the target arch); unpublished pure-Python
   requirements — the pyproto exporter chain developed under
   `packaging/common/python/vendor/` (see its README for provenance) — are built
   from source in a second pass and merged in. This keeps the produced package
   correct regardless of the build host's OS, architecture, or Python version.

2. **Constructs an `nfpm.Info`** for each component with the correct metadata:
   - `Provides` virtual package names (e.g., `opentelemetry-injector1`)
   - `Suggests` for soft dependencies between language packages and the injector
   - `Recommends` for the metapackage's language package references
   - Lifecycle scripts, config files, man pages, and documentation

3. **Writes the package file** via nfpm's `Packager.Package()` — produces valid `.deb` or `.rpm` without requiring `dpkg-deb`, `rpmbuild`, or any platform-specific tools.

### Upstream version pins

Each upstream artifact version is pinned in a `packaging/common/<component>/release.txt` file (Python instead pins its packages in `packaging/common/python/requirements.txt`):

```
# renovate: datasource=github-releases depName=open-telemetry/opentelemetry-java-instrumentation
v2.16.0
```

[Renovate](https://docs.renovatebot.com/) automatically opens PRs when upstream releases are published.

## Building packages

Build all packages (DEB + RPM) for amd64:

```sh
make packages
```

Build a single format:

```sh
make deb-packages
```

```sh
make rpm-packages
```

Build a single component, for example the injector DEB or the Java RPM:

```sh
make deb-package-injector
```

```sh
make rpm-package-java
```

Specify version and architecture:

```sh
make packages VERSION=1.0.0 ARCH=arm64
```

Under the hood, `make packages` runs:

```sh
go run ./cmd/build-packages -version <VERSION> -arch <ARCH> -format all -output build/packages
```

The Python package ships the `otel-config-check` validator, which the make targets cross-compile beforehand (`make otel-config-check`, producing `build/bin/otel-config-check-<arch>`).
When invoking `build-packages` directly for the Python component, build that binary first, or point `-config-check-binary` at one.

## Testing

### Go command unit tests (fast, no containers)

Unit tests for the Go commands, currently the `otel-config-check` declarative-configuration validator that ships inside the Python package.

```sh
make go-unit-tests
```

### Python sitecustomize unit tests (fast, no containers)

Unit tests for the guard logic in `packaging/common/python/sitecustomize.py` (version gate, protocol and configuration-file checks, double-instrumentation detection, dependency-conflict checking).
They run in a throwaway virtualenv under `build/`, so the host Python is untouched.

```sh
make python-unit-tests
```

### Pyproto exporter tests (fast, no containers)

The test suites of the pyproto packages developed under `packaging/common/python/vendor/`.
They run in two throwaway virtualenvs under `build/`: a drop-in venv where the pyproto shims own the public `opentelemetry.exporter.otlp.proto.*` module paths (like in the shipped bundle), and an equivalence venv where the real protobuf-based packages own those paths and the suites compare the pure-Python encoding against the real `google-protobuf` one.
See [the vendor README](packaging/common/python/vendor/README.md) for the details.

```sh
make pyproto-unit-tests
```

### Pure-Python gRPC transport tests (fast, no containers)

Transport-level tests for the grpcio-free gRPC client (`_pygrpc`) of the pyproto OTLP/gRPC exporter, run against `testutil/otelsink` — which serves OTLP through grpc-go, the same server stack as the Collector's OTLP receiver.
The suite runs against the vendored package by default; `PYGRPC_SRC_DIR` overrides the source tree (e.g. a fork checkout).

```sh
make pyproto-grpc-integration-tests
```

### Python sitecustomize interpreter compatibility tests (containers required)

The injector cannot know which Python version a process runs, so `sitecustomize.py` must parse and self-deactivate gracefully on unsupported interpreters (down to Python 2.7) and pass its version gate on supported ones.
These tests execute an unmodified application under real interpreters (`python:2.7` through `python:3.13` images) with `sitecustomize.py` on `PYTHONPATH` and assert the application always runs to completion.

```sh
make integration-test-sitecustomize
```

### Metadata tests (fast, no containers)

Validate that built packages declare correct `Provides`, `Depends`, `Suggests`, `Recommends`, and contain the expected files.
These tests parse `.deb` and `.rpm` files natively in Go using [pault.ag/go/debian](https://pkg.go.dev/pault.ag/go/debian/deb) and [cavaliergopher/rpm](https://pkg.go.dev/github.com/cavaliergopher/rpm) — no `dpkg-deb` or `rpm` CLI tools needed.

```sh
make integration-test-metadata
```

### Integration tests (containers required)

End-to-end tests that install packages in Debian/Fedora containers from a local APT/YUM repository, start instrumented applications, and verify telemetry output.

Run all integration tests (builds packages and local repositories first):

```sh
make integration-tests
```

Run a specific format and language combination:

```sh
make integration-test-deb-java
```

```sh
make integration-test-rpm-nodejs
```

```sh
make integration-test-deb-python
```

The DEB targets also run the per-language declarative-configuration scenarios (`Test<Lang>DeclarativeConfiguration`), which point `OTEL_CONFIG_FILE` at the shipped `otel-config.yaml` and assert telemetry end to end.

### Lifecycle and vendor tests (containers required)

The lifecycle tests validate the injector's `/etc/ld.so.preload` scripts, config file handling across remove, purge, and upgrade, and install/remove dependency scenarios.
The vendor tests validate the vendor replacement mechanism using a mock `acme-java-autoinstrumentation` package built by `packaging/tests/vendor/mkvendor`.

```sh
make integration-test-deb-lifecycle
```

```sh
make integration-test-rpm-vendor
```

The upgrade scenarios install from a second local repository (`build/local-repo/{apt,rpm}-next`) serving an injector built at `NEXT_VERSION` (defaults to `VERSION.1`) with a modified `default_env.conf`.
The vendor scenarios install from a third local repository (`build/local-repo/{apt,rpm}-vendor`) so the E2E suites never see two providers of the same virtual package.
The Makefile stages all of these automatically as target prerequisites.

The Python integration tests run on the architecture of the containers your engine starts.
The package is architecture-specific, so build it for that architecture — for example, on an arm64 host:

```sh
make ARCH=arm64 integration-test-rpm-python
```

The Python tests activate the agent through the injector: the package's conf.d drop-in sets `python_auto_instrumentation_agent_path_prefix`, and the injector prepends the agent's `glibc/` directory to `PYTHONPATH` for Python processes.

### Linting

```sh
make lint
```

This runs `shellcheck` on all shell scripts and `go vet` on all Go code.

## Package architecture

See [docs/design/packages-meta-architecture.md](docs/design/packages-meta-architecture.md) for the full design, including:

- The five-package structure and virtual package dependency model
- `Provides`/`Suggests`/`Recommends` relationships
- Filesystem layout (`/usr/lib/opentelemetry/`, `/etc/opentelemetry/`)
- Interface versioning for vendor package compatibility
- Lifecycle scripts for `/etc/ld.so.preload` management

## Adding a new component

To add a new language auto-instrumentation package:

1. Create config files in `packaging/common/<lang>/` (injector.conf, man page template, and README).
   The declarative configuration file is shared: every language package ships `packaging/common/otel-config.yaml` at `/etc/opentelemetry/<lang>/otel-config.yaml`.
2. Add a version pin file `packaging/common/<lang>/release.txt`.
3. Add a download function in `packaging/builder/download.go`.
4. Add a component definition in `packaging/builder/components.go` (follow the pattern of `javaInfo`).
5. Register the component in the `AllComponents` slice.
6. Add metadata tests in `packaging/tests/metadata/metadata_test.go`.
7. Add a matrix integration test in `packaging/tests/<lang>/`: one `<lang>_test.go` with a `{deb, rpm} × base image` matrix, plus `Dockerfile.deb` and `Dockerfile.rpm` parameterized by `BASE_IMAGE`. Assert on the exported telemetry via `testutil/otelsink`.
8. Add the language to the `languages` list in `packaging/tests/lifecycle/lifecycle_test.go`, so the install scenarios cover it.
9. Add the language to the `lang` matrix in `.github/workflows/build.yml`.

## Cutting a release

A release is cut by pushing a version tag to the `open-telemetry/opentelemetry-packaging` repository.

```sh
git tag v1.0.0
```

```sh
git push origin v1.0.0
```

The tag push triggers the [Build workflow](.github/workflows/build.yml), which lints, runs the unit and compatibility tests, builds the DEB and RPM packages for `amd64` and `arm64` with the version taken from the tag, and runs the full integration test matrix against the built packages.
When all tests pass, the `publish-release` job creates a GitHub release with auto-generated notes and all `.deb` and `.rpm` files attached.
The `publish-release` job runs only in the `open-telemetry/opentelemetry-packaging` repository: tags pushed to forks build and test, but do not create a release.

Publishing the release triggers the [Publish Package Repositories workflow](.github/workflows/publish-repos.yml), which:

1. Downloads the `.deb` and `.rpm` assets from the release.
2. Generates the APT repository metadata in a Debian container, and the YUM repository metadata (with `createrepo_c`, to preserve weak dependencies) in a Fedora container.
3. Renders the landing page from `packaging/repo/index.html`, substituting the release tag and the repository URL.
4. Deploys the result to the `gh-pages` branch.

The workflow can also be dispatched manually with an existing release tag, to re-publish the repositories without cutting a new release.

```sh
gh workflow run publish-repos.yml -f tag=v1.0.0
```

### One-time GitHub Pages setup

Deploying only pushes the `gh-pages` branch; serving it requires GitHub Pages to be enabled once in the repository settings ("Deploy from a branch", branch `gh-pages`, path `/`).
Until Pages is enabled, the publish workflow succeeds but nothing is served at the repository's `github.io` URL.

### Testing the publishing pipeline in a fork

The Publish Package Repositories workflow has no repository guard and computes the repository URL from the repository owner, so it works unmodified in a fork.
Create a release in the fork with `.deb` and `.rpm` assets attached (for example, re-using the artifacts of a Build workflow run), enable GitHub Pages on the `gh-pages` branch, and the repositories publish under the fork's `github.io` URL.
