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

# Synthetic higher version for the upgrade lifecycle tests. Appending ".1"
# sorts higher than VERSION under both dpkg and rpm version comparison, for
# the dev placeholder and for real tag versions alike.
NEXT_VERSION ?= $(VERSION).1

# Export NEXT_VERSION so the lifecycle tests can assert the upgraded version.
export NEXT_VERSION

# Version of the mock acme-java-autoinstrumentation vendor package. It has its
# own versioning scheme and is never compared against upstream versions.
VENDOR_VERSION ?= 1.0.0

# Marker line the next-version injector build ships in default_env.conf. The
# lifecycle tests assert on it; keep in sync with packaging/tests/lifecycle.
NEXT_CONFIG_MARKER := OTEL_TEST_NEXT_CONFIG_MARKER=1

# Components that have packages.
COMPONENTS := injector java nodejs dotnet python meta

# Languages that have integration tests (meta is excluded).
TEST_LANGUAGES := java nodejs dotnet python

# ============================================================================
# Package Build Targets (nfpm, pure Go)
# ============================================================================

# Cross-compiles the otel-config-check validator that ships inside the Python
# package. Pure Go with CGO disabled, so any build host produces the
# linux/$(ARCH) binary; go build is incremental, so running this before every
# package build is cheap.
.PHONY: otel-config-check
otel-config-check:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -o build/bin/otel-config-check-$(ARCH) ./cmd/otel-config-check

.PHONY: deb-package-%
deb-package-%: otel-config-check
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format deb -component $* -output $(OUTPUT_DIR)

.PHONY: deb-packages
deb-packages: otel-config-check
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format deb -output $(OUTPUT_DIR)

.PHONY: rpm-package-%
rpm-package-%: otel-config-check
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format rpm -component $* -output $(OUTPUT_DIR)

.PHONY: rpm-packages
rpm-packages: otel-config-check
	go run ./cmd/build-packages -version $(VERSION) -arch $(ARCH) -format rpm -output $(OUTPUT_DIR)

.PHONY: packages
packages: otel-config-check
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

# Stage a modified copy of packaging/ so the next-version injector ships a
# changed default_env.conf. Both dpkg and rpm skip conffile-conflict handling
# entirely when old and new pristine contents are identical, so the upgrade
# lifecycle tests need the next build to actually change the config file.
.PHONY: next-packaging-dir
next-packaging-dir:
	@rm -rf build/packaging-next
	@cp -R packaging build/packaging-next
	@printf '\n# Added by the next-version test build\n$(NEXT_CONFIG_MARKER)\n' \
		>> build/packaging-next/common/injector/default_env.conf

