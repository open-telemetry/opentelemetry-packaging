#!/bin/bash
# shellcheck disable=SC2034 # Variables are used by build scripts that source this file

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Common functions and variables for building DEB packages.
#
# This file is sourced by each per-component build.sh script. It provides:
#   - Standard path and metadata variables
#   - Version helpers (git-based and release-file-based)
#   - Download functions for upstream artifacts
#   - Man page generation
#   - Buildroot setup functions that populate the package filesystem tree

set -euo pipefail

# ---------------------------------------------------------------------------
# Directory layout
# ---------------------------------------------------------------------------
DEB_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PACKAGING_DIR="$( cd "$DEB_DIR/.." && pwd )"
REPO_DIR="$( cd "$PACKAGING_DIR/.." && pwd )"
COMMON_DIR="$PACKAGING_DIR/common"

# ---------------------------------------------------------------------------
# Package metadata
# ---------------------------------------------------------------------------
PKG_VENDOR="OpenTelemetry"
PKG_MAINTAINER="OpenTelemetry"
PKG_LICENSE="Apache 2.0"
PKG_URL="https://github.com/open-telemetry/opentelemetry-packaging"

# ---------------------------------------------------------------------------
# Installation paths (FHS-compliant)
# ---------------------------------------------------------------------------
INSTALL_DIR="/usr/lib/opentelemetry"
CONFIG_DIR="/etc/opentelemetry"
DOC_DIR="/usr/share/doc"
MAN_DIR="/usr/share/man"

# Injector
INJECTOR_INSTALL_DIR="${INSTALL_DIR}/injector"
INJECTOR_CONFIG_DIR="${CONFIG_DIR}/injector"
LIBOTELINJECT_INSTALL_PATH="${INJECTOR_INSTALL_DIR}/libotelinject.so"

# Java
JAVA_INSTALL_DIR="${INSTALL_DIR}/java"
JAVA_CONFIG_DIR="${CONFIG_DIR}/java"
JAVA_AGENT_INSTALL_PATH="${JAVA_INSTALL_DIR}/opentelemetry-javaagent.jar"

# Node.js
NODEJS_INSTALL_DIR="${INSTALL_DIR}/nodejs"
NODEJS_CONFIG_DIR="${CONFIG_DIR}/nodejs"

# .NET
DOTNET_INSTALL_DIR="${INSTALL_DIR}/dotnet"
DOTNET_CONFIG_DIR="${CONFIG_DIR}/dotnet"

# ---------------------------------------------------------------------------
# Release pin files
# ---------------------------------------------------------------------------
INJECTOR_RELEASE_PATH="${PACKAGING_DIR}/injector-release.txt"
JAVA_AGENT_RELEASE_PATH="${PACKAGING_DIR}/java-agent-release.txt"
NODEJS_AGENT_RELEASE_PATH="${PACKAGING_DIR}/nodejs-agent-release.txt"
DOTNET_AGENT_RELEASE_PATH="${PACKAGING_DIR}/dotnet-agent-release.txt"

# ---------------------------------------------------------------------------
# Upstream download base URLs
# ---------------------------------------------------------------------------
INJECTOR_RELEASE_URL="https://github.com/open-telemetry/opentelemetry-injector/releases"
JAVA_AGENT_RELEASE_URL="https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases"
DOTNET_ARTIFACT_BASE_NAME="opentelemetry-dotnet-instrumentation"
DOTNET_OS_NAME="linux"
DOTNET_AGENT_RELEASE_URL="https://github.com/open-telemetry/${DOTNET_ARTIFACT_BASE_NAME}/releases/download"

# ---------------------------------------------------------------------------
# Version helpers
# ---------------------------------------------------------------------------

# get_version — derive the package version from git tags.
#
# Uses the v[0-9]* tag pattern. Returns:
#   - The exact tag (e.g. "v1.2.3") when HEAD is tagged
#   - "<latest-tag>-post" when HEAD is ahead of the latest tag
#   - "0.0.0-dev" when no tags exist at all
get_version() {
    local commit_tag latest_tag
    commit_tag="$( git -C "$REPO_DIR" describe --abbrev=0 --tags --exact-match --match 'v[0-9]*' 2>/dev/null || true )"
    if [[ -z "$commit_tag" ]]; then
        latest_tag="$( git -C "$REPO_DIR" describe --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true )"
        if [[ -n "$latest_tag" ]]; then
            echo "${latest_tag}-post"
        else
            echo "0.0.0-dev"
        fi
    else
        echo "$commit_tag"
    fi
}

