# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Definitions and shared code for the supported Python version scripts.

This docstring is the single definition of the three terms below. Everywhere
else in the repository that needs them refers here instead of restating them,
so that there is exactly one place to correct if any of them changes.

Supported Python versions
    The Python versions the package is built for, listed in
    packaging/builder/supported_python_versions.json as a JSON array of
    "major.minor" strings. Read that file for the current list. It is not
    repeated here, nor in any test, so that editing it cannot leave a second
    copy naming interpreters the package no longer ships.

    A person maintains that list by hand. packaging/builder/download.go
    embeds it, the builder resolves the bundled distributions once per
    supported Python version, and it writes the same list into the
    sitecustomize.py version gate, so the file is the single
    hand-maintained statement of which interpreters the package supports.
    The gate is generated from it and must not be edited directly.

Minimum supported Python version
    The lowest of the supported Python versions.
    print_minimum_supported_python_version.py prints it.

Minimum required Python version
    The maximum, over every distribution the package bundles, of the lowest
    Python version that distribution's Requires-Python admits. "Every
    distribution the package bundles" means both those found in the
    bundled distributions directory named by --bundled-distributions-dir
    and those whose pyproject.toml is found under --vendor-dir. No person
    writes this value down; it is computed.

    It is the maximum of the per-distribution minimums rather than the
    minimum, because a Python version works for the package only if it works
    for every bundled distribution at once, and the lowest version that
    satisfies all of them is the strictest of their individual lower bounds.

    Raised, when the work phase is given --lowest-resolvable-python-version,
    to at least the lowest supported Python version pip could resolve the
    pins for. Reading Requires-Python is the weaker test: it would miss a
    specifier carrying an exclusion, such as ">=3.9,!=3.10.*", which
    contributes a minimum of 3.9 while 3.10 is excluded, and it would miss a
    version for which no wheel is published at all. Whether pip can resolve
    the pins for a version is the property that actually decides whether the
    package can be built for it, so where the two disagree the resolution
    wins.
