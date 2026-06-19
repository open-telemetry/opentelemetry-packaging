#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Generate RPM repository metadata from .rpm packages
# This script is meant to run inside a Fedora/RHEL-based container.
#
# Usage: generate-rpm-repo.sh <repo_dir>
#
# Expected structure:
#   <repo_dir>/packages/*.rpm
#
# Generated structure:
#   <repo_dir>/repodata/repomd.xml
#   <repo_dir>/repodata/primary.xml.gz
#   <repo_dir>/repodata/filelists.xml.gz
#   <repo_dir>/repodata/other.xml.gz
#
# IMPORTANT: This script uses createrepo_c (not legacy createrepo) to
# preserve weak dependency metadata (Suggests/Recommends) in the repo index.

set -euo pipefail

REPO_DIR="${1:-.}"

# Install dependencies if not present
if ! command -v createrepo_c &>/dev/null; then
    dnf install -y -q createrepo_c
fi

if [ ! -d "$REPO_DIR/packages" ] || [ -z "$(ls -A "$REPO_DIR"/packages/*.rpm 2>/dev/null)" ]; then
    echo "ERROR: No .rpm packages found in ${REPO_DIR}/packages/" >&2
    exit 1
fi

# Generate repository metadata inside packages/ where the RPMs live.
# createrepo_c preserves weak dependency metadata (Suggests, Recommends)
# that legacy createrepo silently drops.
createrepo_c "$REPO_DIR/packages"

echo "=== RPM Repository Generated ==="
ls -la "$REPO_DIR/packages/repodata/"
