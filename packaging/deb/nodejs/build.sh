#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Build script for opentelemetry-nodejs-autoinstrumentation DEB package.
#
# Usage: build.sh [VERSION] [ARCH] [OUTPUT_DIR]

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# shellcheck disable=SC1091
. "$SCRIPT_DIR/../common.sh"

PKG_NAME="opentelemetry-nodejs-autoinstrumentation"
PKG_DESCRIPTION="OpenTelemetry Node.js Auto-Instrumentation"

VERSION="${1:-}"
ARCH="${2:-amd64}"
OUTPUT_DIR="${3:-$REPO_DIR/build/packages}"

if [[ -z "$VERSION" ]]; then
    VERSION="$( get_version )"
fi
VERSION="${VERSION#v}"

echo "Building DEB package: ${PKG_NAME} version ${VERSION}"

buildroot="$(mktemp -d)"
trap 'rm -rf "$buildroot"' EXIT

setup_nodejs_buildroot "$ARCH" "$VERSION" "$buildroot"

mkdir -p "$OUTPUT_DIR"

# Node.js agent is architecture-independent (JavaScript)
fpm -s dir -t deb -n "$PKG_NAME" -v "$VERSION" -f -p "$OUTPUT_DIR" \
    --vendor "$PKG_VENDOR" \
    --maintainer "$PKG_MAINTAINER" \
    --description "$PKG_DESCRIPTION" \
    --license "$PKG_LICENSE" \
    --url "$PKG_URL" \
    --architecture "all" \
    --deb-dist "stable" \
    --deb-use-file-permissions \
    --deb-no-default-config-files \
    --provides "opentelemetry-nodejs-autoinstrumentation1" \
    --deb-suggests "opentelemetry-injector1" \
    --deb-suggests "nodejs (>= 18)" \
    --config-files "$NODEJS_CONFIG_DIR" \
    "$buildroot/"=/

echo "Built: ${OUTPUT_DIR}/${PKG_NAME}_${VERSION}_all.deb"

if [[ "${LIST_PACKAGE_CONTENTS_AFTER_BUILD:-}" == "true" ]]; then
    dpkg -c "${OUTPUT_DIR}/${PKG_NAME}_${VERSION}_all.deb"
fi
