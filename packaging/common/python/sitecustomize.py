# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0
#
# Derived from sitecustomize.py in the Dash0 operator
# (https://github.com/dash0hq/dash0-operator, images/instrumentation/python/),
# Copyright 2026 Dash0 Inc., Apache-2.0. See the NOTICE file at the repository root.

# Trigger the load of the OpenTelemetry distribution for Python. This is enabled by prepending a
# directory with this script to the PYTHONPATH environment variable via the OpenTelemetry injector.

# IMPORTANT: This file must be valid Python 2.7+ so that it can be parsed without crashing
# older interpreters. The version gate below prevents execution on unsupported versions.

from __future__ import print_function
import os
from os.path import dirname
import sys
from sys import path, version, version_info, stderr

double_instrumentation_check_packages = [
    "opentelemetry-distro",
    "opentelemetry-exporter-otlp",
    "opentelemetry-exporter-otlp-proto-common",
    "opentelemetry-exporter-otlp-proto-grpc",
    "opentelemetry-exporter-otlp-proto-http",
    "opentelemetry-exporter-otlp-pyproto-common",
    "opentelemetry-exporter-otlp-pyproto-http",
    "opentelemetry-exporter-prometheus",
    "opentelemetry-instrumentation",
    "opentelemetry-sdk",
    "opentelemetry-proto",
]

# Packages exempt from deactivation on version conflicts: general-purpose
# libraries with stable APIs where the application's version (which wins on
# sys.path) is expected to keep working with the bundled SDK. A mismatch logs
# a warning instead of deactivating; e.g. Debian's python3-yaml 6.0 versus the
# bundled PyYAML pin.
version_conflict_exempt_packages = [
    "pyyaml",
    "jsonschema",
]

debug_enabled = os.environ.get("OTEL_INJECTOR_LOG_LEVEL") == "debug"


def _log(level, message):
    # sys.stderr can be None (daemons, pythonw); print(file=None) would fall
    # through to stdout and corrupt the application's output stream. Diagnostics
    # must never do that, nor raise (e.g. on a closed fd 2).
    if stderr is None:
        return
    try:
        print("[opentelemetry-python-autoinstrumentation] {}: {}".format(level, message), file=stderr)
    except Exception:
        pass


def _log_warn(message):
    _log("WARN", message)


def _log_debug(message):
    if debug_enabled:
        _log("DEBUG", message)


_log_debug("running sitecustomize.py")
_log_debug("PYTHONPATH: {}".format(os.environ.get("PYTHONPATH")))


def _print_cannot_auto_instrument_message(reason):
    if hasattr(sys, "argv"):
        _log_warn("cannot auto-instrument Python process: {} [{}]".format(reason, " ".join(sys.argv)))
    else:
        _log_warn("cannot auto-instrument Python process: {}".format(reason))


def _self_deactivate(current_site):
    # Starting child processes is quite common in Python (e.g. gunicorn etc.), and in particular,
    # the OpenTelemetry instrumentation wrapper (opentelemetry-instrument python app.py) does this.
    # When self-deactivating, we must also deactivate for child processes so they do not attempt
    # to load our packages in a potentially conflicting state.

    # Remove this site from PYTHONPATH so child processes do not load it.
    # Compared normalized: the injected entry can differ textually from
    # dirname(__file__) (e.g. a trailing slash).
    normalized_site = os.path.normpath(current_site)
    current_pythonpath = os.environ.get("PYTHONPATH", "")
    pythonpath_entries = [
        entry for entry in current_pythonpath.split(":")
        if os.path.normpath(entry) != normalized_site
    ]
    new_pythonpath = ":".join(pythonpath_entries)
    _log_debug('setting PYTHONPATH in _self_deactivate: "{}"'.format(new_pythonpath))
    os.environ["PYTHONPATH"] = new_pythonpath

    # Clear the injector's Python agent path so it does not re-add our site to child processes.
    _log_debug("clearing PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX in _self_deactivate")
    os.environ["PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX"] = ""

    path[:] = [entry for entry in path if os.path.normpath(entry) != normalized_site]


def _check_for_double_instrumentation(current_site):
    import importlib.metadata
    offending_packages = []
    for dist in importlib.metadata.distributions():
        name = dist.metadata["Name"]
        if name is not None and name in double_instrumentation_check_packages:
            offending_packages.append(str(dist._path))
    if offending_packages:
        _self_deactivate(current_site)
        _print_cannot_auto_instrument_message(
            "The application has OpenTelemetry dependencies which indicate that it is already instrumented. "
            "The following problematic dependencies have been found: {}. ".format(", ".join(offending_packages)) +
            "Skipping Python auto-instrumentation to avoid double instrumentation. Remove the mentioned "
            "dependencies and make sure the opentelemetry-instrument wrapper is not used if you want to "
            "use the system-package Python auto-instrumentation.")
        return True
    return False


