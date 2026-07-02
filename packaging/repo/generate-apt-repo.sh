#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Generate APT repository metadata from .deb packages
# This script is meant to run inside a Debian-based container.
#
# Usage: generate-apt-repo.sh <repo_dir>
#
# Expected structure:
#   <repo_dir>/pool/*.deb
#
# Generated structure:
#   <repo_dir>/dists/stable/Release
#   <repo_dir>/dists/stable/main/binary-amd64/Packages[.gz]
#   <repo_dir>/dists/stable/main/binary-arm64/Packages[.gz]
#   <repo_dir>/dists/stable/main/binary-all/Packages[.gz]

set -euo pipefail

REPO_DIR="${1:-.}"

# Install dependencies if not present
if ! command -v dpkg-scanpackages &>/dev/null; then
    apt-get update -qq && apt-get install -y -qq dpkg-dev gzip
fi

cd "$REPO_DIR"

if [ ! -d pool ] || [ -z "$(ls -A pool/*.deb 2>/dev/null)" ]; then
    echo "ERROR: No .deb packages found in ${REPO_DIR}/pool/" >&2
    exit 1
fi

# Create directory structure
mkdir -p dists/stable/main/binary-amd64
mkdir -p dists/stable/main/binary-arm64
mkdir -p dists/stable/main/binary-all

# Generate Packages files for each architecture
# Note: dpkg-scanpackages needs the path relative to the repo root
dpkg-scanpackages --arch amd64 pool > dists/stable/main/binary-amd64/Packages 2>/dev/null || true
dpkg-scanpackages --arch arm64 pool > dists/stable/main/binary-arm64/Packages 2>/dev/null || true
dpkg-scanpackages --arch all pool > dists/stable/main/binary-all/Packages 2>/dev/null || true

# Compress Packages files
gzip -kf dists/stable/main/binary-amd64/Packages
gzip -kf dists/stable/main/binary-arm64/Packages
gzip -kf dists/stable/main/binary-all/Packages

# Generate Release file
cd dists/stable
cat > Release << 'EOF'
Origin: OpenTelemetry
Label: OpenTelemetry
Suite: stable
Codename: stable
Architectures: amd64 arm64 all
Components: main
Description: OpenTelemetry auto-instrumentation packages for Linux
EOF

# APT requires a valid Date field. Without it, newer apt versions reject the
# repository metadata ("W: Invalid 'Date' entry in Release file") and fail to
# locate any package. The date must be in RFC 5322 format and in UTC.
echo "Date: $(date -u '+%a, %d %b %Y %H:%M:%S UTC')" >> Release

# Add checksums to Release file
echo "MD5Sum:" >> Release
for f in main/binary-*/Packages*; do
    [ -f "$f" ] && echo " $(md5sum "$f" | cut -d" " -f1) $(stat -c%s "$f") $f" >> Release
done

echo "SHA256:" >> Release
for f in main/binary-*/Packages*; do
    [ -f "$f" ] && echo " $(sha256sum "$f" | cut -d" " -f1) $(stat -c%s "$f") $f" >> Release
done

echo "=== APT Repository Generated ==="
cat Release
