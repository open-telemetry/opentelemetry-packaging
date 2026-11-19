#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

ENV_FILE=/etc/opentelemetry/jmx-scraper/jmx-scraper.env

# The environment file holds the JMX credentials and the OTLP headers, so the
# package ships it mode 0640. Neither format keeps that true by itself: dpkg
# conffiles and %config(noreplace) both leave an operator-edited file's mode
# alone on upgrade, so a file that something else relaxed stays readable by
# everyone while the man page, the README, and the file's own header all say it
# is not.
#
# The mode is only ever tightened. o-rwx,g-wx removes bits and adds none, so an
# operator who chose something stricter, 0600 for instance, keeps it.
#
# On Debian an administrator records a deliberate mode with dpkg-statoverride,
# so check for one first and leave the file alone when it exists. This is the
# pattern ca-certificates, openssh-client, and dbus use for the same problem.
# rpm has no equivalent, so there the chmod is unconditional.
tighten=yes
if command -v dpkg-statoverride >/dev/null 2>&1; then
    if dpkg-statoverride --list "$ENV_FILE" >/dev/null 2>&1; then
        tighten=no
    fi
fi

if [ -f "$ENV_FILE" ] && [ "$tighten" = yes ]; then
    chmod o-rwx,g-wx "$ENV_FILE"
fi

# Refresh systemd's unit cache after installing the JMX scraper service.
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
