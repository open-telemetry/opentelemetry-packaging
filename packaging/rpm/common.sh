#!/bin/bash
# shellcheck disable=SC2034 # Some variables are used by scripts that source this file

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Common functions and variables for building RPM packages

set -euo pipefail

RPM_PKG_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PACKAGING_DIR="$( cd "$RPM_PKG_DIR/.." && pwd )"
REPO_DIR="$( cd "$PACKAGING_DIR/.." && pwd )"
COMMON_DIR="$PACKAGING_DIR/common"

# Package metadata
PKG_VENDOR="OpenTelemetry"
PKG_MAINTAINER="OpenTelemetry"
PKG_LICENSE="Apache 2.0"
PKG_URL="https://github.com/open-telemetry/opentelemetry-packaging"

# Installation paths (FHS compliant per OTEP #4793)
INSTALL_DIR="/usr/lib/opentelemetry"
CONFIG_DIR="/etc/opentelemetry"
DOC_DIR="/usr/share/doc"
LICENSE_DIR="/usr/share/licenses"
MAN_DIR="/usr/share/man"

# Injector paths
INJECTOR_INSTALL_DIR="${INSTALL_DIR}/injector"
INJECTOR_CONFIG_DIR="${CONFIG_DIR}/injector"
LIBOTELINJECT_INSTALL_PATH="${INJECTOR_INSTALL_DIR}/libotelinject.so"

# Java paths
JAVA_INSTALL_DIR="${INSTALL_DIR}/java"
JAVA_CONFIG_DIR="${CONFIG_DIR}/java"
JAVA_AGENT_INSTALL_PATH="${JAVA_INSTALL_DIR}/opentelemetry-javaagent.jar"

# Node.js paths
NODEJS_INSTALL_DIR="${INSTALL_DIR}/nodejs"
NODEJS_CONFIG_DIR="${CONFIG_DIR}/nodejs"

# .NET paths
DOTNET_INSTALL_DIR="${INSTALL_DIR}/dotnet"
DOTNET_CONFIG_DIR="${CONFIG_DIR}/dotnet"

# Agent release files
INJECTOR_RELEASE_PATH="${PACKAGING_DIR}/injector-release.txt"
JAVA_AGENT_RELEASE_PATH="${PACKAGING_DIR}/java-agent-release.txt"
NODEJS_AGENT_RELEASE_PATH="${PACKAGING_DIR}/nodejs-agent-release.txt"
DOTNET_AGENT_RELEASE_PATH="${PACKAGING_DIR}/dotnet-agent-release.txt"

# Agent download URLs
INJECTOR_RELEASE_URL="https://github.com/open-telemetry/opentelemetry-injector/releases"

JAVA_AGENT_RELEASE_URL="https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases"

DOTNET_ARTIFACT_BASE_NAME="opentelemetry-dotnet-instrumentation"
DOTNET_OS_NAME="linux"
DOTNET_AGENT_RELEASE_URL="https://github.com/open-telemetry/$DOTNET_ARTIFACT_BASE_NAME/releases/download"

# Get package version from git tags
get_version() {
    local commit_tag
    commit_tag="$( git -C "$REPO_DIR" describe --abbrev=0 --tags --exact-match --match 'v[0-9]*' 2>/dev/null || true )"
    if [[ -z "$commit_tag" ]]; then
        local latest_tag
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

# Normalize version for RPM (replace dashes with underscores, strip leading "v")
normalize_rpm_version() {
    local version="$1"
    # RPM doesn't allow dashes in version; replace with underscore
    version="${version//'-'/'_'}"
    # Strip leading "v" character
    version="${version#v}"
    echo "$version"
}

# Convert architecture name for RPM
convert_arch_for_rpm() {
    local arch="$1"
    case "$arch" in
        arm64) echo "aarch64" ;;
        amd64) echo "x86_64" ;;
        *) echo "$arch" ;;
    esac
}

# Download injector shared library from GitHub releases
download_injector() {
    local tag="$1"
    local arch="$2"
    local dest="$3"
    local dl_url="${INJECTOR_RELEASE_URL}/download/${tag}/libotelinject_${arch}.so"

    echo "Downloading injector from ${dl_url} ..."
    mkdir -p "$( dirname "$dest" )"
    curl -sfL "$dl_url" -o "$dest"
}

# Download Java agent from GitHub releases
download_java_agent() {
    local tag="$1"
    local dest="$2"
    local dl_url=""
    if [[ "$tag" = "latest" ]]; then
        dl_url="$JAVA_AGENT_RELEASE_URL/latest/download/opentelemetry-javaagent.jar"
    else
        dl_url="$JAVA_AGENT_RELEASE_URL/download/$tag/opentelemetry-javaagent.jar"
    fi

    echo "Downloading Java agent from ${dl_url} ..."
    mkdir -p "$( dirname "$dest" )"
    curl -sfL "$dl_url" -o "$dest"
}

