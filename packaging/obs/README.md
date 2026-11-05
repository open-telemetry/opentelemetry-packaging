# OBS client

This package is a minimal client for the [Open Build Service](https://build.opensuse.org/) (OBS) API.
It does the three things the [Build on OBS workflow](../../.github/workflows/obs.yml) needs: commit a package's sources, wait for them to build, and release the package.
[`cmd/obs-publish`](../../cmd/obs-publish) runs those steps for one package.

It is a shim in place of [`osc`](https://github.com/openSUSE/osc), the OBS command-line client.
It sends the same HTTP requests `osc` sends for these steps, and nothing else.

## Why not `osc`

`osc` is hard to run on a GitHub runner.
It is published only as RPM packages in openSUSE's [`openSUSE:Tools`](https://download.opensuse.org/repositories/openSUSE:/Tools/) repository, or built from its [sources](https://github.com/openSUSE/osc), not on PyPI.
It also needs the system `rpm` Python bindings, which pip cannot install, so the Ubuntu runners would need an openSUSE container just to run it.
It would also need the password written to its configuration file on the runner, and its `results --watch` can report the previous revision as built right after a commit.
The shim needs nothing beyond `go run`, takes the credentials from `OBS_USERNAME` and `OBS_PASSWORD`, and waits for the committed revision itself.
The `osc` sources remain the reference for the requests below.

## What it does

[`Publish`](publish.go) uploads one directory as a package's complete sources.

1. It lists the package's current sources, and creates the package if it does not exist.
2. If the local files have the same names and MD5 sums, it stops: there is nothing to commit or release. Regenerated sources of an unchanged package are byte-identical, because their timestamps come from `SOURCE_DATE_EPOCH`.
3. Otherwise it commits the directory the way `osc commit` does. It posts the file list with `cmd=commitfilelist`, uploads the files the server reports missing, and posts the list again. Files the package held that are not in the list are removed, such as the archive of the previous version.
4. It waits until the new revision has built in every repository and architecture. It checks each one's job history (`_jobhistory`) for a job with the committed `srcmd5`, and fails at once on `excluded`, or on a lasting `failed`, `broken`, or `unresolvable` status.
5. If asked, it releases the package with `cmd=release`, as `osc release` does, which copies it to the project's release targets.

A run that committed but failed before releasing leaves sources the next run sees as unchanged.
`ReleaseUnchanged` (`-release-unchanged`) covers that case: it waits for the builds of the current sources and releases them.

## Endpoints

| Step                  | Request                                                                          |
| --------------------- | -------------------------------------------------------------------------------- |
| List sources          | `GET /source/<project>/<package>`                                                |
| Create the package    | `PUT /source/<project>/<package>/_meta`                                          |
| Commit                | `POST /source/<project>/<package>?cmd=commitfilelist`                            |
| Upload a missing file | `PUT /source/<project>/<package>/<file>?rev=repository`                          |
| Build status          | `GET /build/<project>/_result?package=<package>`                                 |
| Last finished job     | `GET /build/<project>/<repository>/<arch>/_jobhistory?package=<package>&limit=1` |
| Release               | `POST /source/<project>/<package>?cmd=release`                                   |

Anything else, such as inspecting a project, downloading binaries, or changing project configuration, is done with `osc` by hand.
