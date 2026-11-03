# JMX scraper follow-ups

Scratch notes for work deliberately left out of PR #151.
This file is untracked on purpose — do not commit it.

## Start the unit in a test

The metadata test `TestJMXScraperUnitIsHardened` pins the unit's security
directives as shipped in the DEB, but nothing anywhere starts the service.
Every behavioural claim the package makes rests on manual runs that leave no
trace in CI:

- the service reaches `active` under `DynamicUser` and stays there
- the transient user can read `config.properties` and cannot read `jmx-scraper.env`
- a missing Java runtime exits 127, a rejected configuration exits 1
- `Restart=on-failure` leaves a misconfigured service `activating`, not `failed`
- the SASL export is accepted by the JVM rather than rejected as a bad option

A regression in any of those ships green today.

The blocker is harness, not assertions.
`packaging/tests/lifecycle/` starts containers that run `sleep infinity` with
no init, and nothing in the repo has ever started a privileged or
systemd-capable container, so there is no pattern to copy.

What it needs:

- a `Dockerfile.systemd-{deb,rpm}` variant with systemd installed and `/sbin/init` as the command
- a testcontainers start path with `Privileged: true`, `--cgroupns=host`, and the `/sys/fs/cgroup` mount
- a readiness wait on `systemctl is-system-running` returning `running` or `degraded`
- a new row in the `integration-tests` matrix

Worth doing as its own PR.
It carries real flakiness risk on GitHub Actions runners, and that risk should
not be introduced in a PR that is otherwise about packaging a new component.

## Retry the JMX target-system drift check

The `check-jmx-target-systems` step added to the `unit-tests` job downloads the
pinned scraper JAR from GitHub's release CDN with no retry.
Observed failing once locally with `HTTP 504`, then passing immediately on a
rerun, so CI will go red for reasons that have nothing to do with the change
under test.

Either retry the download in `packaging/builder/download.go`, or cache the JAR
in the workflow keyed on `packaging/common/jmx-scraper/release.txt`.