# Download and install Node.js agent from npm
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

# Download .NET agent from GitHub releases
# Uses the OTel Operator dedup approach: extract glibc fully, then overlay
# only the linux-musl-x64/ directory from the musl archive so that shared
# managed assemblies are stored once.
download_dotnet_agent() {
    local tag="$1"
    local dest="$2"

    case "${ARCH:-}" in
        amd64) local dotnet_arch="x64" ;;
        arm64) local dotnet_arch="arm64" ;;
        *)
            echo "Set the architecture type using the ARCH environment variable. Supported values: amd64, arm64." >&2
            exit 1
            ;;
    esac

    # Extract glibc archive fully (this provides the shared managed assemblies)
    download_and_extract_dotnet_archive "$tag" "$dest" "$dotnet_arch" glibc

    # Extract only the native library directory from the musl archive
    local musl_tmp
    musl_tmp="$(mktemp -d)"
    download_and_extract_dotnet_archive "$tag" "$musl_tmp" "$dotnet_arch" musl

    # Overlay only the musl-specific native library directory
    local musl_native_dir="linux-musl-${dotnet_arch}"
    if [[ -d "${musl_tmp}/${musl_native_dir}" ]]; then
        mkdir -p "${dest}/${musl_native_dir}"
        cp -rf "${musl_tmp}/${musl_native_dir}/." "${dest}/${musl_native_dir}/"
    fi
    rm -rf "$musl_tmp"
}

download_and_extract_dotnet_archive() {
    local tag="$1"
    local dest="$2"
    local dotnet_arch="$3"
    local libc_flavor="$4"
    local pkg="${DOTNET_ARTIFACT_BASE_NAME}-${DOTNET_OS_NAME}-${libc_flavor}-${dotnet_arch}.zip"
    local dl_url="${DOTNET_AGENT_RELEASE_URL}/${tag}/${pkg}"

    echo "Downloading .NET agent from ${dl_url} ..."
    curl -sSfL "$dl_url" -o "/tmp/${pkg}"

    echo "Extracting ${pkg} to ${dest} ..."
    mkdir -p "$dest"
    unzip -qo -d "$dest" "/tmp/${pkg}"
    rm -f "/tmp/${pkg}"
}

# Generate man page from template
# Usage: generate_man_page <template_file> <output_file> <version>
generate_man_page() {
    local template="$1"
    local output="$2"
    local version="$3"
    local date
    date="$(date +"%B %Y")"

    mkdir -p "$(dirname "$output")"
    sed -e "s/@VERSION@/$version/g" -e "s/@DATE@/$date/g" "$template" | gzip -9 > "$output"
}

# Setup injector buildroot for RPM package
setup_injector_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local injector_release
    injector_release="$(tail -n 1 <"$INJECTOR_RELEASE_PATH")"
    local pkg_name="opentelemetry-injector"

    # Download and install library
    mkdir -p "${buildroot}${INJECTOR_INSTALL_DIR}"
    download_injector "$injector_release" "$arch" "${buildroot}${LIBOTELINJECT_INSTALL_PATH}"
    chmod 755 "${buildroot}${LIBOTELINJECT_INSTALL_PATH}"

    # Install configuration
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}"
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/injector/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/"
    cp -f "$COMMON_DIR/injector/default_env.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/injector.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/default_env.conf"
    chmod 755 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"

    # Install man page
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/injector/opentelemetry-injector.8.tmpl" \
        "$man_dir/opentelemetry-injector.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/injector/README.md" "$doc_dir/"

    # Install license (RPM uses /usr/share/licenses/)
    local license_dir="${buildroot}${LICENSE_DIR}/${pkg_name}"
    mkdir -p "$license_dir"
    cp -f "$REPO_DIR/LICENSE" "$license_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}

