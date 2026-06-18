#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Build script for opentelemetry-dotnet-autoinstrumentation RPM package

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# shellcheck disable=SC1091
. "$SCRIPT_DIR/../common.sh"

PKG_NAME="opentelemetry-dotnet-autoinstrumentation"
PKG_DESCRIPTION="OpenTelemetry .NET Automatic Instrumentation"

VERSION="${1:-}"
ARCH="${2:-amd64}"
OUTPUT_DIR="${3:-$REPO_DIR/build/packages}"

if [[ -z "$VERSION" ]]; then
    VERSION="$( get_version )"
fi

# Export ARCH so download_dotnet_agent can use it
export ARCH

# Normalize version for RPM
VERSION="$(normalize_rpm_version "$VERSION")"
RPM_ARCH="$(convert_arch_for_rpm "$ARCH")"

echo "Building RPM package: $PKG_NAME version $VERSION for $RPM_ARCH"

buildroot="$(mktemp -d)"
trap 'rm -rf "$buildroot"' EXIT

setup_dotnet_buildroot "$ARCH" "$VERSION" "$buildroot"

mkdir -p "$OUTPUT_DIR"

# .NET agent contains architecture-specific native libraries
fpm -s dir -t rpm -n "$PKG_NAME" -v "$VERSION" -f -p "$OUTPUT_DIR" \
    --vendor "$PKG_VENDOR" \
    --maintainer "$PKG_MAINTAINER" \
    --description "$PKG_DESCRIPTION" \
    --license "$PKG_LICENSE" \
    --url "$PKG_URL" \
    --architecture "$RPM_ARCH" \
    --provides "opentelemetry-dotnet-autoinstrumentation1" \
    --rpm-rpmbuild-define "_build_id_links none" \
    --rpm-summary "$PKG_DESCRIPTION" \
    --rpm-use-file-permissions \
    --rpm-tag "Suggests: opentelemetry-injector1" \
    --config-files "$DOTNET_CONFIG_DIR" \
    "$buildroot/"=/

echo "Built: ${OUTPUT_DIR}/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}.rpm"

if [[ "${LIST_PACKAGE_CONTENTS_AFTER_BUILD:-}" == "true" ]]; then
    rpm -qpli "${OUTPUT_DIR}/${PKG_NAME}-${VERSION}-1.${RPM_ARCH}.rpm"
fi
