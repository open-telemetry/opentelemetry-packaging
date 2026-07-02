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

debug_enabled = os.environ.get("OTEL_INJECTOR_LOG_LEVEL") == "debug"


def _log(level, message):
    print("[opentelemetry-python-autoinstrumentation] {}: {}".format(level, message), file=stderr)


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
    current_pythonpath = os.environ.get("PYTHONPATH", "")
    pythonpath_entries = [entry for entry in current_pythonpath.split(":") if entry != current_site]
    new_pythonpath = ":".join(pythonpath_entries)
    _log_debug('setting PYTHONPATH in _self_deactivate: "{}"'.format(new_pythonpath))
    os.environ["PYTHONPATH"] = new_pythonpath

    # Clear the injector's Python agent path so it does not re-add our site to child processes.
    _log_debug("clearing PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX in _self_deactivate")
    os.environ["PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX"] = ""

    if current_site in path:
        path.remove(current_site)


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
    req = Requirement(req_string)

    if req.marker and not req.marker.evaluate():
        return

    if req.name == "pip":
        return

    try:
        installed_distribution = importlib.metadata.distribution(req.name)
        installed_version = Version(installed_distribution.version)
        _log_debug("installed_version: {}".format(installed_version))
        if req.specifier and installed_version not in req.specifier:
            _log_debug("adding version conflict for {}".format(req.name))
            version_conflicts[req.name] = {
                "version_required": str(req.specifier),
                "version_found": str(installed_version),
            }
    except importlib.metadata.PackageNotFoundError:
        _log_debug("adding version error for {}".format(req.name))
        version_conflicts[req.name] = {"error": "required package not found"}


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
    _log_debug("checking OTEL_EXPORTER_OTLP_PROTOCOL")

    otlp_protocol = os.environ.get("OTEL_EXPORTER_OTLP_PROTOCOL")
    if otlp_protocol is None:
        # Without an explicit protocol, opentelemetry-distro defaults to grpc, which is not
        # included in this package. Self-deactivate to avoid a RuntimeError at startup.
        _self_deactivate(current_site)
        _print_cannot_auto_instrument_message(
            "OTEL_EXPORTER_OTLP_PROTOCOL is not set. "
            "(If OTEL_EXPORTER_OTLP_ENDPOINT is set on the container, the injector cannot set its own "
            "OTEL_EXPORTER_OTLP_PROTOCOL. Remove OTEL_EXPORTER_OTLP_ENDPOINT from the container if "
            "you want Python auto-instrumentation.)"
        )
        return
    if otlp_protocol == "grpc":
        _self_deactivate(current_site)
        _print_cannot_auto_instrument_message(
            "OTEL_EXPORTER_OTLP_PROTOCOL=grpc is not supported. "
            "This package only includes the HTTP exporter. Use http/protobuf or http/json."
        )
        return
    _log_debug("found eligible OTEL_EXPORTER_OTLP_PROTOCOL value: {}".format(otlp_protocol))
    _log_debug("checking for double instrumentation")

    # Temporarily remove this site so the double-instrumentation check only sees the application's
    # own packages. After the check we re-append it, which also moves it to the end of sys.path so
    # the application's package versions win over ours in case of overlap.
    path.remove(current_site)

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
        # Default to the pure-Python OTLP HTTP exporter bundled in this package.
        # These are no-ops if the user has already set the variables explicitly.
        os.environ.setdefault("OTEL_TRACES_EXPORTER", "otlp_pyproto_http")
        os.environ.setdefault("OTEL_METRICS_EXPORTER", "otlp_pyproto_http")
        os.environ.setdefault("OTEL_LOGS_EXPORTER", "otlp_pyproto_http")
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
