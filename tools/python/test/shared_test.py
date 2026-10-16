# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Code shared by the supported Python version test modules.

Holds the paths of the three scripts under test, the path of the file they
read, the two writers that build the bundled distributions and a versions
file on disk, and the runner that invokes a script's work phase.

The name deliberately does not match the test_*.py pattern that "unittest
discover" uses, so this module is imported by the test modules rather than
collected as one. It also cannot be called shared.py: PYTHONPATH puts
tools/python/tools on sys.path alongside this directory, so a second
module of that name would shadow the one the scripts share.
"""

from collections.abc import Iterable
from json import dumps
from pathlib import Path
from subprocess import CompletedProcess, run
from sys import executable

_TOOLS_DIRECTORY: Path = Path(__file__).resolve().parents[1] / "tools"

CHECK_SCRIPT_PATH: str = str(
    _TOOLS_DIRECTORY / "check_supported_python_versions.py")

PRINT_SCRIPT_PATH: str = str(
    _TOOLS_DIRECTORY / "print_minimum_supported_python_version.py")

PRUNE_SCRIPT_PATH: str = str(
    _TOOLS_DIRECTORY / "prune_supported_python_versions.py")

SHIPPED_SUPPORTED_PYTHON_VERSIONS_FILE: Path = (
    Path(__file__).resolve().parents[3] / "packaging" / "builder"
    / "supported_python_versions.json")


def write_dist_info_with_requires_python(
    bundled_distributions_directory: Path,
    name: str,
    version: str,
    requires_python: str | None,
) -> None:
    """Create a dist-info/METADATA in bundled_distributions_directory.

    This is the minimum importlib.metadata needs to enumerate a distribution
    and report its Requires-Python, so bundled distributions can be
    assembled offline without installing anything. Passing None for
    requires_python omits the field, which is how a distribution that
    constrains nothing looks.
    """
    dist_info_directory = bundled_distributions_directory / (
        "{}-{}.dist-info".format(name, version))
    dist_info_directory.mkdir(parents=True)
    metadata: str = (
        "Metadata-Version: 2.1\n"
        "Name: {}\n"
        "Version: {}\n".format(name, version))
    if requires_python is not None:
        metadata += "Requires-Python: {}\n".format(requires_python)
    (dist_info_directory / "METADATA").write_text(
        metadata, encoding="utf-8")


def write_supported_python_versions_file(
    supported_python_versions_path: Path, versions: Iterable[str]
) -> None:
    """Write a versions file listing the given "major.minor" strings."""
    supported_python_versions_path.write_text(
        dumps(list(versions)) + "\n", encoding="utf-8")


def run_bundled_distributions_script(
    script_path: str,
    bundled_distributions_directory: Path,
    vendor_directory: Path,
    supported_python_versions_path: Path,
    lowest_resolvable_python_version: str | None = None,
) -> CompletedProcess[str]:
    """Run a script's work phase and return the completed run.

    Passing --bundled-distributions-dir selects the work phase directly, so
    no virtualenv is built and no distribution is downloaded. Passing
    lowest_resolvable_python_version stands in for what the preparation phase
    would have found, so the versions pip could not resolve can be exercised
    without resolving anything.
    """
    command: list[str] = [
        executable, script_path,
        "--bundled-distributions-dir", str(bundled_distributions_directory),
        "--vendor-dir", str(vendor_directory),
        "--supported-python-versions-file",
        str(supported_python_versions_path),
    ]
    if lowest_resolvable_python_version is not None:
        command += [
            "--lowest-resolvable-python-version",
            lowest_resolvable_python_version,
        ]
    return run(command, capture_output=True, text=True)
