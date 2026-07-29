# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# COPR SCM build entry point.
#
# COPR's "Make srpm" source method clones this repository and runs
#   make -f .copr/Makefile srpm outdir=<dir>
# expecting exactly one source RPM in <dir>.
# See https://docs.pagure.org/copr.copr/user_documentation.html#scm
#
# This file stays thin on purpose. Everything about the build lives in the root
# Makefile and in packaging/builder, so all that belongs here is installing the
# tooling this step needs and handing over. The step COPR runs it in has network
# access, which is what lets it install packages and vendor the Go modules; the
# per-chroot build that follows needs neither.
#
# The version comes from the root Makefile, which derives it from the newest
# version tag. A clone without tags therefore yields the development
# placeholder, and the source RPM is versioned accordingly.

outdir ?= $(CURDIR)

.PHONY: srpm
srpm:
	dnf -y install --setopt=install_weak_deps=False golang git make rpm-build tar gzip
	$(MAKE) -f Makefile srpm
	mkdir -p "$(outdir)"
	cp build/srpm/SRPMS/*.src.rpm "$(outdir)/"
