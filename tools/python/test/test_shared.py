# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for the code in shared.py that several scripts use."""

from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import TestCase

from shared import (
    MinimumRequiredPythonVersionError,
    derive_minimum_required_python_version_from_bundled_distributions,
    python_version_pair,
    read_supported_python_versions,
)
from shared_test import write_dist_info_with_requires_python


class TestDeriveMinimumRequiredPythonVersion(TestCase):
    """The derivation, driven by bundled distributions built in a temp dir.

    Each case writes the dist-info metadata a real bundled distribution
    carries, so the Requires-Python strings reach the derivation the way
    they do in a build, with no pip and no network.
    """

    def derive_from_requires_python(
        self, *requires_python_strings: str | None
    ) -> tuple[int, int]:
        """Return the value derived from bundled distributions declaring these.

        None stands for a distribution that ships no Requires-Python at all,
        which is written as a METADATA file without that field.
        """
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            bundled_distributions_directory.mkdir()
            for index, requires_python in enumerate(requires_python_strings):
                write_dist_info_with_requires_python(
                    bundled_distributions_directory,
                    "thing{}".format(index), "1.0",
                    requires_python)
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()

            return derive_minimum_required_python_version_from_bundled_distributions(
                str(bundled_distributions_directory), str(vendor_directory))

    def test_strictest_lower_bound_wins(self) -> None:
        self.assertEqual(
            self.derive_from_requires_python(">=3.8", ">=3.11", ">=3.9"),
            (3, 11))

    def test_compatible_release_specifier_resolves_to_its_minor(self) -> None:
        self.assertEqual(
            self.derive_from_requires_python("~=3.11"), (3, 11))

    def test_a_distribution_without_requires_python_is_ignored(self) -> None:
        self.assertEqual(
            self.derive_from_requires_python(None, ">=3.10"), (3, 10))

    def test_no_distribution_declaring_anything_raises(self) -> None:
        with self.assertRaises(MinimumRequiredPythonVersionError):
            self.derive_from_requires_python(None, None)

    def test_an_empty_bundled_distributions_directory_raises(self) -> None:
        with self.assertRaises(MinimumRequiredPythonVersionError):
            self.derive_from_requires_python()

    def test_a_major_version_other_than_3_raises(self) -> None:
        # The data file and the sitecustomize.py gate only handle 3.x, so a
        # 4.x-only bundled distribution has to stop the run rather than be
        # mis-derived.
        with self.assertRaises(MinimumRequiredPythonVersionError):
            self.derive_from_requires_python(">=4.0")


class TestReadSupportedPythonVersions(TestCase):

    def test_read_supported_python_versions_from_the_json_array(self) -> None:
        # Deliberately not the versions the package ships: this exercises the
        # parser, so the input has to be fixed rather than whatever
        # supported_python_versions.json happens to list today.
        self.assertEqual(
            read_supported_python_versions('["3.4", "3.5", "4.0"]\n'),
            [(3, 4), (3, 5), (4, 0)])

    def test_read_malformed_json_raises(self) -> None:
        with self.assertRaises(ValueError):
            read_supported_python_versions('["3.10",\n')

    def test_read_a_non_array_document_raises(self) -> None:
        with self.assertRaises(ValueError):
            read_supported_python_versions('{"versions": ["3.10"]}')

    def test_read_an_empty_array_raises(self) -> None:
        with self.assertRaises(ValueError):
            read_supported_python_versions("[]")

    def test_read_a_non_python_version_entry_raises(self) -> None:
        for entry in ('"three.ten"', '"3"', "310", "null"):
            with self.subTest(entry=entry):
                with self.assertRaises(ValueError):
                    read_supported_python_versions("[{}]".format(entry))


class TestPythonVersionPair(TestCase):
    """The converter argparse uses for --lowest-resolvable-python-version."""

    def test_a_major_minor_string_becomes_a_pair(self) -> None:
        self.assertEqual(python_version_pair("3.10"), (3, 10))

    def test_a_double_digit_major_is_accepted(self) -> None:
        # Nothing here assumes a single-digit major, so the parser must not
        # either.
        self.assertEqual(python_version_pair("10.2"), (10, 2))

    def test_a_malformed_version_raises(self) -> None:
        for version_text in ("three.ten", "3", "3.", ".10", "3.10.1", ""):
            with self.subTest(version_text=version_text):
                with self.assertRaises(ValueError):
                    python_version_pair(version_text)
