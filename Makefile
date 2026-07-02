# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

# Makefile for orchestrating OpenTelemetry Linux package builds.
#
# Packages are built using nfpm (via Go), not FPM or Docker. The only external
# tool required for package creation is Go itself. Upstream artifacts
# (libotelinject.so, Java agent, Node.js agent, .NET agent) are fetched as
# pre-built releases during the build step (npm is needed for Node.js).

SHELL = /bin/bash
.SHELLFLAGS = -o pipefail -c

# ============================================================================
# Variables
# ============================================================================

# Package version. Derived from the latest git tag if available, otherwise
# falls back to a development placeholder.
VERSION ?= $(shell v=`git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null` && echo "$${v\#v}" || echo "0.0.0-dev")

# Target CPU architecture (amd64 or arm64).
ARCH ?= amd64

# Export ARCH so the integration tests (go test) build and run their containers
# for the same architecture the packages were built for. Without this, Docker
# builds images for the host architecture (e.g. arm64 on Apple Silicon) and
# package installation fails against amd64 packages.
export ARCH

# Directory where built packages (.deb, .rpm) are placed.
OUTPUT_DIR ?= build/packages

# Container engine (auto-detects podman or docker).
CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

# Directory for local APT/YUM repos used in integration testing.
LOCAL_REPO_DIR := $(CURDIR)/build/local-repo

# Components that have packages.
COMPONENTS := injector java nodejs dotnet meta

# Languages that have integration tests (meta is excluded).
TEST_LANGUAGES := java nodejs dotnet

# ============================================================================
# Package Build Targets (nfpm, pure Go)
# ============================================================================

.PHONY: deb-package-%
deb-package-%:
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format deb -component $* -output $(OUTPUT_DIR)

.PHONY: deb-packages
deb-packages:
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format deb -output $(OUTPUT_DIR)

.PHONY: rpm-package-%
rpm-package-%:
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format rpm -component $* -output $(OUTPUT_DIR)

.PHONY: rpm-packages
rpm-packages:
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format rpm -output $(OUTPUT_DIR)

.PHONY: packages
packages:
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format all -output $(OUTPUT_DIR)

# ============================================================================
# Local Package Repositories for Testing
# ============================================================================

.PHONY: local-apt-repo
local-apt-repo: deb-packages
	@echo "Creating local APT repository in $(LOCAL_REPO_DIR)/apt"
	@mkdir -p $(LOCAL_REPO_DIR)/apt/pool
	@cp $(OUTPUT_DIR)/*.deb $(LOCAL_REPO_DIR)/apt/pool/
	@$(CONTAINER_ENGINE) run --rm --platform linux/$(ARCH) \
		-v $(LOCAL_REPO_DIR)/apt:/repo \
		-v $(CURDIR)/packaging/repo:/scripts:ro \
		debian:12 /scripts/generate-apt-repo.sh /repo
	@echo ""
	@echo "APT repository created at $(LOCAL_REPO_DIR)/apt"
	@echo ""
	@echo "To test in a container:"
	@echo "  docker run --platform linux/$(ARCH) -v $(LOCAL_REPO_DIR)/apt:/local-repo -it debian:12 bash"
	@echo ""
	@echo "Then inside the container:"
	@echo "  echo 'deb [trusted=yes] file:///local-repo stable main' > /etc/apt/sources.list.d/local.list"
	@echo "  apt-get update"
	@echo "  apt-get install opentelemetry"

.PHONY: local-rpm-repo
local-rpm-repo: rpm-packages
	@echo "Creating local RPM repository in $(LOCAL_REPO_DIR)/rpm"
	@mkdir -p $(LOCAL_REPO_DIR)/rpm/packages
	@cp $(OUTPUT_DIR)/*.rpm $(LOCAL_REPO_DIR)/rpm/packages/
	@$(CONTAINER_ENGINE) run --rm --platform linux/$(ARCH) \
		-v $(LOCAL_REPO_DIR)/rpm:/repo \
		-v $(CURDIR)/packaging/repo:/scripts:ro \
		fedora:41 /scripts/generate-rpm-repo.sh /repo
	@echo ""
	@echo "RPM repository created at $(LOCAL_REPO_DIR)/rpm"
	@echo ""
	@echo "To test in a container:"
	@echo "  docker run --platform linux/$(ARCH) -v $(LOCAL_REPO_DIR)/rpm:/local-repo -it fedora:41 bash"
	@echo ""
	@echo "Then inside the container:"
	@echo "  echo -e '[local]\nname=Local\nbaseurl=file:///local-repo/packages\nenabled=1\ngpgcheck=0' > /etc/yum.repos.d/local.repo"
	@echo "  dnf install opentelemetry"

.PHONY: local-repos
local-repos: local-apt-repo local-rpm-repo
	@echo "All local repositories created in $(LOCAL_REPO_DIR)"

# ============================================================================
# Integration Tests (testcontainers)
# ============================================================================

# Ryuk (the testcontainers reaper) does not work with Podman. Tests use
# t.Cleanup for container teardown, so disabling it is safe.
export TESTCONTAINERS_RYUK_DISABLED = true

.PHONY: integration-test-metadata
integration-test-metadata: packages
	go test -v -timeout 5m ./packaging/tests/metadata/

.PHONY: integration-tests
integration-tests: local-repos
	go test -v -timeout 30m ./packaging/tests/...

.PHONY: integration-test-deb-java
integration-test-deb-java: local-apt-repo
	go test -v -timeout 30m ./packaging/tests/deb/java/

.PHONY: integration-test-deb-nodejs
integration-test-deb-nodejs: local-apt-repo
	go test -v -timeout 30m ./packaging/tests/deb/nodejs/

.PHONY: integration-test-deb-dotnet
integration-test-deb-dotnet: local-apt-repo
	go test -v -timeout 30m ./packaging/tests/deb/dotnet/

.PHONY: integration-test-rpm-java
integration-test-rpm-java: local-rpm-repo
	go test -v -timeout 30m ./packaging/tests/rpm/java/

.PHONY: integration-test-rpm-nodejs
integration-test-rpm-nodejs: local-rpm-repo
	go test -v -timeout 30m ./packaging/tests/rpm/nodejs/

.PHONY: integration-test-rpm-dotnet
integration-test-rpm-dotnet: local-rpm-repo
	go test -v -timeout 30m ./packaging/tests/rpm/dotnet/

# ============================================================================
# Lint
# ============================================================================

.PHONY: check-shellcheck-installed
check-shellcheck-installed:
	@if ! shellcheck --version > /dev/null 2>&1; then \
		echo "error: shellcheck is not installed. See https://github.com/koalaman/shellcheck?tab=readme-ov-file#installing for installation instructions."; \
		exit 1; \
	fi

.PHONY: lint
lint: check-shellcheck-installed
	@echo "Linting shell scripts with shellcheck"
	find . -name '*.sh' -not -path './.git/*' | xargs shellcheck -x
	@echo "Vetting Go code"
	go vet ./...

# ============================================================================
# Clean
# ============================================================================

.PHONY: clean
clean:
	rm -rf build

.PHONY: clean-local-repos
clean-local-repos:
	rm -rf $(LOCAL_REPO_DIR)

# ============================================================================
# Utility
# ============================================================================

.PHONY: list
list:
	@grep '^[^#[:space:]].*:' Makefile
