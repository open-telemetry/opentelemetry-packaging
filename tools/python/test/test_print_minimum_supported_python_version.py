# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for print_minimum_supported_python_version.py."""

from pathlib import Path
from subprocess import run
from sys import executable
from tempfile import TemporaryDirectory
from unittest import TestCase

from shared import read_supported_python_versions
from shared_test import (
    PRINT_SCRIPT_PATH,
    SHIPPED_SUPPORTED_PYTHON_VERSIONS_FILE,
    write_supported_python_versions_file,
)


class TestPrintMinimumSupportedPythonVersion(TestCase):

    def test_prints_the_lowest_entry_and_needs_no_bundled_distributions(self) -> None:
        # CI runs this before any interpreter is installed, to decide which
        # one to install, so it must work without a virtualenv or any
        # bundled distributions.
        # The entries are out of order so that the lowest one rather than the
        # first one is what gets printed.
        with TemporaryDirectory() as temporary_directory:
            supported_python_versions_path = (
                Path(temporary_directory) / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.11", "3.12", "3.10"])

            completed = run(
                [
                    executable, PRINT_SCRIPT_PATH,
                    "--supported-python-versions-file",
                    str(supported_python_versions_path),
                ],
                capture_output=True, text=True)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual("3.10", completed.stdout.strip())

    def test_the_shipped_file_prints_the_version_the_workflow_installs(
        self
    ) -> None:
        completed = run(
            [executable, PRINT_SCRIPT_PATH],
            capture_output=True, text=True)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertEqual(
            "{}.{}".format(*min(read_supported_python_versions(
                SHIPPED_SUPPORTED_PYTHON_VERSIONS_FILE.read_text(
                    encoding="utf-8")))),
            completed.stdout.strip())