def _read_all_dependencies():
    """Read all flattened dependencies from all-dependencies.txt. Returns list of requirement strings or None."""
    dependencies_file = os.path.join(dirname(__file__), "all-dependencies.txt")
    requirements_to_check = []
    try:
        with open(dependencies_file, "r") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                requirements_to_check.append(line)
        return requirements_to_check
    except (IOError, OSError):
        return None


def _check_dependency_version_conflict(req_string, version_conflicts):
    """Check for a dependency version conflict. Accumulates conflicts in version_conflicts (modified in place)."""
    import importlib.metadata
    from packaging.requirements import Requirement
    from packaging.version import Version

    _log_debug("_check_dependency_version_conflict({})".format(req_string))
    # An exception escaping this module aborts no application, but it prints a
    # traceback on every process start; parse failures must never propagate.
    # Unparsable input makes the requirement unverifiable, not conflicting:
    # warn and skip it.
    try:
        req = Requirement(req_string)
    except Exception as e:
        _log_warn(
            'cannot parse requirement "{}" from all-dependencies.txt; '
            "skipping its dependency-conflict check: {}: {}".format(req_string, type(e).__name__, e))
        return

    if req.marker and not req.marker.evaluate():
        return

    if req.name == "pip":
        return

    try:
        installed_distribution = importlib.metadata.distribution(req.name)
    except importlib.metadata.PackageNotFoundError:
        _log_debug("adding version error for {}".format(req.name))
        version_conflicts[req.name] = {"error": "required package not found"}
        return

    # Distributions patched by Linux distros can carry versions that do not
    # parse as PEP 440 (and metadata may lack a version entirely).
    try:
        installed_version = Version(installed_distribution.version)
    except Exception as e:
        _log_warn(
            'cannot parse the installed version "{}" of package "{}"; '
            "skipping its dependency-conflict check: {}: {}".format(
                installed_distribution.version, req.name, type(e).__name__, e))
        return

    _log_debug("installed_version: {}".format(installed_version))
    if req.specifier and installed_version not in req.specifier:
        if req.name.lower() in version_conflict_exempt_packages:
            _log_warn(
                'the installed version {} of package "{}" differs from the bundled requirement {}; '
                "continuing anyway (the installed version takes precedence)".format(
                    installed_version, req.name, req.specifier))
            return
        _log_debug("adding version conflict for {}".format(req.name))
        version_conflicts[req.name] = {
            "version_required": str(req.specifier),
            "version_found": str(installed_version),
        }


def _validate_config_file(current_site, config_file):
    """Validate the declarative configuration file with the bundled otel-config-check.

    Returns None when the file is usable (or when validation is impossible, in
    which case a debug/warning line explains why), and a human-readable error
    message when the file would break the SDK's file configurator.
    """
    import subprocess

    validator = os.path.join(dirname(current_site), "otel-config-check")
    if not os.path.isfile(validator):
        _log_debug("otel-config-check not found at {}; skipping configuration file validation".format(validator))
        return None
    try:
        result = subprocess.run([validator, config_file], capture_output=True, text=True, timeout=10)
    except Exception as e:
        _log_warn("cannot run otel-config-check ({}: {}); skipping configuration file validation".format(
            type(e).__name__, e))
        return None
    if result.returncode != 0:
        return (result.stdout + result.stderr).strip()
    return None


def _exporter_for_protocol(otlp_protocol):
    # This package bundles pure-Python OTLP exporters for both gRPC and
    # HTTP/protobuf (the gRPC one transports over the stdlib-only _pygrpc
    # client, so neither needs a C extension). http/json is not supported: the
    # exporters emit protobuf only. An unset protocol follows the OpenTelemetry
    # default of grpc. Returns the exporter entry-point name, or None if the
    # protocol is unsupported.
    if otlp_protocol is None or otlp_protocol == "grpc":
        return "otlp_proto_grpc"
    if otlp_protocol == "http/protobuf":
        return "otlp_proto_http"
    return None


