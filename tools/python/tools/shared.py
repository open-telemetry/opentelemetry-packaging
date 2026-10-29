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
    repeated here, nor in any test, nor in the documentation the package
    ships, so that editing it cannot leave a second copy naming interpreters
    the package no longer ships. The man page and README are templates the
    builder substitutes it into, and the interpreter cases in
    packaging/tests/python/sitecustomize_test.go are generated from it.

    A person maintains that list by hand. packaging/builder/download.go
    embeds it, the builder resolves the bundled distributions once per
    supported Python version, and it writes the same list into the
    sitecustomize.py version gate, so the file is the single
    hand-maintained statement of which interpreters the package supports.
    The gate is generated from it and must not be edited directly.

    The copy committed in sitecustomize.py is only what an unbuilt
    checkout shows; the builder overwrites it. It still has to equal the
    JSON, because the sitecustomize.py unit tests read it to decide which
    interpreters the gate must admit, and a stale copy would have them
    assert the gate admits an interpreter the package no longer ships.
    check_supported_python_versions.py compares the two, and
    prune_supported_python_versions.py rewrites both.

Minimum supported Python version
    The lowest of the supported Python versions.
    print_minimum_supported_python_version.py prints it.

Minimum required Python version
    The maximum, over every distribution the package bundles, of the lowest
    Python version that distribution's Requires-Python admits. "Every
    distribution the package bundles" means both those found in the
    bundled distributions directories named by --bundled-distributions-dir,
    one per target architecture, and those whose pyproject.toml is found
    under --vendor-dir. No person writes this value down; it is computed.

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

    Only a resolution pip itself reported as impossible raises it. pip exits
    non-zero for a network outage, a full disk or an interrupt just as it
    does for an unsatisfiable requirement, and reading the first as the
    second would report an interpreter as unbuildable — and let prune delete
    it from the supported Python versions — on nothing but a bad PyPI
    connection. A non-zero exit whose output carries none of the markers in
    _RESOLUTION_FAILURE_MARKERS therefore stops the run instead.

    The resolution targets the same platforms the builder does, not the
    host: every target architecture in _TARGET_ARCHITECTURES, with the
    implementation, ABI and manylinux platform tags that pipPlatformArgs
    and manylinuxPlatforms in packaging/builder/download.go pass. The
    closure carries compiled extensions, so resolving for the host would
    answer a question nobody asked — whether the bundle could be built on a
    maintainer's macOS arm64 laptop — while leaving the architectures the
    packages are actually built for unchecked. A supported Python version
    counts as resolvable only when it resolves for every target
    architecture. These tags must stay in step with download.go.
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
from re import Pattern, compile
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

# The architectures packaging/builder builds the Python package for, mapped
# to the machine name their manylinux platform tags carry.
_TARGET_ARCHITECTURES: dict[str, str] = {
    "amd64": "x86_64",
    "arm64": "aarch64",
}

_RESOLUTION_FAILURE_MARKERS: tuple[str, ...] = (
    "No matching distribution found",
    "Could not find a version that satisfies the requirement",
    "ResolutionImpossible",
    "conflicting dependencies",
    "Requires-Python",
)

_REPOSITORY_ROOT: Path = Path(__file__).resolve().parents[3]

_PYTHON_PACKAGING_DIRECTORY: Path = (
    _REPOSITORY_ROOT / "packaging" / "common" / "python")

SITECUSTOMIZE_FILE_NAME: str = "sitecustomize.py"

# The same line writeSitecustomize in packaging/builder/download.go finds and
# rewrites, matched the same way: by the trailing marker comment, requiring
# exactly one match.
_SITECUSTOMIZE_SUPPORTED_PYTHON_VERSIONS_PATTERN: Pattern[str] = compile(
    r'supported_python_versions = \[([0-9". ,]*)\]'
    r'  # supported-python-versions')


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
        action="append",
        dest="bundled_distributions_dirs",
        default=None,
        help="directory holding the bundled distributions, as produced by "
             "pip install --target; only the bundled distributions found "
             "there contribute to the minimum required Python version. "
             "Repeat it once per target architecture, since a distribution "
             "can resolve to a different version on each. Omit it to run "
             "the preparation phase, which builds the bundled distributions "
             "and then re-invokes this script with the directories it built")
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


