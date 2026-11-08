#!/bin/bash

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Build the generated debbuild specs the way the Open Build Service does.
# This script is meant to run inside a Debian or Ubuntu container.
#
# Usage: debbuild-rebuild.sh <sources_dir> <output_dir> [debbuild_version]
#
# Expected structure:
#   <sources_dir>/<package>/<package>.spec
#   <sources_dir>/<package>/<package>-<version>.tar.gz
#
# Every package found is built, and the .deb files are copied to <output_dir>.
#
# debbuild publishes no .deb of its own, so it is installed from the release
# tarball. The version is pinned by the caller: a build that picked up whatever
# is current would turn an upstream change to the macros the specs rely on into
# a failure with no commit behind it.

set -euo pipefail

sources_dir="${1:?usage: debbuild-rebuild.sh <sources_dir> <output_dir> [debbuild_version]}"
output_dir="${2:?usage: debbuild-rebuild.sh <sources_dir> <output_dir> [debbuild_version]}"
debbuild_version="${3:-24.12.0}"

export DEBIAN_FRONTEND=noninteractive

echo "Installing the build dependencies"
# A mirror that times out mid-run must not fail the build on its own.
export APT_OPTS="-o Acquire::Retries=5"
# shellcheck disable=SC2086 # deliberate word splitting of the option list
apt-get update -qq $APT_OPTS
# dpkg-dev provides dpkg-architecture, which debbuild calls to resolve %{_arch};
# pax is what it writes source packages with; the two perl packages are what its
# configure script and its localized messages need. perl-modules is pulled in
# explicitly because the slim images ship perl-base alone, which has no
# File::Spec::Functions for the configure script.
# shellcheck disable=SC2086 # deliberate word splitting of the option list
apt-get install -y -qq --no-install-recommends $APT_OPTS \
    ca-certificates curl dpkg-dev fakeroot gettext liblocale-gettext-perl \
    make pax perl perl-modules xz-utils

echo "Installing debbuild ${debbuild_version}"
mkdir -p /opt/debbuild-src
# Downloaded to a file rather than piped into tar, so that a failed transfer is
# a failed download and not a corrupt archive. --retry covers the 5xx GitHub
# serves intermittently, which would otherwise fail the build for no reason of
# ours; without --retry-all-errors the 503s it returns are not retried.
curl -fsSL --retry 5 --retry-delay 5 --retry-all-errors \
    -o /tmp/debbuild.tar.gz \
    "https://github.com/debbuild/debbuild/archive/refs/tags/${debbuild_version}.tar.gz"
tar -xzf /tmp/debbuild.tar.gz -C /opt/debbuild-src --strip-components=1
(
    cd /opt/debbuild-src
    ./configure --prefix=/usr "VERSION=${debbuild_version}" >/dev/null
    make >/dev/null
    make install >/dev/null
)
debbuild --version

mkdir -p "$output_dir"

# The Open Build Service builds under umask 002, and the container defaults to
# 022. Without this the build is not the one being reproduced: debbuild creates
# the package's directories and recompresses its man pages under the umask it
# inherits, which is the whole reason the specs override %__mkdir_p and
# %__gzip. Under 022 those overrides change nothing, and a package that lost
# them would pass every assertion.
umask 002

built=0
for spec in "$sources_dir"/*/*.spec; do
    [ -e "$spec" ] || continue
    package="$(basename "$(dirname "$spec")")"
    work="/tmp/debbuild/$package"

    echo ""
    echo "=== Building $package ==="
    rm -rf "$work"
    mkdir -p "$work"
    cp -r "$(dirname "$spec")"/. "$work/"

    # debbuild creates none of these itself and fails obscurely without them:
    # the prep script exits non-zero and its output is discarded, leaving only
    # "Exec of /var/tmp/deb-tmp.prep.NNN failed (%prep): 512" in the log.
    mkdir -p "$work"/top/{BUILD,BUILDROOT,DEBS,SDEBS,SOURCES,SPECS}

    # No --define of anything the spec reads: the point is to build it as OBS
    # does, from the spec alone, and catch anything that only works because the
    # generator happened to be the one driving it.
    debbuild --define "_sourcedir $work" --define "_topdir $work/top" \
        -bb "$work/$(basename "$spec")"

    find "$work/top/DEBS" -name '*.deb' -exec cp -v {} "$output_dir/" \;
    built=$((built + 1))
done

if [ "$built" -eq 0 ]; then
    echo "error: no spec found under $sources_dir" >&2
    exit 1
fi

echo ""
echo "Built $built package(s) into $output_dir:"
ls -1 "$output_dir"