# read_release_version — read a pinned version from a release file.
#
# The file format is:
#   # renovate: datasource=... depName=...
#   v1.2.3
#
# Comments (lines starting with #) and blank lines are skipped.
# A leading "v" is stripped from the version string.
#
# Usage: read_release_version <file>
read_release_version() {
    local file="$1"
    local version=""
    while IFS= read -r line || [[ -n "$line" ]]; do
        # Skip comments and blank lines
        case "$line" in
            '#'*|'') continue ;;
        esac
        version="$line"
    done < "$file"
    # Strip leading "v" if present
    echo "${version#v}"
}

# ---------------------------------------------------------------------------
# Download functions
# ---------------------------------------------------------------------------

# download_injector — fetch libotelinject.so from the injector repo's GitHub
# releases.
#
# The release asset is named libotelinject_<arch>.so (e.g.
# libotelinject_amd64.so).
#
# Usage: download_injector <version_tag> <arch> <dest_path>
download_injector() {
    local tag="$1"
    local arch="$2"
    local dest="$3"
    local asset_name="libotelinject_${arch}.so"
    local dl_url="${INJECTOR_RELEASE_URL}/download/${tag}/${asset_name}"

    echo "Downloading injector from ${dl_url} ..."
    mkdir -p "$( dirname "$dest" )"
    curl -sfL "$dl_url" -o "$dest"
}

# download_java_agent — fetch the OpenTelemetry Java agent JAR from GitHub
# releases.
#
# Usage: download_java_agent <version_tag> <dest_path>
download_java_agent() {
    local tag="$1"
    local dest="$2"
    local dl_url=""
    if [[ "$tag" = "latest" ]]; then
        dl_url="${JAVA_AGENT_RELEASE_URL}/latest/download/opentelemetry-javaagent.jar"
    else
        dl_url="${JAVA_AGENT_RELEASE_URL}/download/${tag}/opentelemetry-javaagent.jar"
    fi

    echo "Downloading Java agent from ${dl_url} ..."
    mkdir -p "$( dirname "$dest" )"
    curl -sfL "$dl_url" -o "$dest"
}

