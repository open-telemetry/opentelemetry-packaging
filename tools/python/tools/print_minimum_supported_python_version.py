# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Print the minimum supported Python version as major.minor.

The minimum supported Python version and the supported Python versions it is
the lowest of are defined in the module docstring of shared.py, next to this
script, which is the only place either term is defined.

This script needs no virtualenv and no bundled distributions, so CI can run
it to decide which interpreter to install before running
check_supported_python_versions.py.
"""

from pathlib import Path

from shared import build_argument_parser, read_supported_python_versions


def main() -> int:
    arguments = build_argument_parser(__doc__).parse_args()

    supported_python_versions = read_supported_python_versions(
        Path(arguments.supported_python_versions_file).read_text(
            encoding="utf-8"))

    print("{}.{}".format(*min(supported_python_versions)))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
