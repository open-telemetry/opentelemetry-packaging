#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Post-installation script for opentelemetry-injector package.
# Adds the injector library to /etc/ld.so.preload.
#
# This script uses only POSIX shell builtins and constructs (no grep,
# sed, or other external commands) to avoid unnecessary package
# dependencies on both DEB and RPM systems.

PRELOAD_PATH="/etc/ld.so.preload"
LIBOTELINJECT_PATH="/usr/lib/opentelemetry/injector/libotelinject.so"

# Check if the entry already exists
found=false
if [ -f "$PRELOAD_PATH" ]; then
    while IFS= read -r line || [ -n "$line" ]; do
        case "$line" in
            "$LIBOTELINJECT_PATH")
                found=true
                ;;
        esac
    done < "$PRELOAD_PATH"
fi

if [ "$found" = true ]; then
    echo "OpenTelemetry Injector is already configured in $PRELOAD_PATH"
else
    echo "Adding $LIBOTELINJECT_PATH to $PRELOAD_PATH"
    echo "$LIBOTELINJECT_PATH" >> "$PRELOAD_PATH"
    echo "OpenTelemetry Injector installed successfully."
    echo "All new processes will now be instrumented automatically."
fi
