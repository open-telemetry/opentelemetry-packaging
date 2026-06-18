#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Build script for the opentelemetry metapackage (DEB).
#
# This is a dependency-only package with no files of its own (besides a README).
# It pulls in the injector via a hard dependency and language packages via
# Recommends, all using virtual package names for vendor swappability.
#
# Usage: build.sh [VERSION] [ARCH] [OUTPUT_DIR]
# ARCH is accepted for interface consistency but ignored (metapackage is always "all").

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# shellcheck disable=SC1091
. "$SCRIPT_DIR/../common.sh"

PKG_NAME="opentelemetry"
PKG_DESCRIPTION="OpenTelemetry Auto-Instrumentation Suite (metapackage)"

VERSION="${1:-}"
# $2 is ARCH (ignored for metapackage)
OUTPUT_DIR="${3:-$REPO_DIR/build/packages}"

if [[ -z "$VERSION" ]]; then
    VERSION="$( get_version )"
fi
VERSION="${VERSION#v}"

echo "Building DEB metapackage: ${PKG_NAME} version ${VERSION}"

buildroot="$(mktemp -d)"
trap 'rm -rf "$buildroot"' EXIT

# Metapackage has no files of its own, just a README under /usr/share/doc/
mkdir -p "${buildroot}${DOC_DIR}/${PKG_NAME}"
echo "OpenTelemetry Auto-Instrumentation Suite" > "${buildroot}${DOC_DIR}/${PKG_NAME}/README"
chown -R root:root "$buildroot"

mkdir -p "$OUTPUT_DIR"

fpm -s dir -t deb -n "$PKG_NAME" -v "$VERSION" -f -p "$OUTPUT_DIR" \
    --vendor "$PKG_VENDOR" \
    --maintainer "$PKG_MAINTAINER" \
    --description "$PKG_DESCRIPTION" \
    --license "$PKG_LICENSE" \
    --url "$PKG_URL" \
    --architecture "all" \
    --deb-dist "stable" \
    --depends "opentelemetry-injector1" \
    --deb-recommends "opentelemetry-java-autoinstrumentation1" \
    --deb-recommends "opentelemetry-nodejs-autoinstrumentation1" \
    --deb-recommends "opentelemetry-dotnet-autoinstrumentation1" \
    "$buildroot/"=/

echo "Built: ${OUTPUT_DIR}/${PKG_NAME}_${VERSION}_all.deb"

if [[ "${LIST_PACKAGE_CONTENTS_AFTER_BUILD:-}" == "true" ]]; then
    dpkg -c "${OUTPUT_DIR}/${PKG_NAME}_${VERSION}_all.deb"
fi
