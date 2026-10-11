# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Derive and enforce the minimum Python version the package is built for.

The minimum supported Python of the bundled agent is the strictest
Requires-Python lower bound across every distribution that ships in the package
and every vendored package's pyproject.toml. This script derives that floor and
either checks it against the interpreter list the builder resolves wheels for
(--check) or prunes that list to match (--write).

The list lives in packaging/builder/supported_python_versions.json:

    ["3.10", "3.11", "3.12", "3.13"]

packaging/builder/download.go embeds that file, the builder resolves and
installs the payload once per entry, and it writes the same set into the
sitecustomize.py version gate, so this file is the single hand-maintained
statement of which interpreters the package supports. The gate itself is
generated from it and must not be edited directly.

Keeping the list in a data file rather than in the Go source is what lets this
tool read and rewrite it with a JSON parser. Extracting a Go slice literal with
a regular expression cannot tell a live entry from one inside a // comment.

An entry below the derived floor is an error: pip cannot resolve the payload for
an interpreter the bundled distributions reject, so the build either fails or
ships something that cannot run. A floor below every entry is not an error. The
top of the list is governed by whether wheels exist rather than by
Requires-Python, and declining to ship an interpreter the dependencies would
permit is a deliberate choice.

The shipped distributions are enumerated from the payload directory named by
--payload-dir: the directory that a "pip install --target" of requirements.txt
produces, which is what packaging/builder/download.go writes into the DEB and
the RPM. Scoping the enumeration to that directory keeps distributions that
never ship out of the derivation: pip and setuptools, which "python -m venv"
creates in any surrounding virtualenv, and tomli, which only this tool imports.
Because the floor is a maximum over per-distribution lower bounds, a
distribution that does not ship can only raise it and never lower it, so
including one would mask a floor that should drop while --check still reported
the list as in sync.