.PHONY: local-apt-repo-next
local-apt-repo-next: next-packaging-dir
	@echo "Creating next-version APT repository in $(LOCAL_REPO_DIR)/apt-next"
	go run ./cmd/build-packages -version $(NEXT_VERSION) -arch $(ARCH) -format deb \
		-component injector -packaging-dir build/packaging-next -output build/packages-next/deb
	@mkdir -p $(LOCAL_REPO_DIR)/apt-next/pool
	@cp build/packages-next/deb/*.deb $(LOCAL_REPO_DIR)/apt-next/pool/
	@$(CONTAINER_ENGINE) run --rm --platform linux/$(ARCH) \
		-v $(LOCAL_REPO_DIR)/apt-next:/repo \
		-v $(CURDIR)/packaging/repo:/scripts:ro \
		debian:12 /scripts/generate-apt-repo.sh /repo

.PHONY: local-rpm-repo-next
local-rpm-repo-next: next-packaging-dir
	@echo "Creating next-version RPM repository in $(LOCAL_REPO_DIR)/rpm-next"
	go run ./cmd/build-packages -version $(NEXT_VERSION) -arch $(ARCH) -format rpm \
		-component injector -packaging-dir build/packaging-next -output build/packages-next/rpm
	@mkdir -p $(LOCAL_REPO_DIR)/rpm-next/packages
	@cp build/packages-next/rpm/*.rpm $(LOCAL_REPO_DIR)/rpm-next/packages/
	@$(CONTAINER_ENGINE) run --rm --platform linux/$(ARCH) \
		-v $(LOCAL_REPO_DIR)/rpm-next:/repo \
		-v $(CURDIR)/packaging/repo:/scripts:ro \
		fedora:41 /scripts/generate-rpm-repo.sh /repo

.PHONY: local-apt-vendor-repo
local-apt-vendor-repo:
	@echo "Creating vendor APT repository in $(LOCAL_REPO_DIR)/apt-vendor"
	@mkdir -p build/packages-vendor
	go run ./packaging/tests/vendor/mkvendor -version $(VENDOR_VERSION) -arch $(ARCH) \
		-format deb -output build/packages-vendor
	@mkdir -p $(LOCAL_REPO_DIR)/apt-vendor/pool
	@cp build/packages-vendor/*.deb $(LOCAL_REPO_DIR)/apt-vendor/pool/
	@$(CONTAINER_ENGINE) run --rm --platform linux/$(ARCH) \
		-v $(LOCAL_REPO_DIR)/apt-vendor:/repo \
		-v $(CURDIR)/packaging/repo:/scripts:ro \
		debian:12 /scripts/generate-apt-repo.sh /repo

.PHONY: local-rpm-vendor-repo
local-rpm-vendor-repo:
	@echo "Creating vendor RPM repository in $(LOCAL_REPO_DIR)/rpm-vendor"
	@mkdir -p build/packages-vendor
	go run ./packaging/tests/vendor/mkvendor -version $(VENDOR_VERSION) -arch $(ARCH) \
		-format rpm -output build/packages-vendor
	@mkdir -p $(LOCAL_REPO_DIR)/rpm-vendor/packages
	@cp build/packages-vendor/*.rpm $(LOCAL_REPO_DIR)/rpm-vendor/packages/
	@$(CONTAINER_ENGINE) run --rm --platform linux/$(ARCH) \
		-v $(LOCAL_REPO_DIR)/rpm-vendor:/repo \
		-v $(CURDIR)/packaging/repo:/scripts:ro \
		fedora:41 /scripts/generate-rpm-repo.sh /repo

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
integration-tests: local-repos local-apt-repo-next local-rpm-repo-next \
	local-apt-vendor-repo local-rpm-vendor-repo
	go test -v -timeout 30m ./packaging/tests/...

.PHONY: integration-test-deb-java
integration-test-deb-java: local-apt-repo
	go test -v -timeout 30m -run '/deb' ./packaging/tests/java/

.PHONY: integration-test-deb-nodejs
integration-test-deb-nodejs: local-apt-repo
	go test -v -timeout 30m -run '/deb' ./packaging/tests/nodejs/

.PHONY: integration-test-deb-dotnet
integration-test-deb-dotnet: local-apt-repo
	go test -v -timeout 30m -run '/deb' ./packaging/tests/dotnet/

.PHONY: integration-test-deb-python
integration-test-deb-python: local-apt-repo
	go test -v -timeout 30m -run '/deb' ./packaging/tests/python/

# Pure-Python gRPC transport (_pygrpc) against otelsink (host-side, no
# containers). Runs against the vendored pyproto-grpc package by default;
# PYGRPC_SRC_DIR overrides the source tree (e.g. a fork checkout).
.PHONY: pyproto-grpc-integration-tests
pyproto-grpc-integration-tests:
	go test -v -timeout 5m ./packaging/tests/pyprotogrpc/

.PHONY: integration-test-rpm-java
integration-test-rpm-java: local-rpm-repo
	go test -v -timeout 30m -run '/rpm' ./packaging/tests/java/

.PHONY: integration-test-rpm-nodejs
integration-test-rpm-nodejs: local-rpm-repo
	go test -v -timeout 30m -run '/rpm' ./packaging/tests/nodejs/

.PHONY: integration-test-rpm-dotnet
integration-test-rpm-dotnet: local-rpm-repo
	go test -v -timeout 30m -run '/rpm' ./packaging/tests/dotnet/

.PHONY: integration-test-rpm-python
integration-test-rpm-python: local-rpm-repo
	go test -v -timeout 30m -run '/rpm' ./packaging/tests/python/

# Runs sitecustomize.py under every Python interpreter generation the injector
# may hit. Needs a container engine but no built packages or local repos.
.PHONY: integration-test-sitecustomize
integration-test-sitecustomize:
	go test -v -timeout 30m -run 'TestSitecustomizePythonVersionCompatibility' ./packaging/tests/python/

.PHONY: integration-test-deb-lifecycle
integration-test-deb-lifecycle: local-apt-repo local-apt-repo-next
	go test -v -timeout 30m -run 'TestLifecycle/deb' ./packaging/tests/lifecycle/

.PHONY: integration-test-rpm-lifecycle
integration-test-rpm-lifecycle: local-rpm-repo local-rpm-repo-next
	go test -v -timeout 30m -run 'TestLifecycle/rpm' ./packaging/tests/lifecycle/

.PHONY: integration-test-deb-vendor
integration-test-deb-vendor: local-apt-repo local-apt-vendor-repo
	go test -v -timeout 30m -run 'TestVendorReplacement/deb' ./packaging/tests/vendor/

.PHONY: integration-test-rpm-vendor
integration-test-rpm-vendor: local-rpm-repo local-rpm-vendor-repo
	go test -v -timeout 30m -run 'TestVendorReplacement/rpm' ./packaging/tests/vendor/

# ============================================================================
# Unit Tests
# ============================================================================

# Unit tests for the Go commands (e.g. the otel-config-check validator shipped
# in the Python package).
.PHONY: go-unit-tests
go-unit-tests:
	go test -v ./cmd/...

# Unit tests for sitecustomize.py. They need the `packaging` module (a runtime
# dependency of sitecustomize.py itself); a throwaway virtualenv keeps the
# host Python untouched.
.PHONY: python-unit-tests
python-unit-tests:
	python3 -m venv build/python-unit-tests-venv
	build/python-unit-tests-venv/bin/pip install --quiet packaging
	build/python-unit-tests-venv/bin/python -m unittest discover \
		--start-directory packaging/common/python --pattern 'test_*.py' --verbose

# Upstream test suites of the vendored pyproto exporter chain
# (packaging/common/python/vendor/), in two throwaway venvs. The vendored
# packages install in editable mode in both, so their modules resolve into the
# vendor source tree (the suites' conftest.py guards check for "pyproto" in
# resolved file paths — which is also why the venv directory names must not
# contain that substring).
#
# - The drop-in venv has no real protobuf-based packages: the pyproto shims own
#   the public opentelemetry.exporter.otlp.proto.* module paths, exactly like
#   in the shipped bundle, and the exporter test suites import through them.
# - The equivalence venv adds the real opentelemetry-proto and proto exporters,
#   which then own the shared public module paths; the equivalence suites
#   compare the pure-Python encoding (private _proto paths) against the real
#   google-protobuf one (public paths).
#
# Test suites are run per package: the tests/ directories share the module
# name "tests" and would collide in a single pytest invocation.
PYPROTO_VENDOR_DIR = packaging/common/python/vendor
PYPROTO_DROPIN_VENV = build/python-vendor-dropin-venv
PYPROTO_EQUIV_VENV = build/python-vendor-equivalence-venv
.PHONY: pyproto-unit-tests
pyproto-unit-tests:
	python3 -m venv $(PYPROTO_DROPIN_VENV)
	$(PYPROTO_DROPIN_VENV)/bin/pip install --quiet pytest \
		opentelemetry-sdk==1.43.0 hpack
	$(PYPROTO_DROPIN_VENV)/bin/pip install --quiet --no-deps \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-pyproto \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-exporter-otlp-pyproto-common \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-exporter-otlp-pyproto-http \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-exporter-otlp-pyproto-grpc
	set -e; for suite in \
		opentelemetry-exporter-otlp-pyproto-http/tests \
		opentelemetry-exporter-otlp-pyproto-grpc/tests \
		opentelemetry-exporter-otlp-pyproto-grpc/tests_pygrpc; do \
		echo "=== $$suite (drop-in venv) ==="; \
		$(abspath $(PYPROTO_DROPIN_VENV))/bin/python -m pytest -q \
			--rootdir $(PYPROTO_VENDOR_DIR)/$${suite%%/*} \
			$(PYPROTO_VENDOR_DIR)/$$suite; \
	done
	python3 -m venv $(PYPROTO_EQUIV_VENV)
	$(PYPROTO_EQUIV_VENV)/bin/pip install --quiet pytest pytest-benchmark \
		opentelemetry-sdk==1.43.0 \
		opentelemetry-proto==1.43.0 \
		opentelemetry-exporter-otlp-proto-http==1.43.0 \
		opentelemetry-exporter-otlp-proto-grpc==1.43.0
	$(PYPROTO_EQUIV_VENV)/bin/pip install --quiet --no-deps \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-pyproto \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-exporter-otlp-pyproto-common \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-exporter-otlp-pyproto-http \
		--editable $(PYPROTO_VENDOR_DIR)/opentelemetry-exporter-otlp-pyproto-grpc
	set -e; for suite in \
		opentelemetry-pyproto/tests \
		opentelemetry-exporter-otlp-pyproto-common/tests \
		opentelemetry-exporter-otlp-pyproto-http/equivalence_tests \
		opentelemetry-exporter-otlp-pyproto-grpc/equivalence_tests; do \
		echo "=== $$suite (equivalence venv) ==="; \
		$(abspath $(PYPROTO_EQUIV_VENV))/bin/python -m pytest --benchmark-disable -q \
			--rootdir $(PYPROTO_VENDOR_DIR)/$${suite%%/*} \
			$(PYPROTO_VENDOR_DIR)/$$suite; \
	done

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
