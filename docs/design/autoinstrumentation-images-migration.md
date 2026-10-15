# Migrating the auto-instrumentation image builds

This plan moves the build and publication of the language auto-instrumentation container images from `open-telemetry/opentelemetry-operator` to `open-telemetry/opentelemetry-packaging`.

## Scope

In scope are the six image build contexts under `autoinstrumentation/` in the operator repository: `java`, `nodejs`, `python`, `dotnet`, `php`, and `apache-httpd`.
They land under `container-images/` in the packaging repository.
Nginx auto-instrumentation shares the `apache-httpd` image, and Go auto-instrumentation is not built by the operator SIG, so neither needs migration.

The migration also renames the existing `packaging/` tree in the packaging repository to `system-packages/`.
Once the repository builds both container images and system packages, a directory named `packaging/` inside a repository named `opentelemetry-packaging` says nothing about what it holds.

Published image paths do not change.
The images keep publishing to `ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-<language>`, and the four mirrored images keep publishing to `otel/autoinstrumentation-<language>` on Docker Hub.
Renaming the paths would invalidate every pinned `Instrumentation` CR in the field for no benefit.

## Why the GHCR path survives the move

The owner of the package is the `open-telemetry` organization, and `opentelemetry-operator/autoinstrumentation-java` is only the package name.
Container package names are arbitrary under the owner and are not required to match a repository, so the packaging repository can push to the identical path.

## Prerequisites

These require an organization owner and must be done before the first publish from the new repository.

1. For each of the six packages, open the package settings, go to “Manage Actions access”, add `open-telemetry/opentelemetry-packaging`, and set the role to Write.
2. Confirm that the organization has not disabled the linked-repository Actions inheritance.
   If it has, provision a GitHub App token with `write:packages` instead of relying on `GITHUB_TOKEN`.
3. Grant the packaging repository access to the `DOCKER_USERNAME` and `DOCKER_PASSWORD` organization secrets.
4. Install the `OTELBOT_OPERATOR` GitHub App on the packaging repository with `contents: write`, and expose the `OTELBOT_OPERATOR_CLIENT_ID` variable and the `OTELBOT_OPERATOR_PRIVATE_KEY` secret.

## Migration steps

### 1. Rename `packaging/` to `system-packages/`

Do this first, in its own pull request, before any operator sources arrive.
The two trees are disjoint, so the order is a matter of review clarity rather than correctness.

The rename is mechanical but wide, and it touches four kinds of reference:

- **Go import paths.** Every `github.com/open-telemetry/opentelemetry-packaging/packaging/...` import becomes `.../system-packages/...`. Use `gofmt -r` or a `go mod edit`-free `sed` pass, then `go build ./...` to confirm.
- **Dockerfile paths in tests.** The Testcontainers suites pass repository-relative paths as strings, for example `packaging/tests/java/Dockerfile.deb` in `packaging/tests/java/java_test.go`. A string rename misses none of these only if you grep for the literal `packaging/`.
- **The `packaging/` auto-detection in `cmd/build-packages/main.go`.** It stats a literal `packaging` directory and falls back to the `-packaging-dir` flag. Rename the directory it looks for, and decide whether the flag keeps its name.
- **The `publish-repos.yml` workflow.** It bind-mounts `$PWD/packaging/repo` into the repository generation container and reads `packaging/repo/index.html`.

Update `AGENTS.md`, `CLAUDE.md`, `README.md`, `CONTRIBUTING.md`, `RELEASING.md`, and both design documents in the same pull request.
The repository layout tree in `CONTRIBUTING.md` is the one most likely to be missed.

Verify with `make lint`, `make go-unit-tests`, `make packages`, and `make integration-test-metadata`, then a full `make integration-tests` run.

### 2. Move the image sources

Copy `autoinstrumentation/` from the operator repository into `container-images/` in the packaging repository, preserving the per-language layout and every file.

Carry each `revision.txt` across unchanged.
If the revisions reset to `1`, the first publish overwrites an already published `<sdk-version>-1` tag with different content, and the registry does not prevent it.

Move `hack/autoinstrumentation-revision/` and the `make check-autoinstrumentation-revision` and `make bump-autoinstrumentation-revision` targets.
The tool hardcodes the `autoinstrumentation/` prefix when it resolves the per-language version and revision files, so repoint it at `container-images/`.

Do not create a `hack/` directory.
The packaging repository puts its Go tools under `cmd/`, so the revision tool becomes `cmd/image-revision`, and the shared logic goes under `internal/`.

`hack/update-example-images/` stays in the operator repository.
It writes `config/samples`, the `e2e` and `e2e-multi-instrumentation` manifests, and `docs/auto-instrumentation`, all of which the operator owns.
Step 5 covers how it gets the versions it needs.

### 3. Consolidate the version resolution

The revision tool resolves, per language, where the upstream version is pinned and how to parse it, in `sourceFile()` and `extractVersion()`.
The packaging repository answers the same question in two other places:

- `readReleaseVersion()` in `system-packages/builder/download.go`, which takes the last non-comment line of `common/<component>/release.txt` for `injector`, `java`, `nodejs`, and `dotnet`.
- `splitRequirements()` in the same file, for `common/python/requirements.txt`.

Fold all three into one `internal/upstream` package with a single component table that covers both trees.
The revision tool, the package download step, and `make check-minimum-python-version` then share one definition of the upstream version of a component.
The per-language `switch` in the operator tool becomes table entries.