# Setup Java autoinstrumentation buildroot for RPM package
setup_java_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local java_agent_release
    java_agent_release="$(tail -n 1 <"$JAVA_AGENT_RELEASE_PATH")"
    local pkg_name="opentelemetry-java-autoinstrumentation"

    # Download and install Java agent
    mkdir -p "${buildroot}${JAVA_INSTALL_DIR}"
    download_java_agent "$java_agent_release" "${buildroot}${JAVA_AGENT_INSTALL_PATH}"
    chmod 644 "${buildroot}${JAVA_AGENT_INSTALL_PATH}"

    # Install configuration
    mkdir -p "${buildroot}${JAVA_CONFIG_DIR}"
    cp -f "$COMMON_DIR/java/otel-config.yaml" "${buildroot}${JAVA_CONFIG_DIR}/"
    chmod 644 "${buildroot}${JAVA_CONFIG_DIR}/otel-config.yaml"

    # Install injector conf.d drop-in file to enable Java auto-instrumentation
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/java/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/java.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/java.conf"

    # Install man page
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/java/opentelemetry-java.8.tmpl" \
        "$man_dir/opentelemetry-java.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/java/README.md" "$doc_dir/"

    # Install license (RPM uses /usr/share/licenses/)
    local license_dir="${buildroot}${LICENSE_DIR}/${pkg_name}"
    mkdir -p "$license_dir"
    cp -f "$REPO_DIR/LICENSE" "$license_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}

# Setup Node.js autoinstrumentation buildroot for RPM package
setup_nodejs_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local nodejs_agent_release
    nodejs_agent_release="$(tail -n 1 <"$NODEJS_AGENT_RELEASE_PATH")"
    local pkg_name="opentelemetry-nodejs-autoinstrumentation"

    # Download and install Node.js agent
    mkdir -p "${buildroot}${INSTALL_DIR}"
    download_nodejs_agent "$nodejs_agent_release" "${buildroot}${INSTALL_DIR}"
    chmod -R 755 "${buildroot}${NODEJS_INSTALL_DIR}"

    # Install configuration
    mkdir -p "${buildroot}${NODEJS_CONFIG_DIR}"
    cp -f "$COMMON_DIR/nodejs/otel-config.yaml" "${buildroot}${NODEJS_CONFIG_DIR}/"
    chmod 644 "${buildroot}${NODEJS_CONFIG_DIR}/otel-config.yaml"

    # Install injector conf.d drop-in file to enable Node.js auto-instrumentation
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/nodejs/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/nodejs.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/nodejs.conf"

    # Install man page
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/nodejs/opentelemetry-nodejs.8.tmpl" \
        "$man_dir/opentelemetry-nodejs.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/nodejs/README.md" "$doc_dir/"

    # Install license (RPM uses /usr/share/licenses/)
    local license_dir="${buildroot}${LICENSE_DIR}/${pkg_name}"
    mkdir -p "$license_dir"
    cp -f "$REPO_DIR/LICENSE" "$license_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}

# Setup .NET autoinstrumentation buildroot for RPM package
setup_dotnet_buildroot() {
    local arch="$1"
    local version="$2"
    local buildroot="$3"
    local dotnet_agent_release
    dotnet_agent_release="$(tail -n 1 <"$DOTNET_AGENT_RELEASE_PATH")"
    local pkg_name="opentelemetry-dotnet-autoinstrumentation"

    # Download and install .NET agent (OTel Operator dedup approach)
    mkdir -p "${buildroot}${DOTNET_INSTALL_DIR}"
    download_dotnet_agent "$dotnet_agent_release" "${buildroot}${DOTNET_INSTALL_DIR}"
    chmod -R 755 "${buildroot}${DOTNET_INSTALL_DIR}"

    # Install configuration
    mkdir -p "${buildroot}${DOTNET_CONFIG_DIR}"
    cp -f "$COMMON_DIR/dotnet/otel-config.yaml" "${buildroot}${DOTNET_CONFIG_DIR}/"
    chmod 644 "${buildroot}${DOTNET_CONFIG_DIR}/otel-config.yaml"

    # Install injector conf.d drop-in file to enable .NET auto-instrumentation
    mkdir -p "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d"
    cp -f "$COMMON_DIR/dotnet/injector.conf" "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/dotnet.conf"
    chmod 644 "${buildroot}${INJECTOR_CONFIG_DIR}/conf.d/dotnet.conf"

    # Install man page
    local man_dir="${buildroot}${MAN_DIR}/man8"
    mkdir -p "$man_dir"
    generate_man_page "$COMMON_DIR/dotnet/opentelemetry-dotnet.8.tmpl" \
        "$man_dir/opentelemetry-dotnet.8.gz" "$version"

    # Install documentation
    local doc_dir="${buildroot}${DOC_DIR}/${pkg_name}"
    mkdir -p "$doc_dir"
    cp -f "$COMMON_DIR/dotnet/README.md" "$doc_dir/"

    # Install license (RPM uses /usr/share/licenses/)
    local license_dir="${buildroot}${LICENSE_DIR}/${pkg_name}"
    mkdir -p "$license_dir"
    cp -f "$REPO_DIR/LICENSE" "$license_dir/"

    # Set ownership
    chown -R root:root "$buildroot"
}
