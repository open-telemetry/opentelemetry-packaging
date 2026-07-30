# Cutting a release

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

## Building in Fedora COPR

The COPR build is independent of the release above.
It supplements the YUM repository rather than replacing it, and it is currently a proof of concept in the personal project `x1unix/opentelemetry-packaging`.

COPR builds with mock from a source RPM, so its RPMs come from `rpmbuild` and the generated spec rather than from nfpm.
It clones the repository itself and runs `.copr/Makefile`, which installs the source-RPM tooling and calls `make srpm`.

Dispatch the [Build in COPR workflow](.github/workflows/copr-build.yml) to submit a build.
It waits for the result and fails if any single chroot did not succeed, which the aggregate build state alone would hide.

```sh
gh workflow run copr-build.yml -f committish=main
```

The workflow has no repository guard, so a contributor can point a run at their own COPR project with `-f project=<owner>/<project>`.
It needs a `COPR_CONFIG` secret holding the whole configuration file from the [COPR API tokens page](https://copr.fedorainfracloud.org/api/) of the account that owns the project.

A build can also be submitted straight from a workstation with `copr-cli`, which is useful when iterating on the spec.

```sh
copr-cli buildscm x1unix/opentelemetry-packaging --clone-url https://github.com/x1unix/opentelemetry-packaging --commit main --method make_srpm
```

Note that COPR derives the package version from the newest version tag in the clone, exactly as the `Makefile` does, so a branch without tags produces the development placeholder.

## One-time GitHub Pages setup

Deploying only pushes the `gh-pages` branch; serving it requires GitHub Pages to be enabled once in the repository settings ("Deploy from a branch", branch `gh-pages`, path `/`).
Until Pages is enabled, the publish workflow succeeds but nothing is served at the repository's `github.io` URL.

## Testing the publishing pipeline in a fork

The Publish Package Repositories workflow has no repository guard and computes the repository URL from the repository owner, so it works unmodified in a fork.
Create a release in the fork with `.deb` and `.rpm` assets attached (for example, re-using the artifacts of a Build workflow run), enable GitHub Pages on the `gh-pages` branch, and the repositories publish under the fork's `github.io` URL.
