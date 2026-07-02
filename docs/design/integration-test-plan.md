# Integration test plan

## Status

Proposed

## Context

The current integration tests only cover end-to-end telemetry: install packages, start an app, send traffic, and check that traces appear.
This leaves large gaps in coverage around package metadata, lifecycle events, configuration behavior, and vendor replacement.

This document defines the full matrix of tests needed and the implementation approach.

## Test categories

### 1. Package metadata validation

Validate that built packages declare the correct metadata fields without starting any containers or installing anything.

| Test | What it validates |
|------|-------------------|
| Injector provides `opentelemetry-injector1` | Virtual package for interface versioning |
| Java provides `opentelemetry-java-autoinstrumentation1` | Virtual package for vendor replacement |
| Node.js provides `opentelemetry-nodejs-autoinstrumentation1` | Virtual package for vendor replacement |
| .NET provides `opentelemetry-dotnet-autoinstrumentation1` | Virtual package for vendor replacement |
| Metapackage depends on `opentelemetry-injector1` | Not a concrete package name |
| Metapackage depends on `opentelemetry-java-autoinstrumentation1` | Not a concrete package name |
| Metapackage depends on `opentelemetry-nodejs-autoinstrumentation1` | Not a concrete package name |
| Metapackage depends on `opentelemetry-dotnet-autoinstrumentation1` | Not a concrete package name |
| Injector suggests `opentelemetry-injector1` on Java/Node.js/.NET packages (DEB) | Soft dependency |
| No `sed` or `grep` dependencies on injector | Scripts use only POSIX builtins |

**Implementation:** Run `dpkg-deb --info` / `rpm -qp --provides --requires --suggests` on the built package files.
No containers needed — these are host-side assertions on the `.deb`/`.rpm` artifacts.

### 2. Package contents validation

Validate that packages contain the expected files at the expected paths with correct permissions.

| Test | What it validates |
|------|-------------------|
| Injector contains `libotelinject.so` at `/usr/lib/opentelemetry/injector/` | Binary placement |
| Injector contains `injector.conf` at `/etc/opentelemetry/injector/` | Config placement |
| Injector contains `default_env.conf` at `/etc/opentelemetry/injector/` | Env config placement |
| Injector contains empty `conf.d/` directory | Drop-in directory |
| Java contains `opentelemetry-javaagent.jar` | Agent placement |
| Java contains `conf.d/java.conf` with correct path | Drop-in references correct JAR |
| Node.js contains `register.js` in node_modules tree | Agent placement |
| Node.js contains `conf.d/nodejs.conf` with correct path | Drop-in references correct entry point |
| .NET contains native profiler for glibc and musl | Both libc flavors |
| .NET contains `conf.d/dotnet.conf` with correct path prefix | Drop-in references correct prefix |
| Man pages are present and gzipped | Documentation |

**Implementation:** Run `dpkg-deb --contents` / `rpm -qpl` on the built package files.
No containers needed.

### 3. Post-install / pre-uninstall scripts

Validate that the injector's lifecycle scripts correctly manage `/etc/ld.so.preload`.

