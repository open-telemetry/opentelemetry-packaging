#!/usr/bin/env bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Build the generated OBS specs with rpmbuild the way the Open Build Service does.
# This script is meant to run inside a Fedora or EL container.
#
# Usage: rpm-obs-rebuild.sh <sources_dir> <output_dir>
#
# Expected structure:
#   <sources_dir>/<package>/<package>.spec
#   <sources_dir>/<package>/<package>-<version>.tar.gz
#
# Every package found is built, and the .rpm files are copied to <output_dir>.

set -euo pipefail

sources_dir="${1:?usage: rpm-obs-rebuild.sh <sources_dir> <output_dir>}"
output_dir="${2:?usage: rpm-obs-rebuild.sh <sources_dir> <output_dir>}"

echo "Installing the build dependencies"
# The payload is prebuilt, so rpmbuild is all a build needs.
dnf install -q -y --setopt=install_weak_deps=False rpm-build tar gzip >/dev/null

mkdir -p "$output_dir"

# The Open Build Service builds under umask 002, and the container defaults to 022.
# Without this the build is not the one being reproduced.
umask 002

built=0
for spec in "$sources_dir"/*/*.spec; do
    [ -e "$spec" ] || continue
    package="$(basename "$(dirname "$spec")")"

    work="/tmp/rpmbuild/$package"

    echo ""
    echo "=== Building $package ==="
    rm -rf "$work"
    mkdir -p "$work"
    cp -r "$(dirname "$spec")"/. "$work/"

    # No --define of anything the spec reads.
    # The point is to build it as OBS does, from the spec alone, and catch anything that only works because the generator happened to be the one driving it.
    rpmbuild --define "_sourcedir $work" --define "_topdir $work/top" \
        -bb "$work/$(basename "$spec")"

    find "$work/top/RPMS" -name '*.rpm' -exec cp -v {} "$output_dir/" \;
    built=$((built + 1))
done

if [ "$built" -eq 0 ]; then
    echo "error: no spec found under $sources_dir" >&2
    exit 1
fi

echo ""
echo "Built $built package(s) into $output_dir:"
ls -1 "$output_dir"
