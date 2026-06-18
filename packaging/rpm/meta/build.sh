#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Build script for opentelemetry metapackage (RPM)

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

# shellcheck disable=SC1091
. "$SCRIPT_DIR/../common.sh"

PKG_NAME="opentelemetry"
PKG_DESCRIPTION="OpenTelemetry Auto-Instrumentation Suite (metapackage)"

VERSION="${1:-}"
OUTPUT_DIR="${3:-$REPO_DIR/build/packages}"

if [[ -z "$VERSION" ]]; then
    VERSION="$( get_version )"
fi

# Normalize version for RPM
VERSION="$(normalize_rpm_version "$VERSION")"

echo "Building RPM metapackage: $PKG_NAME version $VERSION"

# Metapackage has no files, just dependencies
buildroot="$(mktemp -d)"
trap 'rm -rf "$buildroot"' EXIT

mkdir -p "$OUTPUT_DIR"

# Create minimal directory structure for the metapackage
local_license_dir="${buildroot}${LICENSE_DIR}/${PKG_NAME}"
mkdir -p "$local_license_dir"
cp -f "$REPO_DIR/LICENSE" "$local_license_dir/"

local_doc_dir="${buildroot}${DOC_DIR}/${PKG_NAME}"
mkdir -p "$local_doc_dir"
echo "OpenTelemetry Auto-Instrumentation Suite" > "${local_doc_dir}/README"

chown -R root:root "$buildroot"

fpm -s dir -t rpm -n "$PKG_NAME" -v "$VERSION" -f -p "$OUTPUT_DIR" \
    --vendor "$PKG_VENDOR" \
    --maintainer "$PKG_MAINTAINER" \
    --description "$PKG_DESCRIPTION" \
    --license "$PKG_LICENSE" \
    --url "$PKG_URL" \
    --architecture "noarch" \
    --rpm-rpmbuild-define "_build_id_links none" \
    --rpm-summary "$PKG_DESCRIPTION" \
    --depends "opentelemetry-injector1" \
    --rpm-tag "Recommends: opentelemetry-java-autoinstrumentation1" \
    --rpm-tag "Recommends: opentelemetry-nodejs-autoinstrumentation1" \
    --rpm-tag "Recommends: opentelemetry-dotnet-autoinstrumentation1" \
    "$buildroot/"=/

echo "Built: ${OUTPUT_DIR}/${PKG_NAME}-${VERSION}-1.noarch.rpm"

if [[ "${LIST_PACKAGE_CONTENTS_AFTER_BUILD:-}" == "true" ]]; then
    rpm -qpli "${OUTPUT_DIR}/${PKG_NAME}-${VERSION}-1.noarch.rpm"
fi
