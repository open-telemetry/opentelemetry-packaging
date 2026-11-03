# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for check_supported_python_versions.py."""

from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import TestCase

from shared_test import (
    CHECK_SCRIPT_PATH,
    run_bundled_distributions_script,
    write_dist_info_with_requires_python,
    write_sitecustomize_file,
    write_supported_python_versions_file,
)


class TestTheLowestResolvablePythonVersionFloor(TestCase):
    """What the preparation phase found pip could resolve raises the minimum.

    A pin that stops supporting an interpreter is invisible to the metadata
    in the bundled distributions, because those can only be what did
    resolve. The preparation phase reports the lowest supported Python
    version it could resolve for, and these cases drive that value in
    directly.
    """

    def test_a_floor_above_the_metadata_minimum_fails_the_check(self) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            # The bundled distribution says 3.10 is fine, which is what
            # the metadata of whatever resolved always says. Only the
            # floor knows 3.10 could not be resolved at all.
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
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path,
                lowest_resolvable_python_version="3.11")

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn(
            "minimum required Python version: 3.11", completed.stdout)
        self.assertIn("lists 3.10 below", completed.stderr)
        self.assertIn(
            "prune_supported_python_versions.py", completed.stderr)

    def test_a_floor_below_the_metadata_minimum_changes_nothing(self) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.11")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.11", "3.12"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path,
                lowest_resolvable_python_version="3.11")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn(
            "minimum required Python version: 3.11", completed.stdout)
        self.assertIn("in sync", completed.stdout)


class TestBundledDistributionsScopedEnumeration(TestCase):

    def test_minimum_required_python_version_comes_only_from_the_bundled_distributions_directory(
        self
    ) -> None:
        # The bundled distributions hold a single one at >=3.7, so the
        # minimum required Python version is 3.7. The interpreter running
        # this test has packaging installed (>=3.9), plus pip and
        # setuptools from the throwaway venv the python-unit-tests target
        # builds. An enumeration that walked sys.path instead of
        # --bundled-distributions-dir would derive at least 3.9 from those,
        # so seeing 3.7 reported is what proves the enumeration is scoped
        # to the bundled distributions.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.7")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.7", "3.8"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("minimum required Python version: 3.7", completed.stdout)

    def test_strictest_bundled_distribution_decides_the_minimum_required_python_version(
        self
    ) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "lenient_thing", "1.0", ">=3.9")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "strict_thing", "2.0", ">=3.11")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.11", "3.12"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn(
            "minimum required Python version: 3.11", completed.stdout)

    def test_missing_bundled_distributions_directory_fails(self) -> None:
        # A mistyped or unbuilt bundled distributions path must be an
        # error rather than a derivation from the vendored pyproject files
        # alone, which would silently report a minimum lower than the one
        # the package ships.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            vendor_directory = temporary_path / "vendor" / "some-package"
            vendor_directory.mkdir(parents=True)
            (vendor_directory / "pyproject.toml").write_text(
                "[project]\n"
                'name = "some-package"\n'
                'requires-python = ">=3.10"\n',
                encoding="utf-8")
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH,
                temporary_path / "no-such-bundled-distributions",
                temporary_path / "vendor", supported_python_versions_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("no bundled distributions found", completed.stderr)

    def test_empty_bundled_distributions_directory_fails(self) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            bundled_distributions_directory.mkdir()
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("no bundled distributions found", completed.stderr)


class TestVendorPyprojectParsing(TestCase):

    def test_check_reads_requires_python_from_vendor_pyproject(self) -> None:
        # The bundled distributions minimum (3.8) is below the only
        # vendored pyproject minimum (3.12), which must therefore drive
        # the check to reject a list that still starts at 3.10. This
        # proves the script reads [project].requires-python from
        # pyproject.toml files under --vendor-dir.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.8")
            vendor_directory = temporary_path / "vendor" / "some-package"
            vendor_directory.mkdir(parents=True)
            (vendor_directory / "pyproject.toml").write_text(
                "[project]\n"
                'name = "some-package"\n'
                'requires-python = ">=3.12"\n',
                encoding="utf-8")
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10", "3.11", "3.12"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                temporary_path / "vendor", supported_python_versions_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("3.12", completed.stdout)


