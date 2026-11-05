# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Run the extract stage on the build host so cross-architecture builds do not
# emulate dpkg-deb. BuildKit sets BUILDPLATFORM, and so does buildah -- 1.28,
# 1.33, and 1.43 were checked directly, and all three pin this stage to the
# host either way. The fallback only covers an unset or empty value, where the
# flag would otherwise resolve to the target platform and emulate silently.
FROM --platform=${BUILDPLATFORM:-$TARGETPLATFORM} debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS extract
ARG TARGETARCH
ARG PACKAGE
# Copy the architecture's whole directory rather than a glob: COPY resolves a
# multi-match glob to a single file without complaining, so an ambiguous build
# context would silently ship whichever filename sorts last.
COPY ${TARGETARCH}/ /tmp/packages/
RUN set -eu; \
    matches=$(find /tmp/packages -maxdepth 1 -name "${PACKAGE}_*.deb" | wc -l); \
    if [ "$matches" -ne 1 ]; then \
        echo "expected exactly one ${PACKAGE}_*.deb in ${TARGETARCH}/, found ${matches}" >&2; \
        exit 1; \
    fi; \
    dpkg-deb --extract "$(find /tmp/packages -maxdepth 1 -name "${PACKAGE}_*.deb")" /payload

FROM busybox@sha256:dc2d74b28e4cf8984fa52af1f39bc7c3d9c73760b41a74d629f5d11b1ab28616
COPY --from=extract /payload/ /
