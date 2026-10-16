# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for prune_supported_python_versions.py."""

from json import loads
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import TestCase

from shared import read_supported_python_versions
from prune_supported_python_versions import prune_supported_python_versions
from shared_test import (
    PRUNE_SCRIPT_PATH,
    SHIPPED_SUPPORTED_PYTHON_VERSIONS_FILE,
    run_bundled_distributions_script,
    write_dist_info_with_requires_python,
    write_supported_python_versions_file,
)


class TestPruningWhatPipCouldNotResolve(TestCase):
    """The case that used to be unfixable: a pin stops supporting 3.10.

    Before the bundled distributions were resolved with --python-version,
    pip refused to build them at all for such a version, so this script
    died in its preparation phase and could not drop the entry it exists
    to drop. The floor the preparation phase now reports is what makes it
    possible.
    """

    def test_a_version_pip_could_not_resolve_is_dropped(self) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            # Resolved for 3.11, so its metadata still admits 3.10. Only the
            # floor carries the fact that 3.10 could not be resolved.
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.10")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10", "3.11", "3.12"])

            completed = run_bundled_distributions_script(
                PRUNE_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path,
                lowest_resolvable_python_version="3.11")

            rewritten = supported_python_versions_path.read_text(
                encoding="utf-8")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("dropped 3.10", completed.stdout)
        self.assertEqual(loads(rewritten), ["3.11", "3.12"])


class TestPruneTheSupportedPythonVersionsText(TestCase):
    """Pruning, exercised against the versions the package actually ships.

    The input is read from packaging/builder/supported_python_versions.json
    rather than restated here, so that editing that file cannot leave these
    tests asserting against a list the package no longer ships.
    """

    def setUp(self) -> None:
        self.shipped_text = SHIPPED_SUPPORTED_PYTHON_VERSIONS_FILE.read_text(
            encoding="utf-8")
        self.shipped_versions = read_supported_python_versions(
            self.shipped_text)
        self.assertGreater(
            len(self.shipped_versions), 1,
            "these tests need at least two supported Python versions")

    def test_prune_drops_only_the_supported_python_versions_below_the_minimum_required_python_version(
        self
    ) -> None:
        second_lowest_version = sorted(self.shipped_versions)[1]

        pruned = read_supported_python_versions(
            prune_supported_python_versions(
                self.shipped_text, second_lowest_version))

        self.assertNotIn(min(self.shipped_versions), pruned)
        self.assertEqual(len(pruned), len(self.shipped_versions) - 1)
        self.assertEqual(min(pruned), second_lowest_version)

    def test_prune_output_matches_the_shipped_file_formatting(self) -> None:
        # The pruned text is written back over the file the Go builder
        # embeds, so a prune run must not reformat it into something a
        # reviewer sees as an unrelated change. Pruning at the lowest shipped
        # version drops nothing, so the output has to be the input verbatim.
        self.assertEqual(
            prune_supported_python_versions(
                self.shipped_text, min(self.shipped_versions)),
            self.shipped_text)

    def test_prune_below_every_supported_python_version_changes_nothing(
        self
    ) -> None:
        self.assertEqual(
            read_supported_python_versions(
                prune_supported_python_versions(self.shipped_text, (3, 0))),
            self.shipped_versions)

    def test_prune_that_would_empty_the_list_raises(self) -> None:
        with self.assertRaises(ValueError):
            prune_supported_python_versions(self.shipped_text, (9, 99))


class TestPruneScriptEndToEnd(TestCase):

    def test_prune_drops_the_supported_python_versions_below_the_minimum_required_python_version(
        self
    ) -> None:
        # The versions the package ships go in, and a vendored pyproject
        # demanding the second-lowest of them puts the minimum required
        # Python version there, so the prune must drop the lowest one and
        # keep every other. They are read from the shipped file rather than
        # restated here, so that editing that file cannot leave this test
        # asserting against a list the package no longer ships.
        shipped_text = SHIPPED_SUPPORTED_PYTHON_VERSIONS_FILE.read_text(
            encoding="utf-8")
        shipped_versions = sorted(
            read_supported_python_versions(shipped_text))
        self.assertGreater(
            len(shipped_versions), 1,
            "this test needs at least two supported Python versions")

        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory, "shipped_thing", "1.0",
                ">={}.{}".format(*shipped_versions[0]))
            vendor_directory = temporary_path / "vendor" / "high-requirement"
            vendor_directory.mkdir(parents=True)
            (vendor_directory / "pyproject.toml").write_text(
                "[project]\n"
                'name = "high-requirement"\n'
                'requires-python = ">={}.{}"\n'.format(*shipped_versions[1]),
                encoding="utf-8")
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            supported_python_versions_path.write_text(
                shipped_text, encoding="utf-8")

            completed = run_bundled_distributions_script(
                PRUNE_SCRIPT_PATH, bundled_distributions_directory,
                temporary_path / "vendor", supported_python_versions_path)
            self.assertEqual(completed.returncode, 0, completed.stderr)

            rewritten = supported_python_versions_path.read_text(
                encoding="utf-8")

        self.assertEqual(
            loads(rewritten),
            ["{}.{}".format(*version) for version in shipped_versions[1:]])

    def test_prune_writes_nothing_when_no_version_is_below(self) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.10")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.11", "3.12"])
            before = supported_python_versions_path.read_text(
                encoding="utf-8")

            completed = run_bundled_distributions_script(
                PRUNE_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

            after = supported_python_versions_path.read_text(encoding="utf-8")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("nothing to prune", completed.stdout)
        self.assertEqual(before, after)
