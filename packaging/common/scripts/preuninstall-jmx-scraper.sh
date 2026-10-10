#!/bin/sh

# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Package upgrades must keep the running service. Debian passes "remove" for
# removal; RPM passes "0" when no package instance remains.
case "${1:-}" in
    remove|0)
        if [ -d /run/systemd/system ] && systemctl is-active --quiet opentelemetry-jmx-scraper.service; then
            systemctl disable --now opentelemetry-jmx-scraper.service
        fi
        ;;
esac