# download_nodejs_agent — fetch the OpenTelemetry Node.js auto-instrumentation
# package from npm and install it locally.
#
# Usage: download_nodejs_agent <version_tag> <dest_dir>
download_nodejs_agent() {
    local tag="$1"
    local dest_dir="$2"

    pushd "$dest_dir" > /dev/null
    mkdir -p "nodejs"
    pushd "nodejs" > /dev/null

    export NPM_CONFIG_UPDATE_NOTIFIER=false
    npm --loglevel=warn pack "@opentelemetry/auto-instrumentations-node@${tag#v}"
    mv ./*.tgz opentelemetry-auto-instrumentations-node.tgz
    npm --loglevel=warn --no-fund install --global=false opentelemetry-auto-instrumentations-node.tgz
    rm opentelemetry-auto-instrumentations-node.tgz

    popd > /dev/null
    popd > /dev/null
}

# download_dotnet_agent — fetch the OpenTelemetry .NET auto-instrumentation
# archives for both glibc and musl.
#
# Downloads and extracts the glibc archive fully, then overlays ONLY the
# native library directory (linux-musl-x64/) from the musl archive. Shared
# managed assemblies are stored once, following the OTel Operator approach.
#
# Usage: download_dotnet_agent <version_tag> <arch> <dest_dir>
download_dotnet_agent() {
    local tag="$1"
    local arch="$2"
    local dest="$3"

    local dotnet_arch
    case "$arch" in
        amd64) dotnet_arch="x64" ;;
        arm64) dotnet_arch="arm64" ;;
        *)
            echo "Unsupported architecture: ${arch}. Supported values: amd64, arm64." >&2
            exit 1
            ;;
    esac

    # 1. Download and extract the full glibc distribution
    local glibc_pkg="${DOTNET_ARTIFACT_BASE_NAME}-${DOTNET_OS_NAME}-glibc-${dotnet_arch}.zip"
    local glibc_url="${DOTNET_AGENT_RELEASE_URL}/${tag}/${glibc_pkg}"

    echo "Downloading .NET agent (glibc) from ${glibc_url} ..."
    curl -sSfL "$glibc_url" -o "/tmp/${glibc_pkg}"
    mkdir -p "$dest"
    unzip -qo "/tmp/${glibc_pkg}" -d "$dest"
    rm -f "/tmp/${glibc_pkg}"

    # 2. Download the musl distribution and overlay only the native library
    #    directory (linux-musl-x64/). Shared managed assemblies from the glibc
    #    extraction are already in place.
    local musl_pkg="${DOTNET_ARTIFACT_BASE_NAME}-${DOTNET_OS_NAME}-musl-${dotnet_arch}.zip"
    local musl_url="${DOTNET_AGENT_RELEASE_URL}/${tag}/${musl_pkg}"
    local musl_native_dir="linux-musl-${dotnet_arch}"
    local musl_tmp
    musl_tmp="$(mktemp -d)"

    echo "Downloading .NET agent (musl) from ${musl_url} ..."
    curl -sSfL "$musl_url" -o "/tmp/${musl_pkg}"
    unzip -qo "/tmp/${musl_pkg}" -d "$musl_tmp"
    rm -f "/tmp/${musl_pkg}"

    # Copy only the musl-specific native library directory into dest
    if [[ -d "${musl_tmp}/${musl_native_dir}" ]]; then
        cp -a "${musl_tmp}/${musl_native_dir}" "${dest}/${musl_native_dir}"
    else
        echo "Warning: expected directory ${musl_native_dir} not found in musl archive" >&2
    fi
    rm -rf "$musl_tmp"
}

# ---------------------------------------------------------------------------
# Man page generation
# ---------------------------------------------------------------------------

# generate_man_page — expand @VERSION@ and @DATE@ placeholders in a man page
# template, compress with gzip -9.
#
# Usage: generate_man_page <template_file> <output_file.gz> <version>
generate_man_page() {
    local template="$1"
    local output="$2"
    local version="$3"
    local date
    date="$(date +"%B %Y")"

    mkdir -p "$(dirname "$output")"
    sed -e "s/@VERSION@/$version/g" -e "s/@DATE@/$date/g" "$template" | gzip -9 > "$output"
}

# ---------------------------------------------------------------------------
# Buildroot setup functions
# ---------------------------------------------------------------------------

# setup_injector_buildroot — populate the filesystem tree for the injector DEB.
#
# Downloads libotelinject.so, installs configs, man page, and documentation.
#
# Usage: setup_injector_buildroot <arch> <version> <buildroot>
setup_injector_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local injector_release
    injector_release="$(read_release_version "$INJECTOR_RELEASE_PATH")"
    local pkg_name="opentelemetry-injector"

    # Download and install the shared library
    mkdir -p "${buildroot}${INJECTOR_INSTALL_DIR}"
    download_injector "v${injector_release}" "$arch" "${buildroot}${LIBOTELINJECT_INSTALL_PATH}"
    chmod 755 "${buildroot}${LIBOTELINJECT_INSTALL_PATH}"

    # Install configuration files
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}"
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/injector/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/"
    cp -f "$COMMON_DIR/injector/default_env.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/injector.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/default_env.conf"
    chmod 755 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"

    # Install man page (section 8 — system administration)
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/injector/opentelemetry-injector.8.tmpl" \
        "$man_dir/opentelemetry-injector.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/injector/README.md" "$doc_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}

# setup_java_buildroot — populate the filesystem tree for the Java DEB.
#
# Downloads the Java agent JAR, installs the conf.d drop-in, config, man page,
# and documentation.
#
# Usage: setup_java_buildroot <arch> <version> <buildroot>
setup_java_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local java_agent_release
    java_agent_release="$(read_release_version "$JAVA_AGENT_RELEASE_PATH")"
    local pkg_name="opentelemetry-java-autoinstrumentation"

    # Download and install Java agent
    mkdir -p "${buildroot}${JAVA_INSTALL_DIR}"
    download_java_agent "v${java_agent_release}" "${buildroot}${JAVA_AGENT_INSTALL_PATH}"
    chmod 644 "${buildroot}${JAVA_AGENT_INSTALL_PATH}"

    # Install configuration
    mkdir -p "${buildroot}${JAVA_CONFIG_DIR}"
    cp -f "$COMMON_DIR/java/otel-config.yaml" "${buildroot}${JAVA_CONFIG_DIR}/"
    chmod 644 "${buildroot}${JAVA_CONFIG_DIR}/otel-config.yaml"

    # Install injector conf.d drop-in
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/java/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/java.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/java.conf"

    # Install man page (section 8)
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/java/opentelemetry-java.8.tmpl" \
        "$man_dir/opentelemetry-java.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/java/README.md" "$doc_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}

# setup_nodejs_buildroot — populate the filesystem tree for the Node.js DEB.
#
# Downloads the Node.js auto-instrumentation from npm, installs the conf.d
# drop-in, config, man page, and documentation.
#
# Usage: setup_nodejs_buildroot <arch> <version> <buildroot>
setup_nodejs_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local nodejs_agent_release
    nodejs_agent_release="$(read_release_version "$NODEJS_AGENT_RELEASE_PATH")"
    local pkg_name="opentelemetry-nodejs-autoinstrumentation"

    # Download and install Node.js agent
    mkdir -p "${buildroot}${INSTALL_DIR}"
    download_nodejs_agent "v${nodejs_agent_release}" "${buildroot}${INSTALL_DIR}"
    chmod -R 755 "${buildroot}${NODEJS_INSTALL_DIR}"

    # Install configuration
    mkdir -p "${buildroot}${NODEJS_CONFIG_DIR}"
    cp -f "$COMMON_DIR/nodejs/otel-config.yaml" "${buildroot}${NODEJS_CONFIG_DIR}/"
    chmod 644 "${buildroot}${NODEJS_CONFIG_DIR}/otel-config.yaml"

    # Install injector conf.d drop-in
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/nodejs/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/nodejs.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/nodejs.conf"

    # Install man page (section 8)
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/nodejs/opentelemetry-nodejs.8.tmpl" \
        "$man_dir/opentelemetry-nodejs.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/nodejs/README.md" "$doc_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}

# setup_dotnet_buildroot — populate the filesystem tree for the .NET DEB.
#
# Downloads both glibc and musl .NET distributions. The glibc archive is
# extracted fully; only the native library directory (linux-musl-x64/) from
# the musl archive is overlaid. Shared managed assemblies are stored once.
#
# Usage: setup_dotnet_buildroot <arch> <version> <buildroot>
setup_dotnet_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local dotnet_agent_release
    dotnet_agent_release="$(read_release_version "$DOTNET_AGENT_RELEASE_PATH")"
    local pkg_name="opentelemetry-dotnet-autoinstrumentation"

    # Download and install .NET agent (glibc + musl native overlay)
    mkdir -p "${buildroot}${DOTNET_INSTALL_DIR}"
    download_dotnet_agent "v${dotnet_agent_release}" "$arch" "${buildroot}${DOTNET_INSTALL_DIR}"
    chmod -R 755 "${buildroot}${DOTNET_INSTALL_DIR}"

    # Install configuration
    mkdir -p "${buildroot}${DOTNET_CONFIG_DIR}"
    cp -f "$COMMON_DIR/dotnet/otel-config.yaml" "${buildroot}${DOTNET_CONFIG_DIR}/"
    chmod 644 "${buildroot}${DOTNET_CONFIG_DIR}/otel-config.yaml"

    # Install injector conf.d drop-in
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/dotnet/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/dotnet.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/dotnet.conf"

    # Install man page (section 8)
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/dotnet/opentelemetry-dotnet.8.tmpl" \
        "$man_dir/opentelemetry-dotnet.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/dotnet/README.md" "$doc_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}
