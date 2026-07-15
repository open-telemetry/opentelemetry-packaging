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

## One-time GitHub Pages setup

Deploying only pushes the `gh-pages` branch; serving it requires GitHub Pages to be enabled once in the repository settings ("Deploy from a branch", branch `gh-pages`, path `/`).
Until Pages is enabled, the publish workflow succeeds but nothing is served at the repository's `github.io` URL.

## Testing the publishing pipeline in a fork

The Publish Package Repositories workflow has no repository guard and computes the repository URL from the repository owner, so it works unmodified in a fork.
Create a release in the fork with `.deb` and `.rpm` assets attached (for example, re-using the artifacts of a Build workflow run), enable GitHub Pages on the `gh-pages` branch, and the repositories publish under the fork's `github.io` URL.
