# OpenTelemetry Collector Brew packages

## Status

Proposed

## Context

We have the [Homebrew Tap repository](https://github.com/open-telemetry/homebrew-tap) for distributing OpenTelemetry components.
(It is a separate repository because its name has impact on `brew tap` ergonomics.)
We propose to extend it with formulas for some distributions of the OpenTelemetry Collector from the [OpenTelemetry Collector Releases repository](https://github.com/open-telemetry/opentelemetry-collector-releases) focused on the "local development" use-case.

## OpenTelemetry Collector Formulas

The mechanics of how to expose more formulas in the repo are well understood.
But there are several "product" decisions we need to take.

### Which distributions?

As of the time of writing, there are the following releases in the OpenTelemetry Collector Releases repository:

* `otelcol` (a.k.a. "core")
* `otelcol-contrib`
* `otelcol-otlp`
* `otelcol-k8s`
* `otelcol-ebpf-profiler`

We propose starting with the core and contrib distributions.
The eBPF profiler, which is currently Linux-only, is a follow-up for `brew` on Linux.
The `otelcol-k8s` and `otelcol-otlp` do not seem useful in a Mac OS "local development" setup: on K8s one would install the collector with the Helm chart or the operator, and the `otelcol-otlp` distribution is a "pass-through" that does not allow to add info about where the data is coming from; for the local development use-case, "core" sounds minimal enough.

### Should the Collector be a system service?

Yes, through `brew services`.

Offering a service carries no installation-related risk: Homebrew never starts a service on install.
Starting is always an explicit user action that comes in two forms: `brew services run` starts the collector immediately without registering it, while `brew services start` also registers it to start at every login.
We propose running in Homebrew's user scope rather than as a root-owned boot daemon: the collectors generally need no elevation, although some contrib receivers might, or need additional groups like the `journaldreceiver` on Linux.

For restart behaviour, we propose restarting after a crash but not after a configuration error.
A bad configuration then fails visibly and stays down, instead of crash-looping where nobody is watching.
This distinction holds on macOS, where launchd separates a crash signal from an ordinary non-zero exit.
However, Homebrew's systemd translation does not seem to work this way, so on Linux a misconfigured collector is retried until systemd's start limit stops it.

Unfortunately Homebrew does not seem to have a failure-notification mechanism: A collector that dies is discoverable only through `brew services list` and its logs.
Users who need a stronger guarantee should monitor the collector's own health check endpoint.

### Which configuration by default?

We propose shipping upstream's default configuration verbatim.

Someone installing the Collector from the tap should get the same behaviour they would get from the upstream archive, the deb, or the container image.
Upstream documentation, configuration advice, and community troubleshooting all assume that configuration, and a tap-specific default would leave users debugging a difference we invented.