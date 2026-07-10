#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Pre-uninstall script for opentelemetry-injector package.
# Removes the injector library from /etc/ld.so.preload.
#
# This script uses only POSIX shell builtins and constructs (no grep,
# sed, or other external commands) to avoid unnecessary package
# dependencies on both DEB and RPM systems. printf is used instead of
# echo for file content: dash's echo interprets backslash escapes and
# could corrupt foreign preload entries.

# On upgrade, keep the preload entry: dpkg invokes prerm with "upgrade", rpm
# invokes %preun with the count of remaining package instances ("1"). On RPM
# the old version's %preun runs AFTER the new version's %post, so cleaning up
# here would strip the entry the new version just configured. Only a real
# removal (dpkg "remove", rpm "0") cleans up.
# Captured before the field-splitting `set --` below can clobber it.
action="${1:-}"

case "$action" in
    upgrade|1)
        exit 0
        ;;
esac

# Package manager scriptlets inherit the invoking shell's umask. The rewrite
# below replaces /etc/ld.so.preload with the tmpfile (mv preserves the
# TMPFILE's mode); under umask 077 that would leave the file 0600, and the
# dynamic linker silently ignores a preload file that is not world-readable
# for non-root processes — killing the remaining foreign entries.
umask 022

PRELOAD_PATH="/etc/ld.so.preload"
LIBOTELINJECT_PATH="/usr/lib/opentelemetry/injector/libotelinject.so"

if [ ! -f "$PRELOAD_PATH" ]; then
    echo "No $PRELOAD_PATH file found; nothing to do."
    exit 0
fi

# Build a temporary file with all lines except the injector entry. The trap
# removes the tmpfile if the script is interrupted (rpm may SIGTERM slow
# scriptlets); after a successful mv the rm is a no-op.
tmpfile="${PRELOAD_PATH}.tmp.$$"
# The EXIT trap cleans up; the signal traps must exit explicitly — a POSIX
# signal trap otherwise RESUMES the interrupted loop, which would rebuild the
# tmpfile from the current line onward and mv a truncated file over the
# original, destroying foreign entries. Exiting from a signal trap runs the
# EXIT trap, which removes the tmpfile.
trap 'rm -f "$tmpfile"' EXIT
trap 'exit 1' INT TERM HUP

has_other_entries=false
write_failed=false

while IFS= read -r line || [ -n "$line" ]; do
    # ld.so.preload allows several whitespace-separated entries per line.
    # Field-split the line and drop our path; foreign lines that do not
    # contain it are passed through verbatim to preserve their exact bytes.
    # (Colon-separated entries are not recognized; such a line would be kept
    # as-is, leaving a dangling injector entry the linker warns about.)
    line_had_path=false
    remainder=""
    set -f
    # Unquoted expansion is deliberate: field-split the line on whitespace.
    # shellcheck disable=SC2086
    set -- $line
    set +f
    for entry; do
        if [ "$entry" = "$LIBOTELINJECT_PATH" ]; then
            line_had_path=true
        else
            remainder="$remainder $entry"
        fi
    done
    remainder="${remainder# }"

    if [ "$line_had_path" = true ]; then
        out="$remainder"
    else
        out="$line"
    fi
    # Skip lines that end up empty (our entry was the only content).
    if [ "$line_had_path" = true ] && [ -z "$out" ]; then
        continue
    fi

    if ! printf '%s\n' "$out" >> "$tmpfile"; then
        # Disk full or I/O error: a truncated rewrite must never replace the
        # original file, or the remaining foreign entries would be destroyed.
        write_failed=true
        break
    fi
    # Track whether any non-empty, non-whitespace line remains.
    case "$out" in
        *[!\ \	]*)
            has_other_entries=true
            ;;
    esac
done < "$PRELOAD_PATH"

if [ "$write_failed" = true ]; then
    rm -f "$tmpfile"
    echo "WARNING: could not rewrite $PRELOAD_PATH (write failed); leaving it unchanged." >&2
    echo "WARNING: remove the $LIBOTELINJECT_PATH entry manually to silence linker warnings." >&2
    exit 0
fi

if [ "$has_other_entries" = true ]; then
    mv "$tmpfile" "$PRELOAD_PATH"
else
    rm -f "$tmpfile" "$PRELOAD_PATH"
    echo "Removed empty $PRELOAD_PATH"
fi

echo "OpenTelemetry Injector removed from ld.so.preload."
