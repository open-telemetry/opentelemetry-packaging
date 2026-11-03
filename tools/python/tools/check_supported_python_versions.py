# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Check that no supported Python version is below the minimum required one.

Exits 1 and names the versions that are, so the message says which entries
of packaging/builder/supported_python_versions.json stopped agreeing with
what the package bundles.

Also checks that the generated supported-python-versions line committed in
packaging/common/python/sitecustomize.py lists the same versions as that
JSON, since the sitecustomize.py unit tests read the committed line to
decide which interpreters the version gate must admit.

The supported Python versions and the minimum required Python version are
defined in the module docstring of shared.py, next to this script, which is
the only place either term is defined.

This script never writes a file. Run prune_supported_python_versions.py to
remove from packaging/builder/supported_python_versions.json any supported
Python version that is below the minimum required Python version.
"""

from pathlib import Path
from sys import stderr

from shared import (
    SITECUSTOMIZE_FILE_NAME,
    SUPPORTED_PYTHON_VERSIONS_FILE_NAME,
    MinimumRequiredPythonVersionError,
    add_bundled_distributions_arguments,
    add_sitecustomize_argument,
    build_argument_parser,
    derive_minimum_required_python_version_from_bundled_distributions,
    format_python_versions,
    prepare_bundled_distributions_and_rerun_in_the_virtualenv,
    read_sitecustomize_supported_python_versions,
    read_supported_python_versions,
)


def main() -> int:
    argument_parser = build_argument_parser(__doc__)
    add_bundled_distributions_arguments(argument_parser)
    add_sitecustomize_argument(argument_parser)
    arguments = argument_parser.parse_args()

    supported_python_versions = read_supported_python_versions(
        Path(arguments.supported_python_versions_file).read_text(
            encoding="utf-8"))

    # Cheap, needs no network, and a mismatch makes the sitecustomize.py unit
    # tests assert against interpreters the package may no longer ship, so it
    # runs before anything is resolved.
    sitecustomize_python_versions = (
        read_sitecustomize_supported_python_versions(
            Path(arguments.sitecustomize_file).read_text(encoding="utf-8")))
    if sitecustomize_python_versions != supported_python_versions:
        print(
            "the generated supported-python-versions line in {} lists {}, "
            "but {} lists {}. The JSON is the source of truth; run "
            "prune_supported_python_versions.py, or copy the list by hand, "
            "so the committed gate matches what the builder would "
            "write.".format(
                SITECUSTOMIZE_FILE_NAME,
                format_python_versions(sitecustomize_python_versions),
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME,
                format_python_versions(supported_python_versions)),
            file=stderr)
        return 1

    if arguments.bundled_distributions_dirs is None:
        return prepare_bundled_distributions_and_rerun_in_the_virtualenv(
            __file__, supported_python_versions, arguments)

    try:
        declared_python_version = (
            derive_minimum_required_python_version_from_bundled_distributions(
                arguments.bundled_distributions_dirs,
                arguments.vendor_dir))
    except MinimumRequiredPythonVersionError as error:
        print(error, file=stderr)
        return 1

    minimum_required_python_version = declared_python_version
    if arguments.lowest_resolvable_python_version is not None:
        minimum_required_python_version = max(
            minimum_required_python_version,
            arguments.lowest_resolvable_python_version)

    print("minimum required Python version: {}.{}".format(
        *minimum_required_python_version))
    print("supported Python versions: {}".format(
        format_python_versions(supported_python_versions)))

    below_minimum_required_python_version: list[tuple[int, int]] = [
        version for version in supported_python_versions
        if version < minimum_required_python_version]
    if below_minimum_required_python_version:
        print(
            "{} lists {} below the minimum required Python version {}.{}; "
            "pip cannot resolve the bundled distributions for those "
            "interpreters. Run prune_supported_python_versions.py to "
            "drop them.".format(
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME,
                format_python_versions(
                    below_minimum_required_python_version),
                *minimum_required_python_version),
            file=stderr)
        return 1

    # Compared against what the metadata declares, not against the minimum
    # required Python version: that one is raised to the lowest supported
    # Python version pip could resolve for, which is itself a supported
    # Python version, so the comparison could never hold.
    if declared_python_version < min(supported_python_versions):
        print(
            "note: the Requires-Python of the bundled distributions would "
            "admit {}.{}, below every supported Python version. Whether the "
            "pins resolve there was not tested, since only supported Python "
            "versions are resolved, and wheel availability rather than "
            "Requires-Python governs the list.".format(
                *declared_python_version))
    print("supported Python versions are in sync")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