def add_sitecustomize_argument(argument_parser: ArgumentParser) -> None:
    """Add the option naming the sitecustomize.py whose gate mirrors the JSON."""
    argument_parser.add_argument(
        "--sitecustomize-file",
        default=str(_PYTHON_PACKAGING_DIRECTORY / SITECUSTOMIZE_FILE_NAME),
        help="path to the sitecustomize.py carrying the generated "
             "supported-python-versions line "
             "(default: the one in this repository)")


def read_sitecustomize_supported_python_versions(
    sitecustomize_text: str
) -> list[tuple[int, int]]:
    """Return the versions the sitecustomize.py gate is committed with.

    Finds the generated line by its trailing supported-python-versions
    marker, the way writeSitecustomize in packaging/builder/download.go
    finds it. Raises ValueError unless there is exactly one such line and it
    lists "major.minor" strings.
    """
    listed = _SITECUSTOMIZE_SUPPORTED_PYTHON_VERSIONS_PATTERN.findall(
        sitecustomize_text)
    if len(listed) != 1:
        raise ValueError(
            "expected exactly 1 supported-python-versions marker in {}, "
            "found {}".format(SITECUSTOMIZE_FILE_NAME, len(listed)))
    try:
        return read_supported_python_versions("[{}]".format(listed[0]))
    except ValueError as error:
        raise ValueError(
            "the supported-python-versions line in {} does not list "
            '"major.minor" strings: {}'.format(
                SITECUSTOMIZE_FILE_NAME, error)) from error


def rewrite_sitecustomize_supported_python_versions(
    sitecustomize_text: str, versions: Iterable[tuple[int, int]]
) -> str:
    """Return sitecustomize_text with the gate listing the given versions.

    Writes the line the same way writeSitecustomize in
    packaging/builder/download.go writes it, so a checkout and a built
    package carry the same text. Raises ValueError unless there is exactly
    one line to rewrite.
    """
    replacement = (
        "supported_python_versions = [{}]  # supported-python-versions".format(
            ", ".join('"{}.{}"'.format(*version) for version in versions)))
    rewritten, rewritten_count = (
        _SITECUSTOMIZE_SUPPORTED_PYTHON_VERSIONS_PATTERN.subn(
            replacement, sitecustomize_text))
    if rewritten_count != 1:
        raise ValueError(
            "expected exactly 1 supported-python-versions marker in {}, "
            "found {}".format(SITECUSTOMIZE_FILE_NAME, rewritten_count))
    return rewritten


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


def pip_target_platform_arguments(
    python_version: tuple[int, int], architecture: str
) -> list[str]:
    """Return the pip flags pinning a resolution to a target platform.

    These mirror pipPlatformArgs and manylinuxPlatforms in
    packaging/builder/download.go: the same implementation, ABI tags and
    manylinux platform tags, so what resolves here is what the builder can
    resolve. architecture is a key of _TARGET_ARCHITECTURES; raises
    ValueError for anything else.
    """
    try:
        machine = _TARGET_ARCHITECTURES[architecture]
    except KeyError as error:
        raise ValueError(
            "unsupported target architecture {!r}, expected one of {}".format(
                architecture, ", ".join(sorted(_TARGET_ARCHITECTURES)))
        ) from error
    arguments = [
        "--only-binary=:all:",
        "--python-version", "{}.{}".format(*python_version),
        "--implementation", "cp",
        "--abi", "cp{}{}".format(*python_version),
        "--abi", "abi3",
        "--abi", "none",
    ]
    for platform_tag in (
            "manylinux2014_{}", "manylinux_2_17_{}",
            "manylinux_2_28_{}", "manylinux1_{}"):
        arguments += ["--platform", platform_tag.format(machine)]
    return arguments


def is_resolution_failure(pip_output: str) -> bool:
    """Return whether pip's output says it could not satisfy the requirements.

    pip_output is pip's combined stdout and stderr. A non-zero exit that
    carries none of these markers came from something other than the
    requirements — a network outage, a full disk, an interrupt — and says
    nothing about whether the interpreter is still supported.
    """
    return any(
        marker in pip_output for marker in _RESOLUTION_FAILURE_MARKERS)


def format_python_versions(versions: Iterable[tuple[int, int]]) -> str:
    """Return a comma-separated "major.minor" rendering of the given pairs."""
    return ", ".join("{}.{}".format(*version) for version in versions)


