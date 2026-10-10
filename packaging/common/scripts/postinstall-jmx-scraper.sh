#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Refresh systemd's unit cache after installing the JMX scraper service.
if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
fi
