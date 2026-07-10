#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Post-installation script for opentelemetry-injector package.
# Adds the injector library to /etc/ld.so.preload.
#
# This script uses only POSIX shell builtins and constructs (no grep,
# sed, or other external commands) to avoid unnecessary package
# dependencies on both DEB and RPM systems.

# Package manager scriptlets inherit the invoking shell's umask. On hardened
# hosts (umask 077) that would create /etc/ld.so.preload mode 0600, and the
# dynamic linker silently ignores a preload file that is not world-readable
# for non-root processes.
umask 022

PRELOAD_PATH="/etc/ld.so.preload"
LIBOTELINJECT_PATH="/usr/lib/opentelemetry/injector/libotelinject.so"

# Check if the entry already exists. ld.so.preload allows several entries on
# one line, so match the path as a whitespace-separated field, not only as a
# whole line. (Colon-separated entries are not recognized; a duplicate append
# in that exotic case is harmless — the linker tolerates repeated entries.)
# Track whether the last line is newline-terminated: appending to an
# unterminated line would fuse our path onto the foreign entry, breaking both.
found=false
needs_newline=false
if [ -f "$PRELOAD_PATH" ]; then
    while IFS= read -r line || { [ -n "$line" ] && needs_newline=true; }; do
        set -f
        # Unquoted expansion is deliberate: field-split the line on whitespace.
        # shellcheck disable=SC2086
        set -- $line
        set +f
        for entry; do
            if [ "$entry" = "$LIBOTELINJECT_PATH" ]; then
                found=true
            fi
        done
    done < "$PRELOAD_PATH"
fi

if [ "$found" = true ]; then
    echo "OpenTelemetry Injector is already configured in $PRELOAD_PATH"
else
    echo "Adding $LIBOTELINJECT_PATH to $PRELOAD_PATH"
    # Check the writes: on an unwritable target (read-only /etc, chattr +i)
    # the script must not claim success while the injector is inactive.
    write_ok=true
    if [ "$needs_newline" = true ]; then
        echo "" >> "$PRELOAD_PATH" || write_ok=false
    fi
    if [ "$write_ok" = true ]; then
        echo "$LIBOTELINJECT_PATH" >> "$PRELOAD_PATH" || write_ok=false
    fi
    if [ "$write_ok" = true ]; then
        echo "OpenTelemetry Injector installed successfully."
        echo "All new processes will now be instrumented automatically."
    else
        echo "WARNING: could not write to $PRELOAD_PATH; the injector is NOT active." >&2
        echo "WARNING: add $LIBOTELINJECT_PATH to $PRELOAD_PATH manually to activate it." >&2
    fi
fi