"""

from argparse import (
    ArgumentParser,
    Namespace,
    RawDescriptionHelpFormatter,
)
from collections.abc import Iterable
from importlib.metadata import distributions
from json import JSONDecodeError, loads
from os import environ
from pathlib import Path
from shutil import rmtree
from subprocess import CalledProcessError, run
from sys import executable, stderr

_CANDIDATE_PYTHON_VERSIONS: list[tuple[int, int]] = [
    (3, minor) for minor in range(0, 31)
] + [
    (4, minor) for minor in range(0, 31)
]

SUPPORTED_PYTHON_VERSIONS_FILE_NAME: str = (
    "supported_python_versions.json")

_REPOSITORY_ROOT: Path = Path(__file__).resolve().parents[3]

_PYTHON_PACKAGING_DIRECTORY: Path = (
    _REPOSITORY_ROOT / "packaging" / "common" / "python")


class MinimumRequiredPythonVersionError(Exception):
    """The minimum required Python version could not be derived or is 4.x."""


def build_argument_parser(description: str) -> ArgumentParser:
    """Return a parser carrying the option every script in this set takes."""
    argument_parser = ArgumentParser(
        description=description,
        formatter_class=RawDescriptionHelpFormatter)
    argument_parser.add_argument(
        "--supported-python-versions-file",
        default=str(
            _REPOSITORY_ROOT / "packaging" / "builder"
            / SUPPORTED_PYTHON_VERSIONS_FILE_NAME),
        help="path to the JSON array of supported Python versions that "
             "packaging/builder/download.go embeds "
             "(default: the one in this repository)")
    return argument_parser


def add_bundled_distributions_arguments(
    argument_parser: ArgumentParser
) -> None:
    """Add the options naming where the bundled distributions are found."""
    argument_parser.add_argument(
        "--bundled-distributions-dir",
        default=None,
        help="directory holding the bundled distributions, as produced by "
             "pip install --target; only the bundled distributions found "
             "there contribute to the minimum required Python version. "
             "Omit it to run the preparation phase, which builds the "
             "bundled distributions and then re-invokes this script with "
             "the directory it built")
    argument_parser.add_argument(
        "--vendor-dir",
        default=str(_PYTHON_PACKAGING_DIRECTORY / "vendor"),
        help="directory tree searched for vendored pyproject.toml files "
             "(default: packaging/common/python/vendor)")
    argument_parser.add_argument(
        "--lowest-resolvable-python-version",
        default=None,
        type=python_version_pair,
        help="the lowest supported Python version pip could resolve the "
             "pinned requirements for, as a \"major.minor\" string. The "
             "preparation phase passes what it found; it raises the minimum "
             "required Python version to at least this, so that a version no "
             "longer resolvable is reported even when the metadata alone "
             "would not show it")


def python_version_pair(version_text: str) -> tuple[int, int]:
    """Return the (major, minor) pair for a "major.minor" string.

    Raises ValueError when version_text is not two dot-separated numbers.
    """
    major, _, minor = version_text.partition(".")
    if not major.isdigit() or not minor.isdigit():
        raise ValueError(
            'expected a version like "3.10", found {!r}'.format(version_text))
    return (int(major), int(minor))


def read_supported_python_versions(
    supported_python_versions_text: str
) -> list[tuple[int, int]]:
    """Return the (major, minor) pairs of the supported Python versions.

    Takes the text of packaging/builder/supported_python_versions.json, which
    holds a JSON array of "major.minor" strings. Raises ValueError if the
    document is not such an array or lists no version.
    """
    try:
        parsed = loads(supported_python_versions_text)
    except JSONDecodeError as error:
        raise ValueError(
            "{} is not valid JSON: {}".format(
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME, error)) from error
    if not isinstance(parsed, list):
        raise ValueError(
            "{} must hold a JSON array, found {}".format(
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME, type(parsed).__name__))
    if not parsed:
        raise ValueError(
            "{} lists no version".format(
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME))
    versions: list[tuple[int, int]] = []
    for entry in parsed:
        try:
            versions.append(python_version_pair(entry))
        except (AttributeError, ValueError) as error:
            raise ValueError(
                '{} entries must be strings like "3.10", found {!r}'.format(
                    SUPPORTED_PYTHON_VERSIONS_FILE_NAME, entry)) from error
    return versions


def format_python_versions(versions: Iterable[tuple[int, int]]) -> str:
    """Return a comma-separated "major.minor" rendering of the given pairs."""
    return ", ".join("{}.{}".format(*version) for version in versions)


def derive_minimum_required_python_version_from_bundled_distributions(
    bundled_distributions_directory: str, vendor_directory: str
) -> tuple[int, int]:
    """Return the minimum required Python version of what the package ships.

    Reads Requires-Python from every distribution installed in
    bundled_distributions_directory and requires-python from every
    pyproject.toml under vendor_directory, then derives the value as the
    module docstring describes. Raises MinimumRequiredPythonVersionError
    when the bundled distributions directory is missing or empty, when
    nothing declared a constraint, or when the derived major version is
    not 3.
    """
    shipped_distributions = list(
        distributions(path=[bundled_distributions_directory]))
    if not shipped_distributions:
        raise MinimumRequiredPythonVersionError(
            "no bundled distributions found in {}: it is missing "
            "or empty, and must be populated with pip install --target "
            "before running this script".format(
                bundled_distributions_directory))
    requires_python_strings = [
        distribution.metadata.get("Requires-Python")
        for distribution in shipped_distributions]

    try:
        from tomllib import load
    except ModuleNotFoundError:
        from tomli import load
    for pyproject_path in Path(vendor_directory).rglob("pyproject.toml"):
        with open(pyproject_path, "rb") as pyproject_file:
            pyproject_data = load(pyproject_file)
        requires_python_strings.append(
            pyproject_data.get("project", {}).get("requires-python"))

    from packaging.specifiers import SpecifierSet
    from packaging.version import Version

    per_distribution_minimums: list[tuple[int, int]] = []
    for requires_python in requires_python_strings:
        if not requires_python:
            continue
        specifier_set = SpecifierSet(requires_python)
        for candidate_version in _CANDIDATE_PYTHON_VERSIONS:
            if specifier_set.contains(
                    Version("{}.{}".format(*candidate_version))):
                per_distribution_minimums.append(candidate_version)
                break

    if not per_distribution_minimums:
        raise MinimumRequiredPythonVersionError(
            "could not derive the minimum required Python version: no "
            "distribution or vendored pyproject declared Requires-Python")
    minimum_required_python_version = max(per_distribution_minimums)
    if minimum_required_python_version[0] != 3:
        raise MinimumRequiredPythonVersionError(
            "the minimum required Python version has major version {}, not "
            "3; {} and the sitecustomize.py gate only support 3.x and must "
            "be updated for major-version bumps".format(
                minimum_required_python_version[0],
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME))
    return minimum_required_python_version


def prepare_bundled_distributions_and_rerun_in_the_virtualenv(
    script_path: str,
    supported_python_versions: list[tuple[int, int]],
    arguments: Namespace,
) -> int:
    """Build the virtualenv and bundled distributions, rerun script_path.

    This is the preparation phase the module docstring describes. Resolves the
    pins for each supported Python version in ascending order, stopping at the
    first that resolves, and passes that version to the work phase as
    --lowest-resolvable-python-version. Returns the exit status the caller
    should exit with: the rerun script's own status, or a non-zero one if a
    preparation step failed.
    """
    build_directory = Path(
        environ.get("BUILD_DIR", str(_REPOSITORY_ROOT / "build")))
    venv_directory = (
        build_directory / "minimum-supported-python-version-venv")
    bundled_distributions_directory = build_directory / (
        "minimum-supported-python-version-bundled-distributions")
    pinned_requirements_path = (
        build_directory / "minimum-supported-python-version-requirements.txt")

    pinned_requirements = [
        line for line
        in (_PYTHON_PACKAGING_DIRECTORY / "requirements.txt").read_text(
            encoding="utf-8").splitlines()
        if not line.startswith("./vendor/")]
    build_directory.mkdir(parents=True, exist_ok=True)
    pinned_requirements_path.write_text(
        "\n".join(pinned_requirements) + "\n", encoding="utf-8")
    venv_python = str(venv_directory / "bin" / "python")
    for command in (
            [executable, "-m", "venv", "--clear", str(venv_directory)],
            [venv_python, "-m", "pip", "install", "--quiet",
             "packaging", "tomli"]):
        try:
            run(command, check=True)
        except CalledProcessError as error:
            print(
                "error: preparation step failed with exit status {}: "
                "{}".format(error.returncode, " ".join(command)),
                file=stderr)
            return error.returncode

    lowest_resolvable_python_version: tuple[int, int] | None = None
    for python_version in sorted(supported_python_versions):
        rmtree(bundled_distributions_directory, ignore_errors=True)
        resolution = run([
            venv_python, "-m", "pip", "install", "--quiet",
            "--target", str(bundled_distributions_directory),
            "--python-version", "{}.{}".format(*python_version),
            "--only-binary=:all:",
            "-r", str(pinned_requirements_path)])
        if resolution.returncode == 0:
            lowest_resolvable_python_version = python_version
            break
        print(
            "note: pip cannot resolve {} for Python {}.{}, so the package "
            "can no longer be built for it".format(
                pinned_requirements_path.name, *python_version),
            file=stderr)

    if lowest_resolvable_python_version is None:
        print(
            "error: pip cannot resolve {} for any supported Python version "
            "({}). Every supported Python version would have to be dropped, "
            "which would leave {} empty, so this needs a pin reverted or a "
            "newer interpreter added by hand.".format(
                pinned_requirements_path.name,
                format_python_versions(sorted(supported_python_versions)),
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME),
            file=stderr)
        return 1

    return run([
        venv_python, script_path,
        "--bundled-distributions-dir", str(bundled_distributions_directory),
        "--vendor-dir", arguments.vendor_dir,
        "--lowest-resolvable-python-version",
        "{}.{}".format(*lowest_resolvable_python_version),
        "--supported-python-versions-file",
        arguments.supported_python_versions_file]).returncode
