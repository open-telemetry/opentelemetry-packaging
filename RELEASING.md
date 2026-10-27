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

## Package images

Package images have a separate release flow and do not wait for a packaging release tag or published DEBs.
The [Package images workflow](.github/workflows/images.yml) builds, tests, and publishes an affected component when its Renovate-managed upstream pin is merged into `main`.
Shared configuration, Dockerfile, builder, lifecycle, or image-tooling changes rebuild all configured images.
The initial merge of the image tooling into `main` selects all configured packages and triggers their first publication after builds and tests pass.

The workflow selects affected packages and generates their package × architecture build matrix from [`packaging/images/config.json`](packaging/images/config.json).
Native runners build the DEBs and test the resulting images before publication jobs reuse the tested DEBs to publish multi-architecture images to GitHub Container Registry (GHCR).
Intermediate DEBs are retained as short-lived CI artifacts, not published to package repositories by this workflow.
See [the image documentation](packaging/images/README.md) for contents, naming, and local build commands.

Every merged upstream version receives its own image tag, even when builds finish out of order.
Publication jobs are serialized per component, retaining pending jobs rather than canceling them.
Each publication job checks the current tip of `main` for newer inputs targeting the same component and upstream version, using the affected-component selector.
It yields only to the tip, because a workflow run exists per push and the commits in between — those a rebase-merge or a multi-commit push brings in — never get a run of their own to publish the tag instead.
Only builds the tip itself supersedes are skipped, while unrelated commits and different version tags do not block publication.

Manual dispatch rebuilds and republishes the images for the versions `main` pins at the time it runs, and is not a required release step.
It cannot recover an older tag: the workflow reads each component’s version from the checked-out tree, so a dispatch after a pin has moved on republishes the newer version instead.

```sh
gh workflow run images.yml
```

To recover a tag whose publication failed, re-run the workflow run that was meant to publish it.
A re-run checks out the commit it was originally triggered for, so it reads that commit’s pin and publishes the version that was missed.
Re-running only the failed jobs reuses the DEBs the build jobs already tested, which are retained for 7 days.
Re-running all jobs rebuilds those DEBs from the same commit, which is the option once the artifacts have expired.

Should the run itself no longer be available, restore the pin in a commit on `main`.
The component is then selected through the ordinary path, and its tag is published again.

Publishing is restricted to the official repository’s `main` branch; pull requests and forks build and test without registry writes.
The repository’s Actions token needs permission to create or update its GHCR packages; maintainers should also verify package visibility and anonymous pull access when first enabling publication.

## Building in Fedora COPR

Publishing the release also triggers the [Build in COPR workflow](.github/workflows/copr-build.yml), which builds the release tag in the [`opentelemetry/opentelemetry-packaging`](https://copr.fedorainfracloud.org/coprs/opentelemetry/opentelemetry-packaging/) COPR project.
COPR builds the RPMs with mock from a source RPM, so they come from `rpmbuild` and the generated spec rather than from nfpm, and it publishes them as its own YUM repository alongside the one above.
The workflow waits for the build and fails if any single chroot did not succeed, which the aggregate build state alone would hide.

The workflow can also be dispatched against any branch, tag, or commit, which is how the spec is exercised outside a release.
Without an input it builds the exact commit the run is on.

```sh
gh workflow run copr-build.yml -f committish=main
```

## One-time GitHub Pages setup

Deploying only pushes the `gh-pages` branch; serving it requires GitHub Pages to be enabled once in the repository settings ("Deploy from a branch", branch `gh-pages`, path `/`).
Until Pages is enabled, the publish workflow succeeds but nothing is served at the repository's `github.io` URL.

## Testing the publishing pipeline in a fork

The Publish Package Repositories workflow has no repository guard and computes the repository URL from the repository owner, so it works unmodified in a fork.
Create a release in the fork with `.deb` and `.rpm` assets attached (for example, re-using the artifacts of a Build workflow run), enable GitHub Pages on the `gh-pages` branch, and the repositories publish under the fork's `github.io` URL.
