# Contributing to OpenTelemetry Packaging

## Prerequisites

- **Go 1.22+** (package builds and tests are pure Go)
- **npm** (needed to fetch the Node.js auto-instrumentation agent from the npm registry)
- **Python 3 with pip** (needed to fetch the Python auto-instrumentation packages; invoked as `python3 -m pip`)
- **A container engine** (Podman or Docker — needed for local repository generation and integration tests)

No Ruby, FPM, or special Docker images are required to build packages.

## Repository layout

```
cmd/build-packages/          CLI entry point for building .deb and .rpm packages
packaging/
  builder/                   Go library that drives nfpm to create packages
    builder.go               Build orchestration, common metadata
    components.go            Per-component definitions (injector, java, nodejs, dotnet, python, meta)
    download.go              Upstream artifact download helpers
  common/                    Shared assets referenced by the builder
    scripts/                 POSIX lifecycle scripts (postinstall, preuninstall)
    injector/                Config files, man page template, README, release.txt (version pin)
    java/                    "
    nodejs/                  "
    dotnet/                  "
    python/                  Config, man page template, README, requirements.txt (version pins), sitecustomize.py
  repo/                      APT and YUM repository generation scripts
  tests/                     Integration tests
    metadata/                       Host-side metadata validation (no containers needed)
    deb/{java,nodejs,dotnet,python} Testcontainers-based DEB E2E tests
    rpm/{java,nodejs,dotnet,python} Testcontainers-based RPM E2E tests
    shared/                         Shared test application sources
testutil/                    Shared Go test helpers
docs/design/                 Architecture and design documents
```

## How package builds work

Packages are created using the [nfpm](https://github.com/goreleaser/nfpm) Go library.
The `cmd/build-packages` program:

1. **Downloads upstream artifacts** from their respective release channels:
   - `libotelinject.so` from [opentelemetry-injector](https://github.com/open-telemetry/opentelemetry-injector) GitHub Releases
   - Java agent JAR from [opentelemetry-java-instrumentation](https://github.com/open-telemetry/opentelemetry-java-instrumentation) GitHub Releases
   - Node.js agent from npm (`@opentelemetry/auto-instrumentations-node`)
   - .NET agent from [opentelemetry-dotnet-instrumentation](https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation) GitHub Releases (glibc + musl)
   - Python packages via `pip`, as defined by `packaging/common/python/requirements.txt`

   The Python package bundles compiled C extensions, so its wheels are fetched
   for a fixed target architecture and Python version (`targetPythonVersion` in
   `download.go`) rather than for the build host. PyPI requirements are installed
   binary-only (manylinux wheels for the target arch); any unpublished pure-Python
   requirements pinned to a git branch are built from source in a second pass and
   merged in. This keeps the produced package correct regardless of the build
   host's OS, architecture, or Python version.

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

```sh
# Build all packages (DEB + RPM) for amd64
make packages

# Build a specific format
make deb-packages
make rpm-packages

# Build a single component
make deb-package-injector
make rpm-package-java

# Specify version and architecture
make packages VERSION=1.0.0 ARCH=arm64
```

Under the hood, `make packages` runs:

```sh
go run ./cmd/build-packages -version <VERSION> -arch <ARCH> -format all -output build/packages
```

## Testing

### Metadata tests (fast, no containers)

Validate that built packages declare correct `Provides`, `Depends`, `Suggests`, `Recommends`, and contain the expected files.
These tests parse `.deb` and `.rpm` files natively in Go using [pault.ag/go/debian](https://pkg.go.dev/pault.ag/go/debian/deb) and [cavaliergopher/rpm](https://pkg.go.dev/github.com/cavaliergopher/rpm) — no `dpkg-deb` or `rpm` CLI tools needed.

```sh
make integration-test-metadata
```

### Integration tests (containers required)

End-to-end tests that install packages in Debian/Fedora containers from a local APT/YUM repository, start instrumented applications, and verify telemetry output.

```sh
# Run all integration tests (builds packages + local repos first)
make integration-tests

# Run a specific test
make integration-test-deb-java
make integration-test-rpm-nodejs
make integration-test-deb-python
```

The Python integration tests run on the architecture of the containers your
engine starts. The package is architecture-specific, so build it for that
architecture — for example, on an arm64 host:

```sh
make ARCH=arm64 integration-test-rpm-python
```

The Python tests activate the agent by prepending its directory to `PYTHONPATH`
(the documented manual-activation path), because the injector does not yet
implement the Python `python_auto_instrumentation_agent_path_prefix` conf.d key.

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

1. Create config files in `packaging/common/<lang>/` (injector.conf, otel-config.yaml, man page template, and README).
2. Add a version pin file `packaging/common/<lang>/release.txt`.
3. Add a download function in `packaging/builder/download.go`.
4. Add a component definition in `packaging/builder/components.go` (follow the pattern of `javaInfo`).
5. Register the component in the `AllComponents` slice.
6. Add metadata tests in `packaging/tests/metadata/metadata_test.go`.
7. Add integration tests in `packaging/tests/{deb,rpm}/<lang>/`.