The two trees disagree on both format and content, and that has to be settled here rather than deferred.
`container-images/java/version.txt` holds a bare `2.31.1`, while `system-packages/common/java/release.txt` holds `v2.30.0` behind a `# renovate:` comment line.
Today that divergence is invisible because it spans two repositories.
After the move, one repository ships a container image and a set of system packages carrying different versions of the same Java agent, each bumped by its own Renovate rule.
Decide whether that is intentional before unifying the reader, and record the answer in `docs/design/packages-meta-architecture.md`.

Take `hack/govulncheck.sh` in the same pull request.
The packaging repository runs an OSSF scorecard workflow and no vulnerability scan, and the script is unrelated to the rest of the migration.

Leave the rest of the operator `hack/` behind: `check-operator-ready.go`, the four `install-*.sh` cluster helpers, `ignore-createdAt-bundle.sh`, and `check-chainsaw-test-names.sh` are all specific to the operator or to its chainsaw test suite.

### 4. Move the workflows

Copy the six `publish-autoinstrumentation-*.yaml` workflows, plus `check-autoinstrumentation-revision.yaml` and `bump-autoinstrumentation-revision.yaml`.

Rewrite every path trigger and every build context from `autoinstrumentation/<language>` to `container-images/<language>`.
A stale path trigger does not fail; it silently stops publishing.

Set `org.opencontainers.image.source` explicitly on each build rather than accepting the value that `docker/metadata-action` derives from the running repository.
The derived value silently relinks all six packages to the packaging repository and revokes the inherited access of the operator repository.
Relinking is the desired end state, so make it an explicit, reviewed change.

### 5. Move the Renovate rules

Copy the custom managers and package rules that track the upstream SDK versions, rewriting every `fileMatch` to the `container-images/` prefix:

- `container-images/java/version.txt`, `container-images/dotnet/version.txt`, `container-images/php/version.txt`, and `container-images/apache-httpd/version.txt`.
- `container-images/python/requirements.txt` via the `pip_requirements` manager.
- `container-images/nodejs/package.json` via the `npm` manager.
- The `dockerfile` manager, which pins the `busybox`, `python`, and `node` base images by digest.

Reconcile these with the rules the packaging repository already has for `system-packages/common/<component>/release.txt` and `system-packages/common/python/requirements.txt`.
Two `pip_requirements` scopes now coexist, and they must not bump each other.

### 6. Keep the operator repository working

The operator still needs the SDK versions and the revisions for three things: the defaults compiled into the binary through `versions.txt` and the linker flags, the pinned images in the `Instrumentation` CR examples, and the image tags in the e2e manifests.

Publish those values as a Go API rather than syncing text files.
Add a `container-images/versions.go` in the packaging repository that embeds `*/version.txt`, `*/requirements.txt`, `*/package.json`, and `*/revision.txt`, and exposes the resolved SDK version and revision per language.
The operator adds a `require` on `github.com/open-telemetry/opentelemetry-packaging`, and `update-example-images` reads the values from a compiled-in dependency.

Renovate already runs the `gomod` manager in the operator repository, so every image publish reaches the operator as an ordinary dependency bump pull request.
`make ensure-update-is-noop` stays a fully offline gate, and no scheduled sync workflow is needed.

Two constraints:

- `go:embed` cannot reach outside the directory of its own package, so the embedding package must sit at `container-images/`, not under `internal/`. The packaging repository uses no `go:embed` today.
- The embedded values and the `internal/upstream` table from step 3 must have one implementation between them, not two readers of the same files.

### 7. Cut over

1. Merge the packaging repository changes with the publish workflows still limited to `workflow_dispatch`.
2. Run one manual publish per language and verify that the new digests appear under the existing package path, and that the mirrored images reach Docker Hub.
3. Enable the `push` trigger on the packaging repository workflows.
4. Delete `autoinstrumentation/`, the two revision workflows, the six publish workflows, and the migrated Renovate rules from the operator repository, in a single pull request.
5. Update the operator `CONTRIBUTING.md`, and leave a stub `autoinstrumentation/README.md` that points at `container-images/` in the packaging repository.

## Verification

- The full test suite passes after the `system-packages/` rename, with `make integration-tests` included rather than skipped.
- Each package under `ghcr.io/open-telemetry/opentelemetry-operator/` has a new digest published from the packaging repository, and the historical tags still resolve.
- The `<sdk-version>-<revision>` tags continue the previous lineage, with no revision reset.
- A Renovate SDK bump in the packaging repository triggers the revision bump and passes the revision check without a maintainer edit.
- The operator builds against the published packaging module, and `make ensure-update-is-noop` passes with no network access.
- The operator e2e tests pass against the images published from the packaging repository.

## Risks

- **Revision reset.** The highest-impact failure, and it is silent. Verify the `revision.txt` values in the first publish before enabling the `push` trigger.
- **Package access.** Every prerequisite needs an organization owner, so schedule that before the code work rather than after.
- **Drift between the repositories.** The operator defaults in `versions.txt` already lag the published images by design, and the Go module dependency adds a second lag. Treat a stalled dependency bump as a release blocker.
- **Java version divergence inside one repository.** The container image and the system packages pin the Java agent independently and already differ. After the move the difference is visible in a single tree, and someone will change one of them without the other.
- **String paths missed by the rename.** The Testcontainers Dockerfile paths and the `publish-repos.yml` bind mounts are plain strings, so the compiler catches none of them. They fail only at integration test time or at repository publish time.
