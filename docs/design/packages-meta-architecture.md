# Linux System Packages Meta Architecture

## Status

Proposed

## Context

The Packaging SIG aims to deliver a streamlined, product-like experience for monitoring applications on virtual Linux hosts.
Users should be able to achieve full auto-instrumentation with a single command:

```sh
{apt|yum} install opentelemetry
```

The SIG's [design principles](https://github.com/open-telemetry/community/blob/main/projects/packaging.md) further require:

> Enable vendor packages to provide alternatives to upstream offerings

A vendor (e.g., a commercial observability provider or a Linux distribution) should be able to ship their own Java, Node.js, or .NET auto-instrumentation package that cleanly replaces the upstream equivalent.
Users who installed everything through the `opentelemetry` metapackage should be able to swap a single language package without breaking their system.

This document describes all packages in the first version of the system packages, with the changes needed to support vendor overrides.

> [!NOTE]
> The current [POC](https://github.com/open-telemetry/opentelemetry-injector/pull/239) in the OpenTelemetry Injector repository does not yet support vendor overrides or interface versions. The [Current POC Gaps](#current-poc-gaps) section details the required changes.

## Packages Overview

The first version ships five packages.
All are built with [FPM](https://fpm.readthedocs.io/) for both DEB and RPM.

| Package | Description | Architecture |
|---------|-------------|-------------|
| `opentelemetry-injector` | LD_PRELOAD-based shared library that activates language agents | Per-arch (`amd64`, `arm64`) |
| `opentelemetry-java-autoinstrumentation` | OpenTelemetry Java agent JAR | `all` / `noarch` |
| `opentelemetry-nodejs-autoinstrumentation` | OpenTelemetry Node.js auto-instrumentation | `all` / `noarch` |
| `opentelemetry-dotnet-autoinstrumentation` | OpenTelemetry .NET Automatic Instrumentation (glibc + musl) | Per-arch (`amd64`, `arm64`) |
| `opentelemetry` | Metapackage that pulls in the injector and all language packages | `all` / `noarch` |

### Dependency graph

```
opentelemetry  (metapackage)
├── opentelemetry-injector1                      (virtual)
├── opentelemetry-java-autoinstrumentation1      (virtual)
├── opentelemetry-nodejs-autoinstrumentation1    (virtual)
└── opentelemetry-dotnet-autoinstrumentation1    (virtual)

opentelemetry-injector
└── Provides: opentelemetry-injector1

opentelemetry-java-autoinstrumentation
├── Provides: opentelemetry-java-autoinstrumentation1
└── Suggests: opentelemetry-injector1

opentelemetry-nodejs-autoinstrumentation
├── Provides: opentelemetry-nodejs-autoinstrumentation1
└── Suggests: opentelemetry-injector1

opentelemetry-dotnet-autoinstrumentation
├── Provides: opentelemetry-dotnet-autoinstrumentation1
└── Suggests: opentelemetry-injector1
```

Every dependency in the graph uses a virtual package name rather than a concrete one.
The trailing `1` is not a package release version — it is the **interface generation number**, following the shared-library SONAME convention (`libssl3`, `libgcc-s1`).
Both patterns are well-established in DEB and RPM (see [Appendix: Prior Art in DEB and RPM](#appendix-prior-art-in-deb-and-rpm)).

**Interface versioning (`opentelemetry-injector1`).**
Tracks generation 1 of the injector's `conf.d/` configuration API.
The metapackage depends on this virtual name to ensure the installed injector is compatible with the language packages it pulls in.
If a future injector release breaks the conf.d contract, it bumps to `opentelemetry-injector2`; the package manager blocks incompatible combinations automatically.
See [Injector interface versioning](#injector-interface-versioning) for the full upgrade scenario.

**Swappable alternatives with interface versioning (`opentelemetry-java-autoinstrumentation1`, etc.).**
Tracks generation 1 of the contract between the injector and each language's auto-instrumentation provider — the conf.d key names, the file layout under `/usr/lib/opentelemetry/<language>/`, and the `otel-config.yaml` structure.
The metapackage depends on these virtual names.
A vendor can ship a replacement package that also provides the same virtual name — combined with `Conflicts`/`Replaces` on the concrete upstream name, the package manager handles the swap transparently.
If the upstream changes what "being a Java auto-instrumentation provider" means, it bumps to `opentelemetry-java-autoinstrumentation2`; existing vendor packages that still provide `…1` cannot satisfy the new dependency.
See [Vendor Override](#vendor-override) for the recipe and user experience.

## Filesystem Layout

All paths follow the [Filesystem Hierarchy Standard](https://refspecs.linuxfoundation.org/FHS_3.0/fhs-3.0.html) (FHS).

```
/usr/lib/opentelemetry/
├── injector/
│   └── libotelinject.so
├── java/
│   └── opentelemetry-javaagent.jar
├── nodejs/
│   └── node_modules/@opentelemetry/auto-instrumentations-node/…
└── dotnet/
    ├── glibc/…
    └── musl/…

/etc/opentelemetry/
├── injector/
│   ├── otelinject.conf
│   ├── default_env.conf
│   └── conf.d/
│       ├── java.conf
│       ├── nodejs.conf
│       └── dotnet.conf
├── java/
│   └── otel-config.yaml
├── nodejs/
│   └── otel-config.yaml
└── dotnet/
    └── otel-config.yaml

/usr/share/man/
├── man8/opentelemetry-injector.8.gz
└── man1/
    ├── opentelemetry-java.1.gz
    ├── opentelemetry-nodejs.1.gz
    └── opentelemetry-dotnet.1.gz

/usr/share/doc/
├── opentelemetry-injector/
├── opentelemetry-java-autoinstrumentation/
├── opentelemetry-nodejs-autoinstrumentation/
├── opentelemetry-dotnet-autoinstrumentation/
└── opentelemetry/
```

## Package Definitions

### `opentelemetry-injector`

The core package.
Installs `libotelinject.so`, a shared library loaded into every process via `/etc/ld.so.preload`.
At runtime, the library inspects each process to determine if it is a Java, Node.js, or .NET application and, if so, activates the corresponding auto-instrumentation agent whose path is configured in the `conf.d/` drop-in files.

#### Contents

| Path | Description |
|------|-------------|
| `/usr/lib/opentelemetry/injector/libotelinject.so` | Injector shared library (per-arch) |
| `/etc/opentelemetry/injector/otelinject.conf` | Main configuration file |
| `/etc/opentelemetry/injector/default_env.conf` | Default `OTEL_*` environment variables for all agents |
| `/etc/opentelemetry/injector/conf.d/` | Drop-in directory for language agent paths (empty until language packages are installed) |
| `/usr/share/man/man8/opentelemetry-injector.8.gz` | Man page |
| `/usr/share/doc/opentelemetry-injector/` | Documentation and copyright |

#### Package metadata

| Field | DEB | RPM |
|-------|-----|-----|
| Architecture | `amd64` or `arm64` | `x86_64` or `aarch64` |
| Provides | `opentelemetry-injector1` | `opentelemetry-injector1` |
| Config files | `/etc/opentelemetry/injector` | `/etc/opentelemetry/injector` |
| Post-install | Appends `libotelinject.so` to `/etc/ld.so.preload` | Same |
| Pre-uninstall | Removes `libotelinject.so` from `/etc/ld.so.preload` | Same |

The post-install and pre-uninstall scripts must use only POSIX shell builtins (`read`, `case`, shell redirection) to avoid dependencies on `sed` or `grep`.

#### Injector interface versioning

The injector declares `Provides: opentelemetry-injector1`.
The trailing `1` is not the package release version — it is the **generation number of the conf.d configuration API**, following the Debian shared-library naming convention (`libssl3`, `libgcc-s1`, `libpng16-16`).

The metapackage depends on `opentelemetry-injector1` (hard dependency) to ensure the installed injector is compatible with the language packages it pulls in.
Language packages only suggest `opentelemetry-injector1` (DEB `Suggests`; RPM [`Suggests`](https://docs.fedoraproject.org/en-US/packaging-guidelines/WeakDependencies/) via `--rpm-tag`) since they are useful on their own without the injector.
This decouples the API contract from the package's release cadence:

- **Today:** the injector provides `opentelemetry-injector1`. The metapackage depends on it. Everything resolves.
- **If a future injector release breaks the conf.d contract:** that release stops providing `opentelemetry-injector1` and starts providing `opentelemetry-injector2`. The metapackage still depends on `opentelemetry-injector1`, so the package manager blocks the injector upgrade until the metapackage is also updated.
- **Updated metapackage and language packages** switch to `opentelemetry-injector2`, and the system can upgrade atomically.

The same logic applies to language package interface generations. When the metapackage moves from `opentelemetry-java-autoinstrumentation1` to `opentelemetry-java-autoinstrumentation2`, the package manager upgrades the upstream language package in the same transaction.

**Impact on vendor packages.** If a user has a vendor package that provides `opentelemetry-java-autoinstrumentation1` but the new metapackage requires `opentelemetry-java-autoinstrumentation2`, the package manager holds back the metapackage upgrade until the vendor ships an updated package providing `…2`. This is the intended safety behavior — it prevents a vendor package from being silently used with an incompatible interface — but it means the user cannot upgrade to the new metapackage until their vendor catches up.

This mechanism is self-service for vendors: a vendor package provides a given interface generation and is automatically protected from incompatible upgrades without the upstream needing to know the vendor package exists.

### `opentelemetry-java-autoinstrumentation`

Downloads the upstream [OpenTelemetry Java agent](https://github.com/open-telemetry/opentelemetry-java-instrumentation) JAR at build time and packages it.

#### Contents

| Path | Description |
|------|-------------|
| `/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar` | Java agent JAR |
| `/etc/opentelemetry/injector/conf.d/java.conf` | Drop-in: `jvm_auto_instrumentation_agent_path=/usr/lib/opentelemetry/java/opentelemetry-javaagent.jar` |
| `/etc/opentelemetry/java/otel-config.yaml` | Declarative configuration template |
| `/usr/share/man/man1/opentelemetry-java.1.gz` | Man page |
| `/usr/share/doc/opentelemetry-java-autoinstrumentation/` | Documentation and copyright |

#### Package metadata

| Field | DEB | RPM |
|-------|-----|-----|
| Architecture | `all` | `noarch` |
| Provides | `opentelemetry-java-autoinstrumentation1` | `opentelemetry-java-autoinstrumentation1` |
| Suggests | `opentelemetry-injector1`, `default-jre` | `opentelemetry-injector1` |
| Config files | `/etc/opentelemetry/java` | `/etc/opentelemetry/java` |

### `opentelemetry-nodejs-autoinstrumentation`

Downloads [`@opentelemetry/auto-instrumentations-node`](https://www.npmjs.com/package/@opentelemetry/auto-instrumentations-node) from npm at build time and packages the installed `node_modules` tree.

#### Contents

| Path | Description |
|------|-------------|
| `/usr/lib/opentelemetry/nodejs/node_modules/…` | Node.js auto-instrumentation modules |
| `/etc/opentelemetry/injector/conf.d/nodejs.conf` | Drop-in: `nodejs_auto_instrumentation_agent_path=…/register.js` |
| `/etc/opentelemetry/nodejs/otel-config.yaml` | Declarative configuration template |
| `/usr/share/man/man1/opentelemetry-nodejs.1.gz` | Man page |
| `/usr/share/doc/opentelemetry-nodejs-autoinstrumentation/` | Documentation and copyright |

#### Package metadata

| Field | DEB | RPM |
|-------|-----|-----|
| Architecture | `all` | `noarch` |
| Provides | `opentelemetry-nodejs-autoinstrumentation1` | `opentelemetry-nodejs-autoinstrumentation1` |
| Suggests | `opentelemetry-injector1`, `nodejs (>= 18)` | `opentelemetry-injector1` |
| Config files | `/etc/opentelemetry/nodejs` | `/etc/opentelemetry/nodejs` |

### `opentelemetry-dotnet-autoinstrumentation`

Downloads the [OpenTelemetry .NET Automatic Instrumentation](https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation) binaries at build time, for both glibc and musl libc flavors.
The injector detects the libc flavor at runtime by reading ELF headers.

#### Contents

| Path | Description |
|------|-------------|
| `/usr/lib/opentelemetry/dotnet/glibc/…` | .NET agent binaries (glibc) |
| `/usr/lib/opentelemetry/dotnet/musl/…` | .NET agent binaries (musl) |
| `/etc/opentelemetry/injector/conf.d/dotnet.conf` | Drop-in: `dotnet_auto_instrumentation_agent_path_prefix=/usr/lib/opentelemetry/dotnet` |
| `/etc/opentelemetry/dotnet/otel-config.yaml` | Declarative configuration template |
| `/usr/share/man/man1/opentelemetry-dotnet.1.gz` | Man page |
| `/usr/share/doc/opentelemetry-dotnet-autoinstrumentation/` | Documentation and copyright |

#### Package metadata

| Field | DEB | RPM |
|-------|-----|-----|
| Architecture | `amd64` or `arm64` | `x86_64` or `aarch64` |
| Provides | `opentelemetry-dotnet-autoinstrumentation1` | `opentelemetry-dotnet-autoinstrumentation1` |
| Suggests | `opentelemetry-injector1` | `opentelemetry-injector1` |
| Config files | `/etc/opentelemetry/dotnet` | `/etc/opentelemetry/dotnet` |

### `opentelemetry`

A metapackage with no files of its own (besides a README under `/usr/share/doc/`).
It exists so that `apt install opentelemetry` or `yum install opentelemetry` pulls in the full auto-instrumentation suite.

#### Package metadata

| Field | DEB | RPM |
|-------|-----|-----|
| Architecture | `all` | `noarch` |
| Depends | `opentelemetry-injector1` | `opentelemetry-injector1` |
| Depends | `opentelemetry-java-autoinstrumentation1` | `opentelemetry-java-autoinstrumentation1` |
| Depends | `opentelemetry-nodejs-autoinstrumentation1` | `opentelemetry-nodejs-autoinstrumentation1` |
| Depends | `opentelemetry-dotnet-autoinstrumentation1` | `opentelemetry-dotnet-autoinstrumentation1` |

Every dependency uses a virtual name rather than a concrete package name.
`opentelemetry-injector1` ensures compatibility with the injector's conf.d API generation.
Language package dependencies (`opentelemetry-<language>-autoinstrumentation1`) serve a dual purpose: they version the provider interface *and* allow either the upstream or a vendor package to satisfy them.
See [Injector interface versioning](#injector-interface-versioning) for the upgrade scenario.

## Configuration System

### Hierarchy

1. **`/etc/opentelemetry/injector/otelinject.conf`** — main injector configuration. Points to the default environment file and documents the `conf.d/` mechanism.

2. **`/etc/opentelemetry/injector/default_env.conf`** — default `OTEL_*` environment variables applied to all instrumented processes. Format: `KEY=VALUE`, one per line.

3. **`/etc/opentelemetry/injector/conf.d/*.conf`** — drop-in files read in alphabetical order. Each language package installs one file that sets the agent path. Later files override earlier ones for the same key.

4. **`/etc/opentelemetry/<language>/otel-config.yaml`** — per-language declarative configuration templates (used when `OTEL_EXPERIMENTAL_CONFIG_FILE` is set).

### Available conf.d settings

| Key | Set by | Description |
|-----|--------|-------------|
| `jvm_auto_instrumentation_agent_path` | `conf.d/java.conf` | Absolute path to the Java agent JAR |
| `nodejs_auto_instrumentation_agent_path` | `conf.d/nodejs.conf` | Absolute path to the Node.js agent entry point |
| `dotnet_auto_instrumentation_agent_path_prefix` | `conf.d/dotnet.conf` | Directory prefix for the .NET agent (injector appends `glibc/` or `musl/`) |
| `all_auto_instrumentation_agents_env_path` | `otelinject.conf` | Path to the default environment variables file |

## File Ownership Boundaries

Each package owns a disjoint set of paths.
This is critical for `Conflicts`/`Replaces` to work correctly.

| Package | Owns |
|---------|------|
| `opentelemetry-injector` | `/usr/lib/opentelemetry/injector/`, `/etc/opentelemetry/injector/otelinject.conf`, `/etc/opentelemetry/injector/default_env.conf`, `/etc/opentelemetry/injector/conf.d/` (directory only) |
| `opentelemetry-java-autoinstrumentation` | `/usr/lib/opentelemetry/java/`, `/etc/opentelemetry/injector/conf.d/java.conf`, `/etc/opentelemetry/java/` |
| `opentelemetry-nodejs-autoinstrumentation` | `/usr/lib/opentelemetry/nodejs/`, `/etc/opentelemetry/injector/conf.d/nodejs.conf`, `/etc/opentelemetry/nodejs/` |
| `opentelemetry-dotnet-autoinstrumentation` | `/usr/lib/opentelemetry/dotnet/`, `/etc/opentelemetry/injector/conf.d/dotnet.conf`, `/etc/opentelemetry/dotnet/` |
| `opentelemetry` | `/usr/share/doc/opentelemetry/` |

A vendor replacement package *must* own the same set of paths as the upstream package it replaces.
This is what makes `--replaces` work: `dpkg` allows the new package to overwrite files owned by the replaced package.

## Vendor Override

### Virtual package names

Each upstream language package declares a virtual package via `--provides` that any alternative provider can also satisfy:

| Concrete package | Virtual package |
|---|---|
| `opentelemetry-java-autoinstrumentation` | `opentelemetry-java-autoinstrumentation1` |
| `opentelemetry-nodejs-autoinstrumentation` | `opentelemetry-nodejs-autoinstrumentation1` |
| `opentelemetry-dotnet-autoinstrumentation` | `opentelemetry-dotnet-autoinstrumentation1` |

### Vendor package recipe

A vendor building a replacement for, say, the Java auto-instrumentation package would use:

```bash
# DEB
fpm -s dir -t deb -n "acme-java-autoinstrumentation" -v "$ACME_VERSION" \
    --provides "opentelemetry-java-autoinstrumentation1" \
    --conflicts "opentelemetry-java-autoinstrumentation" \
    --replaces "opentelemetry-java-autoinstrumentation" \
    --deb-suggests "opentelemetry-injector1" \
    --config-files /etc/opentelemetry/injector/conf.d/ \
    --config-files /etc/opentelemetry/java/ \
    …
```

```bash
# RPM
fpm -s dir -t rpm -n "acme-java-autoinstrumentation" -v "$ACME_VERSION" \
    --provides "opentelemetry-java-autoinstrumentation1" \
    --conflicts "opentelemetry-java-autoinstrumentation" \
    --replaces "opentelemetry-java-autoinstrumentation" \
    --rpm-tag "Suggests: opentelemetry-injector1" \
    --config-files /etc/opentelemetry/injector/conf.d/ \
    --config-files /etc/opentelemetry/java/ \
    …
```

Key properties:

- `--provides` the virtual name so the metapackage's dependency is satisfied.
- `--conflicts` and `--replaces` the concrete upstream name so the package manager removes the upstream package automatically during install.
- The vendor package installs its own `conf.d/java.conf` (same filename, not a higher-priority override) pointing to its agent path. Because it `--replaces` the upstream package, the upstream's `conf.d/java.conf` is cleanly removed by the package manager.
- The injector is a `Suggests`, not a hard dependency. Language packages are useful on their own (e.g., a Java agent JAR can be used directly via `-javaagent:`), and the metapackage already ensures co-installation for the standard use case.

### Conf.d file naming convention

Vendor packages should use the **same conf.d filename** as the upstream package they replace (e.g., `java.conf`, not `99-vendor-java.conf`).
This ensures:

1. Only one config file per language is present at any time.
2. No ordering ambiguity.
3. The package manager handles the file transition via `Replaces`.

Custom user overrides (not managed by any package) should use a numeric prefix to sort after package-managed files: e.g., `99-local-java.conf`.

### User experience

#### Fresh install with upstream packages only

```sh
apt install opentelemetry
```

Installs the metapackage and all upstream language packages.
Every `opentelemetry-<language>-autoinstrumentation1` dependency is satisfied by the corresponding concrete upstream package.

#### Fresh install with a vendor package

```sh
apt install opentelemetry acme-java-autoinstrumentation
```

`apt` resolves `opentelemetry-java-autoinstrumentation1` via `acme-java-autoinstrumentation`'s `Provides`.
Because `acme-java-autoinstrumentation` declares `Conflicts: opentelemetry-java-autoinstrumentation`, `apt` skips the upstream Java package entirely.
The remaining language packages (Node.js, .NET) are installed from upstream as usual.

#### Swapping on an existing system

```sh
apt install acme-java-autoinstrumentation
```

`apt` sees `Conflicts: opentelemetry-java-autoinstrumentation` and `Replaces: opentelemetry-java-autoinstrumentation`, removes the upstream package, installs the vendor package.
The metapackage remains installed because its dependency on `opentelemetry-java-autoinstrumentation1` is still satisfied.

#### Reverting to upstream

```sh
apt install opentelemetry-java-autoinstrumentation
```

`dpkg` removes the vendor package (symmetric `Conflicts`/`Replaces` if the vendor chose to declare them, or the user runs `apt remove acme-java-autoinstrumentation` first) and installs upstream.

## Current POC Gaps

The [POC](https://github.com/open-telemetry/opentelemetry-injector/pull/239) in the OpenTelemetry Injector repository implements most of the architecture above but has the following gaps relative to the proposed design:

### 1. No interface version on the injector

The injector package does not declare `Provides: opentelemetry-injector1`.
There is no mechanism to prevent an incompatible injector upgrade from breaking installed language packages.
All consumers depend on the concrete package name (`opentelemetry-injector`) instead of a versioned interface name.

### 2. No interface version on language packages

Language packages do not declare `Provides` with a versioned virtual name (e.g., `opentelemetry-java-autoinstrumentation1`).
There is no abstraction that both the upstream and a vendor package can satisfy, and no way to detect a breaking change in the provider contract.

### 3. Metapackage depends on concrete package names with exact versions

```bash
# DEB
--depends "opentelemetry-java-autoinstrumentation (= ${VERSION})"
# RPM
--depends "opentelemetry-java-autoinstrumentation = ${VERSION}"
```

A vendor package cannot satisfy these dependencies.
Installing a vendor alternative forces the user to first remove the metapackage, which also removes any `apt autoremove` / `dnf autoremove` protection for the other language packages.

### 4. No `Conflicts` / `Replaces` metadata

If a vendor package installs files to the same paths under `/usr/lib/opentelemetry/<language>/` or `/etc/opentelemetry/injector/conf.d/`, `dpkg` and `rpm` will refuse the installation due to file conflicts.
Neither the upstream nor the vendor package declares ownership boundaries.

### 5. Unnecessary `sed` and `grep` dependencies

The injector's post-install and pre-uninstall scripts use `grep` and `sed` to manipulate `/etc/ld.so.preload`, causing the package to declare `--depends sed` and `--depends grep`.
While these tools are practically always present, the dependencies are unnecessary and should be eliminated by rewriting the scripts to use only POSIX shell builtins.

### 6. Conf.d files cannot coexist

If the upstream package owns `conf.d/java.conf` and the vendor package installs `conf.d/99-vendor-java.conf`, *both* config files are present.
Because the injector reads files in alphabetical order and the last value wins, the vendor file's value takes precedence at runtime.
But the upstream file still sets the path on every read before the vendor file overrides it, creating a fragile ordering dependency.
Worse, the upstream JAR is still on disk consuming space, and the upstream package is still installed, making `dpkg -l` / `rpm -qa` output misleading.

## Required Changes to the POC

### Injector build scripts

| File | Change |
|------|--------|
| `packaging/deb/injector/build.sh` | Add `--provides "opentelemetry-injector1"`, remove `--depends sed` and `--depends grep` |
| `packaging/rpm/injector/build.sh` | Add `--provides "opentelemetry-injector1"`, remove `--depends sed` and `--depends grep` |
| `packaging/common/scripts/postinstall-injector.sh` | Rewrite to use POSIX shell builtins only (no `grep`) |
| `packaging/common/scripts/preuninstall-injector.sh` | Rewrite to use POSIX shell builtins only (no `grep`, `sed`) |

### Language package build scripts (DEB)

| File | Change |
|------|--------|
| `packaging/deb/java/build.sh` | Add `--provides "opentelemetry-java-autoinstrumentation1"`, replace `--depends "opentelemetry-injector (>= ${VERSION})"` with `--deb-suggests "opentelemetry-injector1"` |
| `packaging/deb/nodejs/build.sh` | Add `--provides "opentelemetry-nodejs-autoinstrumentation1"`, replace `--depends "opentelemetry-injector (>= ${VERSION})"` with `--deb-suggests "opentelemetry-injector1"` |
| `packaging/deb/dotnet/build.sh` | Add `--provides "opentelemetry-dotnet-autoinstrumentation1"`, replace `--depends "opentelemetry-injector (>= ${VERSION})"` with `--deb-suggests "opentelemetry-injector1"` |

### Language package build scripts (RPM)

| File | Change |
|------|--------|
| `packaging/rpm/java/build.sh` | Add `--provides "opentelemetry-java-autoinstrumentation1"`, replace `--depends "opentelemetry-injector >= ${VERSION}"` with `--rpm-tag "Suggests: opentelemetry-injector1"` |
| `packaging/rpm/nodejs/build.sh` | Add `--provides "opentelemetry-nodejs-autoinstrumentation1"`, replace `--depends "opentelemetry-injector >= ${VERSION}"` with `--rpm-tag "Suggests: opentelemetry-injector1"` |
| `packaging/rpm/dotnet/build.sh` | Add `--provides "opentelemetry-dotnet-autoinstrumentation1"`, replace `--depends "opentelemetry-injector >= ${VERSION}"` with `--rpm-tag "Suggests: opentelemetry-injector1"` |

### Metapackage build scripts

| File | Change |
|------|--------|
| `packaging/deb/meta/build.sh` | Change `--depends "opentelemetry-injector (= ${VERSION})"` to `--depends "opentelemetry-injector1"`, change `--depends "opentelemetry-java-autoinstrumentation (= ${VERSION})"` to `--depends "opentelemetry-java-autoinstrumentation1"` (same for nodejs, dotnet) |
| `packaging/rpm/meta/build.sh` | Change `--depends "opentelemetry-injector = ${VERSION}"` to `--depends "opentelemetry-injector1"`, change `--depends "opentelemetry-java-autoinstrumentation = ${VERSION}"` to `--depends "opentelemetry-java-autoinstrumentation1"` (same for nodejs, dotnet) |

### No changes needed

- **Runtime configuration system**: the `conf.d/` mechanism already supports the override model. No code changes needed in the injector binary.
- **Installation scripts** (`postinstall-injector.sh`, `preuninstall-injector.sh`): unaffected.

## Alternatives Considered

### Conf.d-only override (no package metadata changes)

Vendor packages install a higher-priority drop-in file (e.g., `99-vendor-java.conf`) without declaring `Conflicts`/`Replaces`.

Rejected because:
- Both the upstream and vendor packages must be installed simultaneously.
- The upstream agent files remain on disk, wasting space.
- `dpkg -l` / `rpm -qa` shows both packages, confusing operators.
- Removing the upstream package also removes the metapackage.

### Dpkg `divert` mechanism

Vendor packages use `dpkg-divert` to redirect upstream files.

Rejected because:
- RPM has no equivalent; the solution must work for both DEB and RPM.
- Diverts are fragile and difficult to debug.

### Vendor-swappable injector

Making the injector itself swappable by vendors, the same way language packages are (i.e., a vendor could ship an alternative injector that provides `opentelemetry-injector1`).

Deferred because:
- The injector is a single binary with a well-defined configuration interface.
- There is no current demand for vendor-alternative injectors.
- The `opentelemetry-injector1` virtual provides is already in place for interface versioning, so adding vendor swappability later only requires vendors to declare `Conflicts`/`Replaces` on the concrete name — no further changes to the upstream packages.

## Appendix: Prior Art in DEB and RPM

### Swappable alternatives (vendor override)

The virtual-package-with-alternatives pattern used for language packages is widely established in both ecosystems.

#### DEB (Debian/Ubuntu)

**`mail-transport-agent`** — the canonical example.
Packages like `mailx` depend on `default-mta | mail-transport-agent`.
Multiple concrete packages provide it:

- `postfix` → `Provides: mail-transport-agent`
- `exim4-daemon-light` → `Provides: mail-transport-agent`
- `sendmail-bin` → `Provides: mail-transport-agent`

Each declares `Conflicts: mail-transport-agent` so only one is installed at a time.

**`java-runtime` / `java-runtime-headless`** — the JRE virtual package.
Packages like `default-jre` depend on it, and multiple implementations satisfy it:

- `openjdk-17-jre-headless` → `Provides: java-runtime-headless`
- `openjdk-21-jre-headless` → `Provides: java-runtime-headless`

**`awk`** — provided by `gawk`, `mawk`, and `original-awk`.
The `base-files` package depends on `awk`, and any of the three satisfies it.

**`x-terminal-emulator`** — provided by `xterm`, `gnome-terminal`, `kitty`, etc.
Higher-level packages depend on the virtual name.

#### RPM (RHEL/Fedora)

**`MTA`** — direct equivalent of `mail-transport-agent`:

- `postfix` → `Provides: MTA`
- `sendmail` → `Provides: MTA`
- `exim` → `Provides: MTA`

They use `Conflicts: MTA` to ensure mutual exclusion.

**`java-headless` / `java-17-headless`** — Fedora's JRE alternatives:

- `java-17-openjdk-headless` → `Provides: java-17-headless`
- `java-21-openjdk-headless` → `Provides: java-21-headless`

**`webclient`** — provided by both `wget` and `curl` in Fedora.
Packages that just need "something that can fetch a URL" depend on `webclient`.

#### Mapping to this design

| Role | DEB example | RPM example | Our equivalent |
|------|-------------|-------------|----------------|
| Virtual name | `mail-transport-agent` | `MTA` | `opentelemetry-java-autoinstrumentation1` |
| Default provider | `postfix` | `postfix` | `opentelemetry-java-autoinstrumentation` |
| Alternative provider | `exim4-daemon-light` | `exim` | `acme-java-autoinstrumentation` |
| Consumer | `mailx` | `mailx` | `opentelemetry` metapackage |

### Versioned interface names (API generations)

The pattern used by all virtual names in this design — embedding an interface generation number as a suffix (`opentelemetry-injector1`, `opentelemetry-java-autoinstrumentation1`, etc.) — follows the shared-library SONAME convention used throughout Debian and RPM.

#### DEB (Debian/Ubuntu)

**`libssl3`** — the OpenSSL shared library package.
The concrete package is `libssl3`; applications depend on it by SONAME.
When OpenSSL shipped an incompatible ABI (`libssl1.1` → `libssl3`), the package name changed, preventing applications linked against the old ABI from silently loading the new one.

**`libgcc-s1`** — the GCC support library.
The trailing `1` is the SONAME version.
Packages that link against `libgcc_s.so.1` depend on `libgcc-s1`; a hypothetical ABI break would produce `libgcc-s2`.

**`libpng16-16`** — the PNG library.
The `16` tracks the SONAME; consumers depend on the versioned name to ensure ABI compatibility.

#### RPM (RHEL/Fedora)

RPM uses the same SONAME convention but expresses it through auto-generated `Provides`:

- `openssl-libs` → `Provides: libssl.so.3()(64bit)`
- `glibc` → `Provides: libc.so.6()(64bit)`

Consumers automatically depend on the SONAME symbol.
An incompatible library version produces a different SONAME, breaking the dependency and preventing mismatched installations — the same mechanism `opentelemetry-injector1` relies on.

#### Mapping to this design

| Role | DEB example | RPM example | Our equivalent |
|------|-------------|-------------|----------------|
| Versioned interface | `libssl3` | `libssl.so.3()(64bit)` | `opentelemetry-injector1`, `opentelemetry-java-autoinstrumentation1`, etc. |
| Provider | `libssl3` package | `openssl-libs` package | `opentelemetry-injector`, `opentelemetry-java-autoinstrumentation`, etc. |
| Consumer | any package linked against `libssl.so.3` | any package linked against `libssl.so.3` | metapackage, language packages, vendor packages |

## Appendix: Implementation Notes

### RPM `Suggests` via FPM

FPM does not have a native `--rpm-suggests` flag ([jordansissel/fpm#1457](https://github.com/jordansissel/fpm/issues/1457)).
The `--rpm-tag` flag injects arbitrary directives into the RPM spec header, so `Suggests` is expressed as:

```bash
--rpm-tag "Suggests: opentelemetry-injector1"
```

The RPM repository must be indexed with `createrepo_c` (not the legacy `createrepo`) for weak dependencies to be preserved in the repository metadata.
