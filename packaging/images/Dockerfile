# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

FROM --platform=$BUILDPLATFORM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS extract
ARG TARGETARCH
ARG PACKAGE
COPY ${TARGETARCH}/${PACKAGE}_*.deb /tmp/package.deb
RUN dpkg-deb --extract /tmp/package.deb /payload

FROM busybox@sha256:dc2d74b28e4cf8984fa52af1f39bc7c3d9c73760b41a74d629f5d11b1ab28616
COPY --from=extract /payload/ /