def import_distro():
    _log_debug("checking Python version")
    current_site = dirname(__file__)

    # Require Python >= 3.10 (opentelemetry-exporter-otlp-pyproto-http minimum).
    # We cannot use named attributes (e.g. sys.version_info.major) as those were introduced in 3.1.
    if version_info[0] != 3 or version_info[1] < 10:
        _self_deactivate(current_site)
        _print_cannot_auto_instrument_message("unsupported Python version: {}".format(version))
        return
    _log_debug("found eligible Python version: {}".format(version_info))

    # Exporter selected from OTEL_EXPORTER_OTLP_PROTOCOL below (env-var mode).
    # Under OTEL_CONFIG_FILE the configuration file drives exporter selection
    # and OTEL_*_EXPORTER is ignored, so this default is inert in that mode.
    default_exporter = "otlp_proto_grpc"
    config_file = os.environ.get("OTEL_CONFIG_FILE")
    if config_file:
        # With OTEL_CONFIG_FILE in effect the SDK ignores the OTEL_* exporter
        # environment variables, so the protocol guard below would check
        # values that are never used. Validate the configuration file instead
        # (readable, valid YAML, file_format "1.0", no otlp_grpc exporter).
        _log_debug("validating OTEL_CONFIG_FILE: {}".format(config_file))
        validation_error = _validate_config_file(current_site, config_file)
        if validation_error is not None:
            _self_deactivate(current_site)
            _print_cannot_auto_instrument_message(
                "the configuration file set via OTEL_CONFIG_FILE ({}) is not usable: {}".format(
                    config_file, validation_error))
            return
    else:
        _log_debug("checking OTEL_EXPORTER_OTLP_PROTOCOL")

        otlp_protocol = os.environ.get("OTEL_EXPORTER_OTLP_PROTOCOL")
        default_exporter = _exporter_for_protocol(otlp_protocol)
        if default_exporter is None:
            _self_deactivate(current_site)
            _print_cannot_auto_instrument_message(
                "OTEL_EXPORTER_OTLP_PROTOCOL={} is not supported. "
                "This package supports grpc and http/protobuf.".format(otlp_protocol)
            )
            return
        _log_debug("found eligible OTEL_EXPORTER_OTLP_PROTOCOL value: {}".format(otlp_protocol))

    _log_debug("checking for double instrumentation")

    # Temporarily remove this site so the double-instrumentation check only sees the application's
    # own packages. After the check we re-append it, which also moves it to the end of sys.path so
    # the application's package versions win over ours in case of overlap.
    # Compared normalized: the sys.path entry can differ textually from
    # dirname(__file__) (e.g. a trailing slash in the injected PYTHONPATH
    # value). Leaving the site on sys.path here would make the
    # double-instrumentation check see the bundle's own packages and falsely
    # self-deactivate; an unguarded exact remove() would raise instead.
    normalized_site = os.path.normpath(current_site)
    path[:] = [entry for entry in path if os.path.normpath(entry) != normalized_site]

    if _check_for_double_instrumentation(current_site):
        return

    _log_debug("no double instrumentation detected")
    _log_debug("checking for dependency conflicts")

    path.append(current_site)

    version_conflicts = {}
    requirements_to_check = _read_all_dependencies()
    if requirements_to_check is None:
        _self_deactivate(current_site)
        _print_cannot_auto_instrument_message("cannot read all-dependencies.txt for dependency conflict checking")
        return

    for req_string in requirements_to_check:
        _check_dependency_version_conflict(req_string, version_conflicts)
        if version_conflicts:
            break

    if not version_conflicts:
        if not config_file:
            # Select the bundled pure-Python OTLP exporter matching the protocol
            # (otlp_proto_grpc or otlp_proto_http), both registered as drop-in
            # replacements under the standard entry points. No-ops if the user
            # already set these. Skipped under OTEL_CONFIG_FILE, where the
            # configuration file drives exporter selection and these are ignored.
            os.environ.setdefault("OTEL_TRACES_EXPORTER", default_exporter)
            os.environ.setdefault("OTEL_METRICS_EXPORTER", default_exporter)
            os.environ.setdefault("OTEL_LOGS_EXPORTER", default_exporter)
        try:
            _log_debug("importing and initializing the Python auto-instrumentation now")
            from opentelemetry.instrumentation import auto_instrumentation
            auto_instrumentation.initialize()
        except Exception as e:
            _self_deactivate(current_site)
            _print_cannot_auto_instrument_message(
                "error when importing/initializing Python OpenTelemetry auto-instrumentation: {}: {}".format(
                    type(e).__name__, e))
    else:
        _self_deactivate(current_site)
        _print_cannot_auto_instrument_message("dependency conflicts: {}".format(version_conflicts))


import_distro()