class TestCheckOutcome(TestCase):

    def test_check_passes_when_every_supported_python_version_meets_the_minimum_required_python_version(
        self
    ) -> None:
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.11")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.11", "3.12"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("are in sync", completed.stdout)

    def test_check_fails_when_a_supported_python_version_is_below_the_minimum_required_python_version(
        self
    ) -> None:
        # This is the regression the check exists for: a dependency bump
        # raises the minimum to 3.11 while the builder still tries to resolve
        # the bundled distributions for 3.10, which pip cannot do.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.11")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10", "3.11", "3.12"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("3.10", completed.stderr)
        self.assertIn(
            "prune_supported_python_versions.py", completed.stderr)

    def test_check_leaves_the_supported_python_versions_file_untouched(
        self
    ) -> None:
        # The check must never write, however stale the list is. Only
        # prune_supported_python_versions.py rewrites the file.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.12")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10", "3.11", "3.12"])
            before = supported_python_versions_path.read_text(
                encoding="utf-8")

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path)

            after = supported_python_versions_path.read_text(encoding="utf-8")

        self.assertNotEqual(completed.returncode, 0)
        self.assertEqual(before, after)

    def test_check_passes_with_a_note_when_the_minimum_required_python_version_is_below_every_supported_python_version(
        self
    ) -> None:
        # Shipping fewer interpreters than the dependencies permit is a
        # deliberate choice, because wheel availability rather than
        # Requires-Python governs the top of the list, so this reports rather
        # than fails.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            bundled_distributions_directory = (
                temporary_path / "bundled-distributions")
            write_dist_info_with_requires_python(
                bundled_distributions_directory,
                "shipped_thing", "1.0", ">=3.8")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            supported_python_versions_path = (
                temporary_path / "supported_python_versions.json")
            write_supported_python_versions_file(
                supported_python_versions_path, ["3.10", "3.11"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path,
                # What every real run passes: the preparation phase always
                # reports the version it resolved for, and that version is
                # always one of the supported ones. A note compared against
                # the raised minimum rather than against what the metadata
                # declares could therefore never be printed here.
                lowest_resolvable_python_version="3.10")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("would admit 3.8", completed.stdout)

    def test_the_note_is_absent_when_the_metadata_permits_nothing_older(
        self
    ) -> None:
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
                supported_python_versions_path, ["3.10", "3.11"])

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory,
                supported_python_versions_path,
                lowest_resolvable_python_version="3.10")

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertNotIn("would admit", completed.stdout)


class TestTheCommittedSitecustomizeGate(TestCase):
    """The committed gate has to list what the JSON lists.

    The builder overwrites that line in the copy it stages, so the shipped
    package is right either way. The committed copy still matters: the
    sitecustomize.py unit tests read it to decide which interpreters the
    gate must admit, so drift there goes unnoticed while the tests assert
    against interpreters the package may no longer ship.
    """

    def run_check_with_a_gate_listing(
        self, gate_versions: list[str], json_versions: list[str]
    ):
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
                supported_python_versions_path, json_versions)
            sitecustomize_path = temporary_path / "sitecustomize.py"
            write_sitecustomize_file(sitecustomize_path, gate_versions)

            return run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory, supported_python_versions_path,
                sitecustomize_path=sitecustomize_path)

    def test_a_gate_naming_a_dropped_interpreter_fails_the_check(
        self
    ) -> None:
        # What a prune run that wrote only the JSON used to leave behind.
        completed = self.run_check_with_a_gate_listing(
            ["3.10", "3.11", "3.12"], ["3.11", "3.12"])

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("3.10, 3.11, 3.12", completed.stderr)
        self.assertIn("sitecustomize.py", completed.stderr)

    def test_a_gate_missing_an_added_interpreter_fails_the_check(
        self
    ) -> None:
        completed = self.run_check_with_a_gate_listing(
            ["3.11"], ["3.11", "3.12"])

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("sitecustomize.py", completed.stderr)

    def test_a_gate_listing_the_same_versions_in_another_order_fails(
        self
    ) -> None:
        # The builder writes the JSON's order, so the two can be compared as
        # they stand; a reordered copy is a copy that was edited by hand.
        completed = self.run_check_with_a_gate_listing(
            ["3.12", "3.11"], ["3.11", "3.12"])

        self.assertNotEqual(completed.returncode, 0)

    def test_a_matching_gate_passes(self) -> None:
        completed = self.run_check_with_a_gate_listing(
            ["3.11", "3.12"], ["3.11", "3.12"])

        self.assertEqual(completed.returncode, 0, completed.stderr)

    def test_a_sitecustomize_without_the_marker_fails_the_check(self) -> None:
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
                supported_python_versions_path, ["3.11"])
            sitecustomize_path = temporary_path / "sitecustomize.py"
            sitecustomize_path.write_text(
                "supported_python_versions = []\n", encoding="utf-8")

            completed = run_bundled_distributions_script(
                CHECK_SCRIPT_PATH, bundled_distributions_directory,
                vendor_directory, supported_python_versions_path,
                sitecustomize_path=sitecustomize_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("marker", completed.stderr)
