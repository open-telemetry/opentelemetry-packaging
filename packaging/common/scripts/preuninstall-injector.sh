#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Pre-uninstall script for opentelemetry-injector package.
# Removes the injector library from /etc/ld.so.preload.
#
# This script uses only POSIX shell builtins and constructs (no grep,
# sed, or other external commands) to avoid unnecessary package
# dependencies on both DEB and RPM systems.

# On upgrade, keep the preload entry: dpkg invokes prerm with "upgrade", rpm
# invokes %preun with the count of remaining package instances ("1"). On RPM
# the old version's %preun runs AFTER the new version's %post, so cleaning up
# here would strip the entry the new version just configured. Only a real
# removal (dpkg "remove", rpm "0") cleans up.
case "${1:-}" in
    upgrade|1)
        exit 0
        ;;
esac

PRELOAD_PATH="/etc/ld.so.preload"
LIBOTELINJECT_PATH="/usr/lib/opentelemetry/injector/libotelinject.so"

if [ ! -f "$PRELOAD_PATH" ]; then
    echo "No $PRELOAD_PATH file found; nothing to do."
    exit 0
fi

# Build a temporary file with all lines except the injector entry
tmpfile="${PRELOAD_PATH}.tmp.$$"
has_other_entries=false

while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
        "$LIBOTELINJECT_PATH")
            # Skip the injector entry
            ;;
        *)
            echo "$line" >> "$tmpfile"
            # Track whether any non-empty, non-whitespace line remains.
            # Use a case pattern to detect lines that contain at least one
            # non-space/tab character, without calling external commands.
            case "$line" in
                *[!\ \	]*)
                    has_other_entries=true
                    ;;
            esac
            ;;
    esac
done < "$PRELOAD_PATH"

if [ "$has_other_entries" = true ]; then
    mv "$tmpfile" "$PRELOAD_PATH"
else
    rm -f "$tmpfile" "$PRELOAD_PATH"
    echo "Removed empty $PRELOAD_PATH"
fi

echo "OpenTelemetry Injector removed from ld.so.preload."