| Test | What it validates |
|------|-------------------|
| Install creates `/etc/ld.so.preload` with injector path | Post-install |
| Install is idempotent (second install doesn't duplicate the entry) | Post-install idempotency |
| Remove cleans up the injector path from `/etc/ld.so.preload` | Pre-uninstall |
| Remove deletes `/etc/ld.so.preload` if it becomes empty | Cleanup |
| Remove preserves other entries in `/etc/ld.so.preload` | Non-destructive |

**Implementation:** Install/remove the injector package in a container and inspect `/etc/ld.so.preload` at each stage.

### 4. Config file lifecycle

Validate that configuration files are handled correctly across package lifecycle events.

#### 4a. Config file preservation on remove (DEB)

| Test | What it validates |
|------|-------------------|
| `apt remove opentelemetry-injector` preserves `injector.conf` | Conffiles kept on remove |
| `apt remove opentelemetry-injector` preserves `default_env.conf` | Conffiles kept on remove |
| `apt purge opentelemetry-injector` removes config files | Conffiles deleted on purge |

#### 4b. Config file behavior on upgrade

| Test | What it validates |
|------|-------------------|
| Upgrade with unmodified config: new config replaces old | Standard upgrade |
| Upgrade with user-modified config: user changes preserved (DEB prompts, RPM creates `.rpmnew`) | Modified conffile handling |

#### 4c. Config file behavior with user overrides

| Test | What it validates |
|------|-------------------|
| User adds `99-custom.conf` in `conf.d/`: injector reads it | Custom drop-in |
| User modifies `default_env.conf` to set `OTEL_SERVICE_NAME`: value is applied | Env var override |
| User modifies `default_env.conf`: package upgrade preserves the modification | Modified conffile on upgrade |

**Implementation:** Install packages, modify config files, upgrade/remove, and assert on file state and application behavior.

### 5. Install scenarios (beyond metapackage)

Validate that individual packages and combinations install correctly via `apt`/`dnf`.

| Test | What it validates |
|------|-------------------|
| `apt install opentelemetry` | Metapackage pulls in everything |
| `apt install opentelemetry-injector` | Injector alone, no language packages |
| `apt install opentelemetry-injector opentelemetry-java-autoinstrumentation` | Injector + single language |
| `apt install opentelemetry-java-autoinstrumentation` (without injector) | Language package alone (Suggests, not Depends) |
| `apt remove opentelemetry-java-autoinstrumentation` | Removing one language doesn't remove injector |
| `apt remove opentelemetry-injector` | Removing injector doesn't force-remove language packages |

**Implementation:** Run `apt install`/`apt remove` sequences in containers and verify package state with `dpkg -l`.

### 6. Vendor replacement

Validate the vendor override mechanism end-to-end using a mock `acme-java-autoinstrumentation` package.

#### 6a. Build the mock vendor package

Build an `acme-java-autoinstrumentation` package that:
- `Provides: opentelemetry-java-autoinstrumentation1`
- `Conflicts: opentelemetry-java-autoinstrumentation`
- `Replaces: opentelemetry-java-autoinstrumentation`
- Installs a different JAR at the same path
- Installs its own `conf.d/java.conf` pointing to the different JAR

#### 6b. Vendor replacement tests

| Test | What it validates |
|------|-------------------|
| Fresh install: `apt install opentelemetry acme-java-autoinstrumentation` | Vendor package satisfies metapackage dependency |
| Swap: `apt install acme-java-autoinstrumentation` on existing system | Upstream Java replaced, metapackage stays |
| Revert: `apt install opentelemetry-java-autoinstrumentation` after vendor | Vendor removed, upstream restored |
| Upstream `conf.d/java.conf` is replaced by vendor's, not duplicated | File ownership transfer |
| Metapackage remains installed throughout swap and revert | Dependency resolution |

**Implementation:** Build a mock vendor package as part of the test setup, then run install/swap/revert sequences in containers.

### 7. E2E telemetry (existing, expanded)

Already implemented for Java, Node.js, and .NET across DEB and RPM.
No changes needed.

## Test infrastructure

### Package metadata and contents tests

These run on the host against built artifacts — no containers needed.
New test file: `packaging/tests/metadata/metadata_test.go`.

### Lifecycle and install scenario tests

These need a base container with the local repo configured.
The test execs `apt`/`dnf` commands and inspects state.
These tests share a common Dockerfile per format (DEB/RPM) that just sets up the repo — no application runtime needed.

### Vendor replacement tests

These need a build step for the mock `acme-*` package, then use the same lifecycle container pattern.

## Makefile targets

The following targets are implemented today:

```
make integration-tests                    # all tests
make integration-test-metadata            # package metadata + contents
make integration-test-deb-java            # E2E telemetry
make integration-test-deb-nodejs          # E2E telemetry
make integration-test-deb-dotnet          # E2E telemetry
make integration-test-deb-python          # E2E telemetry
make integration-test-rpm-java            # E2E telemetry
make integration-test-rpm-nodejs          # E2E telemetry
make integration-test-rpm-dotnet          # E2E telemetry
make integration-test-rpm-python          # E2E telemetry
```

The lifecycle and vendor-replacement categories above are not yet implemented.
They are planned as the following targets:

```
make integration-test-deb-lifecycle       # DEB install/remove/upgrade/config
make integration-test-rpm-lifecycle       # RPM install/remove/upgrade/config
make integration-test-deb-vendor          # DEB vendor replacement
make integration-test-rpm-vendor          # RPM vendor replacement
```

## Priority order

1. **Package metadata validation** — fastest to implement, catches regressions in `--provides`/`--depends` early.
2. **Package contents validation** — fast, no containers.
3. **Post-install / pre-uninstall scripts** — validates the most critical lifecycle event.
4. **Install scenarios** — validates dependency resolution for all install patterns.
5. **Vendor replacement** — validates the design doc's core premise.
6. **Config file lifecycle** — most complex, multiple scenarios per format.
