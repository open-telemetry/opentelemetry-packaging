# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Derive and enforce the sitecustomize.py minimum Python version.

The minimum supported Python of the bundled agent is the strictest
Requires-Python lower bound across every distribution installed in the running
interpreter (the PyPI pins from requirements.txt) and every vendored package's
pyproject.toml. This script derives that floor and either checks it against the
sitecustomize.py version gate (--check) or rewrites the gate to match (--write).

The gate is a plain integer comparison in sitecustomize.py so that the file
still parses and runs on ancient interpreters. The comparison reads a named
constant marked with an anchor comment:

    _MINIMUM_PYTHON_MINOR = 10  # sync-minimum-python-version: 3.x minor floor

This script only knows how to keep the minor of a 3.x floor in sync. If the
derived major is not 3, it exits with an error asking for a gate refactor.
"""

from argparse import ArgumentParser
from importlib.metadata import distributions
from os.path import dirname, join
from pathlib import Path
from re import compile as re_compile
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
# as needing a gate refactor rather than silently mis-derived.
_CANDIDATE_VERSIONS = [(3, minor) for minor in range(0, 31)] + [
    (4, minor) for minor in range(0, 31)
]

_GATE_CONSTANT_PATTERN = re_compile(
    r"_MINIMUM_PYTHON_MINOR = (\d+)  # sync-minimum-python-version")

_HUMAN_READABLE_COMMENT_PATTERN = re_compile(r"# Require Python >= 3\.\d+\b")


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


def read_gate_minor_from_sitecustomize(sitecustomize_text):
    """Return the current _MINIMUM_PYTHON_MINOR integer from the gate marker."""
    match = _GATE_CONSTANT_PATTERN.search(sitecustomize_text)
    if match is None:
        raise ValueError(
            "no sync-minimum-python-version marker found in sitecustomize.py")
    return int(match.group(1))


def rewrite_gate_minor_in_sitecustomize(sitecustomize_text, new_minor):
    """Return sitecustomize text with the gate minor set to new_minor.

    Rewrites both the marked constant line and the human-readable
    "# Require Python >= 3.N" comment above it. Asserts that exactly one
    marker line is present before substituting.
    """
    marker_match_count = len(
        _GATE_CONSTANT_PATTERN.findall(sitecustomize_text))
    if marker_match_count != 1:
        raise ValueError(
            "expected exactly 1 sync-minimum-python-version marker, "
            "found {}".format(marker_match_count))
    rewritten = _GATE_CONSTANT_PATTERN.sub(
        "_MINIMUM_PYTHON_MINOR = {}  # sync-minimum-python-version".format(
            new_minor),
        sitecustomize_text)
    rewritten = _HUMAN_READABLE_COMMENT_PATTERN.sub(
        "# Require Python >= 3.{}".format(new_minor), rewritten)
    return rewritten


def main():
    argument_parser = ArgumentParser(description=__doc__)
    script_directory = dirname(__file__)
    argument_parser.add_argument(
        "--sitecustomize",
        default=join(script_directory, "sitecustomize.py"),
        help="path to sitecustomize.py (default: next to this script)")
    argument_parser.add_argument(
        "--vendor-dir",
        default=join(script_directory, "vendor"),
        help="directory tree searched for vendored pyproject.toml files "
             "(default: the vendor dir next to this script)")
    mode_group = argument_parser.add_mutually_exclusive_group(required=True)
    mode_group.add_argument(
        "--check",
        action="store_true",
        help="verify the gate matches the derived floor; exit 1 if it differs")
    mode_group.add_argument(
        "--write",
        action="store_true",
        help="rewrite the gate constant to match the derived floor")
    arguments = argument_parser.parse_args()

    # Collect Requires-Python from every installed distribution, then from
    # every vendored pyproject.toml. The importlib.metadata enumeration and the
    # pyproject reads are inlined here (each is used only in this one place);
    # the derivation logic they feed is a reusable, separately tested function.
    requires_python_strings = [
        distribution.metadata["Requires-Python"]
        for distribution in distributions()]
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
            "derived minimum Python major is {}, not 3; the sitecustomize.py "
            "gate refactor only supports 3.x floors and must be updated for "
            "major-version bumps".format(derived_major),
            file=stderr)
        return 1

    with open(arguments.sitecustomize, encoding="utf-8") as sitecustomize_file:
        sitecustomize_text = sitecustomize_file.read()
    current_minor = read_gate_minor_from_sitecustomize(sitecustomize_text)

    if arguments.check:
        print("derived minimum Python: 3.{}".format(derived_minor))
        print("sitecustomize gate: 3.{}".format(current_minor))
        if derived_minor != current_minor:
            print(
                "gate is out of sync with the bundled distributions; run "
                "sync_minimum_python_version.py --write to update it",
                file=stderr)
            return 1
        print("gate is in sync")
        return 0

    if derived_minor == current_minor:
        print("gate already at 3.{}; nothing to rewrite".format(current_minor))
        return 0
    rewritten_text = rewrite_gate_minor_in_sitecustomize(
        sitecustomize_text, derived_minor)
    with open(
            arguments.sitecustomize, "w",
            encoding="utf-8") as sitecustomize_file:
        sitecustomize_file.write(rewritten_text)
    print(
        "rewrote sitecustomize gate from 3.{} to 3.{}".format(
            current_minor, derived_minor))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
