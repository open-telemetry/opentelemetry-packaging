# CLAUDE.md

## Project overview

This repository builds Linux system packages (DEB and RPM) for the OpenTelemetry auto-instrumentation suite.
Packages are created using [nfpm](https://github.com/goreleaser/nfpm) as a Go library — no FPM, Ruby, or Docker required for package creation.

## Key commands

```sh
make packages                  # Build all DEB + RPM packages
make deb-packages              # Build DEB packages only
make rpm-packages              # Build RPM packages only
make deb-package-<component>   # Build a single DEB (injector, java, nodejs, dotnet, meta)
make rpm-package-<component>   # Build a single RPM

make go-unit-tests             # Go command unit tests (otel-config-check)
make python-unit-tests         # sitecustomize.py unit tests (throwaway venv, no containers)
make pyproto-unit-tests        # Vendored pyproto exporter test suites (throwaway venvs, no containers)
make integration-test-metadata # Fast metadata tests (no containers)
make integration-tests         # Full E2E tests (requires Podman/Docker)
make integration-test-deb-java # Single E2E test
make integration-test-deb-lifecycle # Package lifecycle tests (preload, config, install/remove)
make integration-test-rpm-vendor    # Vendor replacement tests (mock acme package)
make integration-test-sitecustomize # sitecustomize.py across Python interpreter versions (2.7–3.x)

make lint                      # shellcheck + go vet
make clean                     # Remove build/
```

## Architecture at a glance

- `cmd/build-packages/` — CLI entry point; calls `packaging/builder/`
- `cmd/otel-config-check/` — declarative-config validator, cross-compiled into the Python package and invoked by sitecustomize.py
- `packaging/builder/` — Go package that constructs nfpm.Info per component and writes .deb/.rpm
- `packaging/common/` — Config files, POSIX lifecycle scripts, man page templates (referenced by builder)
- `packaging/repo/` — APT/YUM repo generation scripts (run in containers)
- `packaging/tests/metadata/` — Host-side tests using native Go parsers (no CLI tools)
- `packaging/tests/{java,nodejs,dotnet,python}/` — Testcontainers-based E2E telemetry tests; `python/` also hosts the sitecustomize.py interpreter compatibility tests (unit tests live next to the script)
- `packaging/tests/lifecycle/` — Package lifecycle tests (preload scripts, config handling, install/remove)
- `packaging/tests/vendor/` — Vendor replacement tests; `mkvendor/` builds the mock acme package
- `packaging/common/<component>/release.txt` — Renovate-managed upstream version pins (Python pins live in `packaging/common/python/requirements.txt`)

## Coding conventions

- Package creation is pure Go via nfpm. Do not introduce shell-based build scripts or Docker build containers for package creation.
- Lifecycle scripts (`packaging/common/scripts/`) must use only POSIX shell builtins — no `grep`, `sed`, or other external commands.
- Metadata tests use `pault.ag/go/debian` and `cavaliergopher/rpm` to parse packages natively. Do not shell out to `dpkg-deb` or `rpm` CLI tools in tests.
- Man pages use section 8 (system administration).
- All config files are `config|noreplace` type in nfpm (preserved on upgrade).
- nfpm `Info` structs must set `Platform: "linux"` explicitly — omitting it causes malformed `Architecture` fields in DEB packages.

## Prose rules

Follow these rules when writing or editing prose in this project.

### Line and paragraph structure
- **One sentence per line** (semantic line breaks).
  Each sentence starts on its own line; do not wrap mid-sentence.
- Separate paragraphs with a single blank line.
- Keep paragraphs between 2 and 5 lines (sentences).

### Section headers

Section headers should be written in sentence case, e.g., "This is an example".

### Links

- Use inline Markdown links: `[visible text](url)`.
- Link the most specific relevant term, not generic phrases like "click here" or "this page."

### Code blocks
- Fence with triple backticks and a language identifier (e.g., ` ```yaml `).
- Use code blocks to provide illustrative examples.
- **One independent command per code block.**
  Do not stack unrelated commands inside the same ` ```bash ` block.
  A reader's "copy" action should never grab more than one thing they intended to run.
  Exceptions: a multi-line invocation continued with `\`, a `key=value` env-var prefix followed by the command (`DASH0_AGENT_MODE=0 dash0 foo`), or a pipeline (`dash0 foo | jq …`) — those are a *single* command.
  Workflows that genuinely involve several steps should use one code block per step, with prose between them describing what the previous step accomplished and what the next one does.

### Punctuation and typography
- End sentences with full stops.
- Use the **Oxford comma** (e.g., "error status, latency thresholds, rate limits, and so on").
- Use curly/typographic quotes in prose (`"..."`, `'...'`); straight quotes are fine inside code blocks.
- Write numbers as digits and spell out "percent" (e.g., "10 percent", not "10%" or "ten percent").

## CONTRIBUTING.md maintenance

CONTRIBUTING.md is the primary onboarding document for new contributors.
Keep it accurate as the codebase evolves:

- **When adding or removing files/directories** referenced in the "Repository Layout" section, update that tree.
- **When adding a new component** (a new language auto-instrumentation package), update the "Adding a New Component" checklist.
- **When changing build commands or Makefile targets**, update the "Building Packages" and "Testing" sections.
- **When adding new prerequisites** (tools, runtimes), update the "Prerequisites" section.
- **When changing how package builds work** (e.g., new download sources, new nfpm patterns, new metadata fields), update "How Package Builds Work".
- **When modifying the dependency model** (Provides/Suggests/Recommends/Depends relationships), update both CONTRIBUTING.md and `docs/design/packages-meta-architecture.md`.
- Do not let CONTRIBUTING.md drift from reality — a stale contributor guide is worse than none.

## Design documents

- `docs/design/packages-meta-architecture.md` — Package architecture, dependency model, filesystem layout, interface versioning
- `docs/design/integration-test-plan.md` — Full test matrix
