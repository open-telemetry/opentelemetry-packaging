# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Drop the supported Python versions below the minimum required one.

The supported Python versions and the minimum required Python version are
defined in the module docstring of shared.py, next to this script, which is
the only place either term is defined.

This script only ever removes entries from
packaging/builder/supported_python_versions.json. Adding an interpreter to
that file is a hand edit. Run check_supported_python_versions.py to see what
this script would drop without writing anything.

It also writes the kept versions into the generated supported-python-versions
line of packaging/common/python/sitecustomize.py, so the committed gate never
names an interpreter the JSON has dropped.
"""

from json import dumps
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
    read_supported_python_versions,
    rewrite_sitecustomize_supported_python_versions,
)


def prune_supported_python_versions(
    supported_python_versions_text: str,
    minimum_required_python_version: tuple[int, int],
) -> str:
    """Return the supported Python versions JSON without the ones below.

    minimum_required_python_version is a (major, minor) pair. Raises
    ValueError if pruning would empty the list, which would mean no
    interpreter the builder knows about can run the bundled distributions
    at all.
    """
    kept: list[tuple[int, int]] = [
        version
        for version in read_supported_python_versions(
            supported_python_versions_text)
        if version >= minimum_required_python_version]
    if not kept:
        raise ValueError(
            "pruning to the minimum required Python version {}.{} would "
            "empty {}".format(
                *minimum_required_python_version,
                SUPPORTED_PYTHON_VERSIONS_FILE_NAME))
    return dumps(["{}.{}".format(*version) for version in kept]) + "\n"


def main() -> int:
    argument_parser = build_argument_parser(__doc__)
    add_bundled_distributions_arguments(argument_parser)
    add_sitecustomize_argument(argument_parser)
    arguments = argument_parser.parse_args()

    supported_python_versions_text = Path(
        arguments.supported_python_versions_file).read_text(encoding="utf-8")
    supported_python_versions = read_supported_python_versions(
        supported_python_versions_text)

    if arguments.bundled_distributions_dirs is None:
        return prepare_bundled_distributions_and_rerun_in_the_virtualenv(
            __file__, supported_python_versions, arguments)

    try:
        minimum_required_python_version = (
            derive_minimum_required_python_version_from_bundled_distributions(
                arguments.bundled_distributions_dirs,
                arguments.vendor_dir))
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
        pruned_text = prune_supported_python_versions(
            supported_python_versions_text, minimum_required_python_version)
        Path(arguments.supported_python_versions_file).write_text(
            pruned_text, encoding="utf-8")
        print("dropped {} from {}".format(
            format_python_versions(below_minimum_required_python_version),
            SUPPORTED_PYTHON_VERSIONS_FILE_NAME))
    else:
        print("no supported Python version is below the minimum required "
              "Python version; nothing to prune")

    # Unconditional: the committed gate can have drifted from the JSON
    # without this run dropping anything, and a stale gate is what the
    # sitecustomize.py unit tests would then assert against.
    kept = [
        version for version in supported_python_versions
        if version >= minimum_required_python_version]
    sitecustomize_path = Path(arguments.sitecustomize_file)
    sitecustomize_text = sitecustomize_path.read_text(encoding="utf-8")
    rewritten_text = rewrite_sitecustomize_supported_python_versions(
        sitecustomize_text, kept)
    if rewritten_text != sitecustomize_text:
        sitecustomize_path.write_text(rewritten_text, encoding="utf-8")
        print("wrote {} into the generated line in {}".format(
            format_python_versions(kept), SITECUSTOMIZE_FILE_NAME))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