This tool only knows how to keep a list of 3.x interpreters in sync. If the
derived major is not 3, it exits with an error asking for a refactor.
"""

from argparse import ArgumentParser
from importlib.metadata import distributions
from json import JSONDecodeError, dumps, loads
from os.path import dirname, join
from pathlib import Path
from sys import stderr

from packaging.specifiers import SpecifierSet
from packaging.version import Version

# tomllib is standard library since Python 3.11. This tool must also run under
# the current 3.10 floor (so pip resolves transitive dependencies exactly as it
# would on the minimum interpreter), where tomllib is absent and the tomli
# backport is installed alongside packaging instead.
try:
    from tomllib import load
except ModuleNotFoundError:
    from tomli import load

# The lowest major.minor pairs we scan when probing a specifier for the lowest
# version it admits. Python 3 minors are the realistic range for this project;
# major 4 is included so that a future 4.x-only floor is detected and reported
# as needing a refactor rather than silently mis-derived.
_CANDIDATE_VERSIONS = [(3, minor) for minor in range(0, 31)] + [
    (4, minor) for minor in range(0, 31)
]

_VERSIONS_FILE_NAME = "supported_python_versions.json"


def lowest_supported_major_minor_across_requires_python(
        requires_python_strings):
    """Return the strictest (major, minor) lower bound over the given specs.

    Each item is a Requires-Python string (for example ">=3.10" or
    "~=3.11,!=3.12.*"). For each specifier we find the lowest candidate
    major.minor it admits, then return the maximum of those per-specifier
    minimums: the floor every bundled distribution can agree on. Strings that
    are empty or None are ignored (a distribution without Requires-Python
    imposes no floor).
    """
    per_specifier_minimums = []
    for requires_python in requires_python_strings:
        if not requires_python:
            continue
        specifier_set = SpecifierSet(requires_python)
        lowest_admitted = None
        for major, minor in _CANDIDATE_VERSIONS:
            if specifier_set.contains(Version("{}.{}".format(major, minor))):
                lowest_admitted = (major, minor)
                break
        if lowest_admitted is not None:
            per_specifier_minimums.append(lowest_admitted)
    if not per_specifier_minimums:
        return None
    return max(per_specifier_minimums)


def read_supported_versions(versions_text):
    """Return the (major, minor) pairs listed in the supported versions file.

    Takes the text of packaging/builder/supported_python_versions.json, which
    holds a JSON array of "major.minor" strings. Raises ValueError if the
    document is not such an array or lists no version.
    """
    try:
        parsed = loads(versions_text)
    except JSONDecodeError as error:
        raise ValueError(
            "{} is not valid JSON: {}".format(
                _VERSIONS_FILE_NAME, error)) from error
    if not isinstance(parsed, list):
        raise ValueError(
            "{} must hold a JSON array, found {}".format(
                _VERSIONS_FILE_NAME, type(parsed).__name__))
    if not parsed:
        raise ValueError("{} lists no version".format(_VERSIONS_FILE_NAME))
    versions = []
    for entry in parsed:
        major, _, minor = entry.partition(".") if isinstance(
            entry, str) else ("", "", "")
        if not major.isdigit() or not minor.isdigit():
            raise ValueError(
                '{} entries must be strings like "3.10", found {!r}'.format(
                    _VERSIONS_FILE_NAME, entry))
        versions.append((int(major), int(minor)))
    return versions


def prune_supported_versions(versions_text, floor):
    """Return supported versions JSON with the versions below floor removed.

    floor is a (major, minor) pair. Raises ValueError if pruning would empty
    the list, which would mean no interpreter the builder knows about can run
    the bundled distributions at all.
    """
    kept = [
        version for version in read_supported_versions(versions_text)
        if version >= floor]
    if not kept:
        raise ValueError(
            "pruning to the derived floor {}.{} would empty {}".format(
                *floor, _VERSIONS_FILE_NAME))
    return dumps(["{}.{}".format(*version) for version in kept]) + "\n"


def format_versions(versions):
    """Return a comma-separated "major.minor" rendering of the given pairs."""
    return ", ".join("{}.{}".format(*version) for version in versions)


def main():
    argument_parser = ArgumentParser(description=__doc__)
    script_directory = dirname(__file__)
    argument_parser.add_argument(
        "--versions-file",
        default=join(
            script_directory, "..", "..", "builder", _VERSIONS_FILE_NAME),
        help="path to the JSON array of supported interpreters that "
             "packaging/builder/download.go embeds "
             "(default: the one in this repository)")
    argument_parser.add_argument(
        "--payload-dir",
        required=True,
        help="directory holding the distributions that ship in the package, "
             "as produced by pip install --target; only the distributions "
             "found there contribute to the derived floor")
    argument_parser.add_argument(
        "--vendor-dir",
        default=join(script_directory, "vendor"),
        help="directory tree searched for vendored pyproject.toml files "
             "(default: the vendor dir next to this script)")
    mode_group = argument_parser.add_mutually_exclusive_group(required=True)
    mode_group.add_argument(
        "--check",
        action="store_true",
        help="verify no supported interpreter is below the derived floor; "
             "exit 1 if one is")
    mode_group.add_argument(
        "--write",
        action="store_true",
        help="drop the supported interpreters below the derived floor")
    arguments = argument_parser.parse_args()

    # Collect Requires-Python from every shipped distribution, then from every
    # vendored pyproject.toml. The enumeration is scoped to the payload
    # directory with path=; called with no arguments, distributions() would walk
    # the running interpreter's sys.path and pick up pip, setuptools and tomli,
    # which the DEB and the RPM never ship. The importlib.metadata enumeration
    # and the pyproject reads are inlined here (each is used only in this one
    # place); the derivation logic they feed is a reusable, separately tested
    # function.
    shipped_distributions = list(distributions(path=[arguments.payload_dir]))
    if not shipped_distributions:
        print(
            "no distributions found in payload directory {}: it is missing or "
            "empty, and must be populated with pip install --target before "
            "running this check".format(arguments.payload_dir),
            file=stderr)
        return 1
    requires_python_strings = [
        distribution.metadata["Requires-Python"]
        for distribution in shipped_distributions]
    for pyproject_path in Path(arguments.vendor_dir).rglob("pyproject.toml"):
        with open(pyproject_path, "rb") as pyproject_file:
            pyproject_data = load(pyproject_file)
        requires_python_strings.append(
            pyproject_data.get("project", {}).get("requires-python"))

    derived_floor = lowest_supported_major_minor_across_requires_python(
        requires_python_strings)
    if derived_floor is None:
        print(
            "could not derive a minimum Python version: no distribution or "
            "vendored pyproject declared Requires-Python",
            file=stderr)
        return 1
    derived_major, derived_minor = derived_floor
    if derived_major != 3:
        print(
            "derived minimum Python major is {}, not 3; {} and the "
            "sitecustomize.py gate only support 3.x and must be updated for "
            "major-version bumps".format(
                derived_major, _VERSIONS_FILE_NAME),
            file=stderr)
        return 1

    with open(arguments.versions_file, encoding="utf-8") as versions_file:
        versions_text = versions_file.read()
    supported_versions = read_supported_versions(versions_text)
    below_floor = [
        version for version in supported_versions if version < derived_floor]

    print("derived minimum Python: {}.{}".format(derived_major, derived_minor))
    print("supported Python versions: {}".format(
        format_versions(supported_versions)))

    if arguments.check:
        if below_floor:
            print(
                "{} lists {} below the derived floor {}.{}; pip cannot "
                "resolve the payload for those interpreters. Run "
                "sync_minimum_python_version.py --write to drop them.".format(
                    _VERSIONS_FILE_NAME, format_versions(below_floor),
                    derived_major, derived_minor),
                file=stderr)
            return 1
        if min(supported_versions) > derived_floor:
            print(
                "note: the bundled distributions would also permit {}.{}, "
                "which the package does not ship".format(
                    derived_major, derived_minor))
        print("supported Python versions are in sync")
        return 0

    if not below_floor:
        print("no supported interpreter is below the floor; nothing to prune")
        return 0
    pruned_text = prune_supported_versions(versions_text, derived_floor)
    with open(arguments.versions_file, "w", encoding="utf-8") as versions_file:
        versions_file.write(pruned_text)
    print("dropped {} from {}".format(
        format_versions(below_floor), _VERSIONS_FILE_NAME))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
