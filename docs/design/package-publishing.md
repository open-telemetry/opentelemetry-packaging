# Package building and publishing

## Status

Proposed

## Context

The system packages need to reach users as signed DEB and RPM repositories that `apt` and `dnf` can install from and upgrade against.
As of the beginning of November 2026, the Debian and Ubuntu packages come from nfpm (`make deb-packages`) and never pass through a build service, so nothing signs them or generates repository metadata for them.
The Fedora and EPEL RPMs build in [Fedora COPR](https://copr.fedorainfracloud.org/coprs/opentelemetry/opentelemetry-packaging/), which signs them and serves a repository.
COPR, however, only builds RPMs, and it builds every component at once from a release tag.

This document specifies how the packages are built, versioned, signed, and published.

### COPR

[COPR](https://copr.fedorainfracloud.org/coprs/) is a build system for RPM provided by the Fedora project.

### Open Build Service

[OBS](https://build.opensuse.org) is a hosted build farm run by openSUSE.
Like COPR, it builds packages, signs them, and publishes them as repositories.
Unlike COPR, it builds for many distributions, including Debian and Ubuntu, so a single service can cover every target currently in scope for system packages (Debian, Ubuntu, Fedora, and RHEL).

### Release schedule

Packages release at different paces, when the upstream agents (the injector, or the Java, Node.js, .NET, or Python auto-instrumentation) ship.

### Scope

The work is split into phases.

1. **DEBs on OBS.** Build and publish the Debian and Ubuntu packages on the openSUSE Open Build Service (OBS).
   Users install them from the repositories OBS publishes.
   COPR keeps building the RPMs, unchanged, in the meantime.
2. **RPMs on OBS.** Once the DEB path is in place, build the Fedora and EPEL packages on OBS with the same model, and retire COPR.
   This document covers that phase only in outline; its details are added when it starts.
3. **Blob storage mirror.** Mirror the published repositories to blob storage behind a project domain.
   Its provider, domain, and trigger are not decided yet.
4. **Retire GitHub Pages.**

### Goals

- Build the DEB packages, and then the RPMs, on a single hosted build service that signs them and generates repository metadata.
- Release each package independently from the others, when its upstream agent or its packaging changes.
- Keep previously released versions installable.
- Serve the repositories from object storage and a domain the project controls, so the build service can change with limited impact on the users.

### Non-goals

- Running our own build service instance.
- Nightly or development channels.
- Changing how COPR builds the RPMs before it is retired.
- Changing how the GitHub release assets are versioned.

### Vocabulary

OBS gives common words specific meanings, and this document uses them in the OBS sense.
A **project** (for example `home:opentelemetry:stable`) is a namespace that holds packages, build settings, and a signing key; projects nest with `:`.
An OBS **package** is a directory of source files inside a project, from which OBS builds one or more binary `.deb` or `.rpm` files.
An OBS **repository** is a build target inside a project: a base distribution plus a list of architectures.
When a project publishes, each of its repositories becomes an apt repository that users can add.

A **source commit** stores a new set of files in an OBS package and creates a new **source revision**, much like a Git commit.
OBS then builds that revision for every repository and architecture of the project.
“Git commit” in this document always means a commit in this GitHub repository, and “source revision” always means the OBS one.

## Component versioning

The version of the agents to install is tracked in dedicated files in the GitHub repository, and updated by Renovate.

| Package | Version source |
| --- | --- |
| injector, Java, Node.js, .NET | `packaging/common/<component>/release.txt`, bumped by Renovate |
| Python | the `opentelemetry-distro` pin in `packaging/common/python/requirements.txt`, bumped by Renovate |
| metapackage | `packaging/common/meta/release.txt`, bumped by hand |

## Package versioning

A package version has the form `<component version>-<package release>`.
The component version says which agent the package ships, and the package release says which packaging of that agent it is.

This scheme applies to the packages on OBS only.
The GitHub release assets, and COPR until it is retired, keep the repository release tag version.

The metapackage declares its relations without versions and ships only a README, so it does not change when other packages do.
Its version is bumped by hand when its relations or contents change, for example when a new package joins the suite.
New versions of the `opentelemetry` metapackage are expected to be rare.

## Build process

CI prepares the complete contents of each package from `main` and the recipe to assemble them, and uploads both to OBS.
OBS compiles nothing: it assembles the prepared contents into DEBs with [`debbuild`](https://github.com/debbuild/debbuild), a tool that builds DEBs from an RPM-style spec.
Once every build has succeeded, CI asks OBS to release the package, and OBS copies the binaries into the project users install from.

```mermaid
flowchart TD
    git["Git commit on main"] --> ci["GitHub Actions<br/>prepares the sources of each changed package"]
    ci -- "source commit" --> build["OBS: home:opentelemetry:stable:build<br/>builds every repository and architecture"]
    build -- "release (copies the binaries)" --> stable["OBS: home:opentelemetry:stable<br/>signs, generates repository metadata, publishes"]
    stable --> users["Users<br/>apt repositories on download.opensuse.org"]
    stable -. "phase 3: mirror" .-> mirror["Blob storage on a project domain"]
    mirror -.-> users
```

In phase 3, a mirror copies the published repositories to blob storage on a project domain, and that address replaces `download.opensuse.org` for users.
See [Blob storage mirror](#blob-storage-mirror).

| Concern | Owner |
| --- | --- |
| Choosing what to build, preparing the package contents, generating sources | GitHub Actions, from this repository |
| Building the binary packages | OBS |
| Signing packages and repository metadata | OBS, with a key it generates and holds |
| Generating repository metadata (`Packages`, `Release`, `InRelease`) | OBS |
| Retaining released versions | OBS (`home:opentelemetry:stable`) |
| Serving users | OBS at first, a blob storage mirror on a project domain from phase 3 |

OBS generates the signing key and keeps it on its signing server, and no private key is ever downloaded, uploaded, or handled by the project.
Subprojects inherit the key of `home:opentelemetry`, and users fetch the public key from the published repository.

## OBS setup

### Packages

Each component is a separate OBS package with its own sources: `opentelemetry-injector`, the 4 `opentelemetry-*-autoinstrumentation` packages, and the `opentelemetry` metapackage.
The packages have no build dependencies on each other, so committing to one rebuilds only that one.
They depend on each other only through unversioned virtual names (see [Linux system packages meta architecture](packages-meta-architecture.md)), so packages at different versions install together.
The `opentelemetry-jmx-scraper` package from that document is not implemented yet; it becomes another OBS package once it is.

### Package release

The package release changes whenever the packaging changes for the same component version, for example its paths, dependencies, or lifecycle scripts.
OBS manages it, so this repository neither stores nor bumps it.

OBS sets the release to `<commits>.<builds>`.
`<commits>` counts the source commits at the current component version, and restarts at 1 when the version changes.
`<builds>` counts rebuilds of the same source revision, which OBS starts on its own, for example after an update to the base distribution.

| Event | Version in the build project | Version users get |
| --- | --- | --- |
| `1.0.0`, first upload | `1.0.0-1.1` | `1.0.0-1.1` |
| Same version, identical sources | stays `1.0.0-1.1`, nothing is committed | `1.0.0-1.1` |
| OBS rebuilds the same source revision | `1.0.0-1.2` | stays `1.0.0-1.1` |
| Same version, packaging changed | `1.0.0-2.1` | `1.0.0-2.1` |
| Next version, `1.1.0` | `1.1.0-1.1` | `1.1.0-1.1` |

A rebuild that OBS starts on its own stays in the build project, because releases happen only when CI requests them.
The next push sees that package's sources as unchanged and skips it.

### Project layout

```text
home:opentelemetry                 owns the signing key; no packages, no repositories
├── :sandbox                       disposable; trying changes before they reach users
└── :stable                        what users install: published, builds disabled, keeps old versions
    └── :build                     CI commits here; a release copies the results into :stable
```

CI commits sources to `home:opentelemetry:stable:build`, which builds them but publishes nothing.
After the builds succeed, CI asks OBS to release the package, and OBS copies its binaries into `home:opentelemetry:stable`.
Each release lands there as a separate entry named `<package>.<timestamp>`, so earlier versions stay in the published repository.

In OBS terms, each repository of the build project has a release target pointing at `home:opentelemetry:stable`, triggered manually, which is what the release request uses.
`home:opentelemetry:stable` is a maintenance release project, the OBS project kind that keeps every release instead of replacing the previous one.
`home:opentelemetry:sandbox` is a separate project for trying changes, and it never releases.

### Credentials

CI authenticates to OBS as a dedicated bot account, with a password held as a repository secret.
OBS scoped tokens can only trigger actions on sources OBS already holds, so they cannot make source commits, which is what this pipeline needs.

The bot is a maintainer of the subprojects only, and not of `home:opentelemetry`, which holds the signing key.
A compromise of the CI credentials reaches the sources and the builds, but not the key.

### Repositories and targets

In phase 1, the stable build project has 2 repositories, both built for the `x86_64` and `aarch64` architectures.

| Repository | Built against | Serves |
| --- | --- | --- |
| `debian` | Debian 12 | Debian 12 and newer |
| `ubuntu` | Ubuntu 22.04 | Ubuntu 22.04 and newer |

The package contents do not depend on the distribution release they are built against, so the packages are expected to install on newer releases of the same distribution.
Debian and Ubuntu get separate repositories in case the content of the packages needs to diverge.

The sandbox instead has a repository per release (Debian 12 and 13, Ubuntu 22.04, 24.04, and 26.04), each for both architectures so that it is possible to compare the same package across releases before it reaches users.

### Sources

Nothing is compiled on OBS: its build roots have no network access.
Debian 12 also cannot supply the Go version this repository needs, so a build cannot compile the tools either.

Instead, CI prepares everything a build needs, as Kubernetes does (see [Prior art](#prior-art)).
For each package, `make deb-sources` (or `make deb-sources-<component>`) writes the 2 files that make up its OBS sources.

- `<package>-<version>.tar.gz` holds the prepared package contents for every architecture, a directory per architecture, plus the file lists.
  It is prepared with the same code as the nfpm packages, so the contents cannot drift between the two.
- `<package>.spec` is the packaging recipe, rendered from the same component metadata.
  It has no build step: its install step only copies the directory for the build architecture.

OBS builds the spec with `debbuild`.
This keeps a single spec format for DEB and RPM instead of a separate `debian/` tree.

`debbuild` is not part of Debian or Ubuntu, so the build roots take it from `Debian:debbuild` and `Ubuntu:debbuild`, the OBS projects that package it.
Every repository lists the matching debbuild project ahead of the distribution itself, which is what makes OBS choose the `debbuild` recipe for a spec with no `debian/` tree.

### Reproducible sources

The release counter is meaningful only if CI makes source commits when the sources really changed.
CI dates each package by its **source date**: the time of the last Git commit that touched that package's inputs, passed to the generator as [`SOURCE_DATE_EPOCH`](https://reproducible-builds.org/specs/source-date-epoch/).
The archive entries are sorted, owned by root, and dated with the source date, and so are the man pages.

An unchanged package regenerates byte-identical sources.
CI compares their file names and MD5 sums with the sources stored on OBS, and skips the upload when they match.

## Rebuild triggers

CI uploads, rebuilds, and releases a selected package only when the following paths change on a push to `main`, and the generated sources differ from those on OBS:

| Change | Selects |
| --- | --- |
| Anything under `packaging/common/<component>/`, such as a `release.txt` bump | that component |
| Shared packaging code, lifecycle scripts, the tools built into packages, or build configuration | every component |

The tools that plan the work and upload the sources are not inputs, because they change how sources reach OBS, not what is in them.

The typical update trigger is merging a Renovate PR that bumps the pin of a single agent.
Once it merges, only that package is rebuilt, gets the new component version with release `1.1`, and is released.
A builder change selects every package: those whose regenerated sources are unchanged are skipped, and the others get a new release counter at the same component version.

The workflow must also be dispatchable by hand: for the first upload, for a single package, for another Git ref, or to try a change in the sandbox.

## Blob storage mirror

OBS publishes the released repositories on `download.opensuse.org`.
Those URLs name the build service and the project, so pointing users at them ties every installed system to OBS.

In phase 3, the published repositories are mirrored to blob storage and served from a domain the project controls.
After a release publishes, a mirror job copies the repository contents and metadata exactly as OBS generated them: the packages, the indexes, the signed `Release` and `InRelease` files, and the public key.
Because the files are copied unchanged, the OBS signatures stay valid and the mirror never needs a signing key.
The mirror will be the only documented URL for package managers.

Note that, if the build system changes, users will need to change which keys they trust, as described in [Risks](#the-signing-key-cannot-be-ported-to-a-different-build-service).

Until the mirror exists, users install from `download.opensuse.org` directly, and those URLs should be documented as provisional.
Users who configured them change their apt source once, to the mirror address, when it launches.
Users of the COPR repository likewise switch once, when COPR is retired.
The storage provider, domain, bucket layout, and the trigger for the mirror job are not decided yet.

## Risks

### The signing key cannot be ported to a different build service

OBS generates the signing key and keeps it on its signing server, and the project cannot export it.
Everything users install is signed by a key that belongs to the build service rather than to the project.
If the project leaves OBS, or OBS rotates or retires the key, the packages and the repository metadata are signed by a different key.
That costs every user a manual step: an installed system has to trust the new signing key before it can install or upgrade the packages again.

The mirror bounds that step to the key alone.
Users are instructed to point their package managers at the project domain, so a change of build service does not also make them change the repository address they configured.

### The build root depends on outside projects

`debbuild` reaches the build roots from `Debian:debbuild` and `Ubuntu:debbuild`, at whatever version those projects publish at the time of the build.
They are maintained outside this project: a break in them, or an end to their support for a Debian or Ubuntu release, stops the pipeline until it is worked around.
The fallback is the `debian/` tree that [Alternatives considered](#a-native-debian-directory) rejects, which is a larger change than the situation would warrant.

## Prior art

The design follows how Kubernetes publishes its packages ([KEP-1731](https://github.com/kubernetes/enhancements/tree/master/keps/sig-release/1731-publishing-packages), implemented in [kubernetes/release](https://github.com/kubernetes/release)).
Kubernetes builds and signs its DEBs and RPMs on OBS, from prebuilt binaries its release pipeline produces.
CI commits a spec and a tarball per package to a build project, waits for the builds, and releases the result into a publishing project.
Users install through a vanity domain (`pkgs.k8s.io`), so the storage behind it can change.

We take the same model, with 2 differences.
We ship a single product line, so there is a single pair of build and publishing projects instead of a pair per minor version.
Each package is versioned by the agent it bundles, not by a shared release version.

## Alternatives considered

### A native `debian/` directory

Generate a `debian/` tree (`control`, `rules`, maintainer scripts) next to the RPM spec.
Rejected because it is a second metadata projection in a different format to keep in sync, while `debbuild` builds DEBs from the spec format the RPM path already uses.
The cost of that choice is the dependency on `Debian:debbuild` and `Ubuntu:debbuild` described in [Sources](#sources), which a `debian/` tree would not need.

### Building on OBS from source

Run `go build` and fetch the upstream agents during the OBS build, as COPR does.
Rejected because OBS build roots have no network access.
The required Go version is also unavailable in the standard Debian 12 repositories, and the other distributions need backports or additional repositories to provide it.
Preparing the contents in CI removes every build dependency, so Debian and Ubuntu can share a single spec.

### Releasing every package together from a release tag

Build and release all packages from a single tag, as COPR does.
Rejected because it couples the release of every agent to every other, rebuilds unchanged packages, and forces a shared version that does not tell users which agent a package contains.

### Using `osc` in CI

Run `osc` in CI, as Kubernetes does.
Rejected because `osc` is not on PyPI and needs the system `rpm` Python bindings, so a GitHub runner would need an openSUSE container just to run it.
It would also need the password written to its configuration file on the runner, and `osc results --watch` can report the previous revision as built right after a commit.
