# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Check that no supported Python version is below the minimum required one.

Exits 1 and names the versions that are, so the message says which entries
of packaging/builder/supported_python_versions.json stopped agreeing with
what the package bundles.

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
    SUPPORTED_PYTHON_VERSIONS_FILE_NAME,
    MinimumRequiredPythonVersionError,
    add_bundled_distributions_arguments,
    build_argument_parser,
    derive_minimum_required_python_version_from_bundled_distributions,
    format_python_versions,
    prepare_bundled_distributions_and_rerun_in_the_virtualenv,
    read_supported_python_versions,
)


def main() -> int:
    argument_parser = build_argument_parser(__doc__)
    add_bundled_distributions_arguments(argument_parser)
    arguments = argument_parser.parse_args()

    supported_python_versions = read_supported_python_versions(
        Path(arguments.supported_python_versions_file).read_text(
            encoding="utf-8"))

    if arguments.bundled_distributions_dir is None:
        return prepare_bundled_distributions_and_rerun_in_the_virtualenv(
            __file__, supported_python_versions, arguments)

    try:
        minimum_required_python_version = (
            derive_minimum_required_python_version_from_bundled_distributions(
                arguments.bundled_distributions_dir, arguments.vendor_dir))
    except MinimumRequiredPythonVersionError as error:
        print(error, file=stderr)
        return 1

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

    if min(supported_python_versions) > minimum_required_python_version:
        print(
            "note: the bundled distributions would also permit {}.{}, "
            "which the package does not ship".format(
                *minimum_required_python_version))
    print("supported Python versions are in sync")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