def derive_minimum_required_python_version_from_bundled_distributions(
    bundled_distributions_directories: list[str], vendor_directory: str
) -> tuple[int, int]:
    """Return the minimum required Python version of what the package ships.

    Reads Requires-Python from every distribution installed in each of
    bundled_distributions_directories — one per target architecture, since
    a distribution can resolve to a different version on each — and
    requires-python from every pyproject.toml under vendor_directory, then
    derives the value as the module docstring describes. Raises
    MinimumRequiredPythonVersionError when the bundled distributions
    directories are all missing or empty, when nothing declared a
    constraint, or when the derived major version is not 3.
    """
    shipped_distributions = list(
        distributions(path=list(bundled_distributions_directories)))
    if not shipped_distributions:
        raise MinimumRequiredPythonVersionError(
            "no bundled distributions found in {}: they are missing "
            "or empty, and must be populated with pip install --target "
            "before running this script".format(
                ", ".join(bundled_distributions_directories)))
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
    pins for each supported Python version in ascending order, for every
    target architecture, stopping at the first version that resolves for all
    of them, and passes that version to the work phase as
    --lowest-resolvable-python-version. Returns the exit status the caller
    should exit with: the rerun script's own status, or a non-zero one if a
    preparation step failed.
    """
    build_directory = Path(
        environ.get("BUILD_DIR", str(_REPOSITORY_ROOT / "build")))
    venv_directory = (
        build_directory / "minimum-supported-python-version-venv")
    bundled_distributions_root = build_directory / (
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
    bundled_distributions_directories: list[Path] = []
    for python_version in sorted(supported_python_versions):
        resolved_directories: list[Path] = []
        for architecture in sorted(_TARGET_ARCHITECTURES):
            target_directory = bundled_distributions_root / "{}.{}-{}".format(
                *python_version, architecture)
            rmtree(target_directory, ignore_errors=True)
            resolution = run(
                [venv_python, "-m", "pip", "install", "--quiet",
                 "--target", str(target_directory),
                 "-r", str(pinned_requirements_path)]
                + pip_target_platform_arguments(python_version, architecture),
                capture_output=True, text=True)
            if resolution.returncode == 0:
                resolved_directories.append(target_directory)
                continue
            pip_output = resolution.stdout + resolution.stderr
            if not is_resolution_failure(pip_output):
                print(
                    "error: pip exited {} for Python {}.{} on linux/{} "
                    "without reporting a resolution failure, so whether the "
                    "pins still support that interpreter is unknown. Fix "
                    "what pip is reporting below and run this again; nothing "
                    "was concluded about {}.".format(
                        resolution.returncode, *python_version, architecture,
                        SUPPORTED_PYTHON_VERSIONS_FILE_NAME),
                    file=stderr)
                print(pip_output, file=stderr)
                return resolution.returncode or 1
            print(
                "note: pip cannot resolve {} for Python {}.{} on linux/{}, "
                "so the package can no longer be built for it".format(
                    pinned_requirements_path.name, *python_version,
                    architecture),
                file=stderr)
            print(pip_output, file=stderr)
            break
        else:
            lowest_resolvable_python_version = python_version
            bundled_distributions_directories = resolved_directories
            break

    if lowest_resolvable_python_version is None:
        print(
            "error: pip cannot resolve {} for any supported Python version "
            "({}) on every target architecture ({}). Every supported Python "
            "version would have to be dropped, which would leave {} empty, "
            "so this needs a pin reverted or a newer interpreter added by "
            "hand.".format(
                pinned_requirements_path.name,
                format_python_versions(sorted(supported_python_versions)),
                ", ".join(sorted(_TARGET_ARCHITECTURES)),
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME),
            file=stderr)
        return 1

    command = [venv_python, script_path]
    for directory in bundled_distributions_directories:
        command += ["--bundled-distributions-dir", str(directory)]
    command += [
        "--vendor-dir", arguments.vendor_dir,
        "--lowest-resolvable-python-version",
        "{}.{}".format(*lowest_resolvable_python_version),
        "--supported-python-versions-file",
        arguments.supported_python_versions_file,
        "--sitecustomize-file", arguments.sitecustomize_file]
    return run(command).returncode
