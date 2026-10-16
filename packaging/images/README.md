# Package images

These images distribute the contents of individual OpenTelemetry packages as container images. Image publication follows the packages’ upstream version pins independently of the system-package release cycle.

## Supported packages and architectures

[`config.json`](config.json) defines the supported packages and architectures. Every configured package is published for every configured architecture. The CI build matrix, publication platforms, and package-content tests use this configuration. Each architecture entry specifies the native CI runner used to test it.

TODO: Expand `config.json` to include all other auto-instrumentation packages, including Node.js, .NET, and Python.

## Contents

The shared [Dockerfile](Dockerfile) extracts the selected DEB in an intermediate Debian image without running installation hooks. It copies every package-owned file and directory into the final BusyBox image, preserving the package’s paths, permissions, and symbolic links, including configuration files, documentation, and man pages. Package-manager state and files created by installation hooks, such as `/etc/ld.so.preload`, are not copied.

BusyBox supplies `cp` for consumers that copy package contents into another filesystem. The images inherit the base image’s default user, group, entrypoint, and command. No compatibility aliases or image-specific configuration files are added.

## Image names and versions

Image names match the package names defined by the package builder. Full image references have this form:

```text
ghcr.io/open-telemetry/opentelemetry-packaging/<package-name>:<upstream-version>
```

For example, the Java package at upstream version `2.32.0` produces:

```text
ghcr.io/open-telemetry/opentelemetry-packaging/opentelemetry-java-autoinstrumentation:2.32.0
```

The upstream version comes from `packaging/common/<component>/release.txt`, the same Renovate-managed pin used by system-package builds, without the leading `v`. Repeated builds replace the corresponding tag, so use image digests for immutable references.

TODO: Define a way to version image construction independently of the enclosed resources, including shared construction changes that affect all images.

## Publication

Images are published automatically from accepted upstream version pins, independently of system-package releases. Each merged upstream version receives its own tag, even if a newer version merges before publication. See [RELEASING.md](../../RELEASING.md#package-images) for workflow triggers, testing, and publication procedures.

## Make tasks

| Task | Description |
| --- | --- |
| `make images [ARCH=<arch>]` | Build images for all configured packages with an optional architecture override (default: `amd64`). |
| `make image-<component>` | Build the selected package’s DEB and image. |
| `make image-packages-<component>` | Build only the intermediate DEB used by the image. |
| `make integration-test-images [ARCH=<arch>]` | Build and test all configured package images with an optional architecture override (default: `amd64`). |
| `make integration-test-image-<component>` | Build the selected package’s image and test its contents and `cp` command. |

`<component>` is a configured package component, such as `injector` or `java`.

Image integration tests compare every package-owned path, file permission, symbolic link, and file checksum against the DEB, check that installation state is absent, and test `cp` with a read-only root filesystem. These tests also run as part of `make integration-tests`.
