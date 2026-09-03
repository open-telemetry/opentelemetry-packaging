# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for sync_minimum_python_version.py."""

from pathlib import Path
from subprocess import run
from sys import executable
from tempfile import TemporaryDirectory
from unittest import TestCase

from sync_minimum_python_version import (
    lowest_supported_major_minor_across_requires_python,
    read_gate_minor_from_sitecustomize,
    rewrite_gate_minor_in_sitecustomize,
)

_TOOL_PATH = str(Path(__file__).with_name("sync_minimum_python_version.py"))

_MINIMAL_SITECUSTOMIZE = (
    "# Require Python >= 3.10. This floor is the strictest.\n"
    "_MINIMUM_PYTHON_MINOR = 10  # sync-minimum-python-version: 3.x minor floor\n"
    "if version_info[0] != 3 or version_info[1] < _MINIMUM_PYTHON_MINOR:\n"
    "    pass\n"
)


def write_dist_info_with_requires_python(
        payload_directory, name, version, requires_python):
    """Create a <name>-<version>.dist-info/METADATA under payload_directory.

    This is the minimum importlib.metadata needs to enumerate a distribution
    and report its Requires-Python, so a payload can be assembled offline
    without installing anything.
    """
    dist_info_directory = payload_directory / "{}-{}.dist-info".format(
        name, version)
    dist_info_directory.mkdir(parents=True)
    (dist_info_directory / "METADATA").write_text(
        "Metadata-Version: 2.1\n"
        "Name: {}\n"
        "Version: {}\n"
        "Requires-Python: {}\n".format(name, version, requires_python),
        encoding="utf-8")


def write_sitecustomize_with_gate_minor(sitecustomize_path, gate_minor):
    """Write a minimal sitecustomize.py whose gate constant holds gate_minor."""
    sitecustomize_path.write_text(
        _MINIMAL_SITECUSTOMIZE.replace(
            "= 10  #", "= {}  #".format(gate_minor)).replace(
            ">= 3.10", ">= 3.{}".format(gate_minor)),
        encoding="utf-8")


def run_sync_minimum_python_version(
        mode, payload_directory, vendor_directory, sitecustomize_path):
    """Run the tool in --check or --write mode and return the completed run."""
    return run(
        [
            executable, _TOOL_PATH, mode,
            "--payload-dir", str(payload_directory),
            "--vendor-dir", str(vendor_directory),
            "--sitecustomize", str(sitecustomize_path),
        ],
        capture_output=True, text=True)


class TestDeriveFloorFromRequiresPython(TestCase):

    def test_strictest_lower_bound_wins(self):
        self.assertEqual(
            lowest_supported_major_minor_across_requires_python(
                [">=3.9", ">=3.10", ">=3.8,<4"]),
            (3, 10))

    def test_compatible_release_specifier_resolves_to_its_minor(self):
        self.assertEqual(
            lowest_supported_major_minor_across_requires_python(["~=3.11"]),
            (3, 11))

    def test_empty_and_none_specifiers_are_ignored(self):
        self.assertEqual(
            lowest_supported_major_minor_across_requires_python(
                [None, "", ">=3.12"]),
            (3, 12))

    def test_no_specifiers_yields_none(self):
        self.assertIsNone(
            lowest_supported_major_minor_across_requires_python([None, ""]))


class TestPayloadScopedEnumeration(TestCase):

    def test_floor_comes_only_from_the_payload_directory(self):
        # The payload declares a single distribution at >=3.7, so the floor is
        # 3.7 and a gate of 7 is in sync. The interpreter running this test has
        # packaging installed (>=3.9), plus pip and setuptools from the
        # throwaway venv the python-unit-tests target builds. An enumeration
        # that walked sys.path instead of --payload-dir would derive at least
        # 3.9 from those and report the gate as out of sync, so passing here is
        # what proves the enumeration is scoped to the payload.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            payload_directory = temporary_path / "payload"
            write_dist_info_with_requires_python(
                payload_directory, "shipped_thing", "1.0", ">=3.7")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            sitecustomize_path = temporary_path / "sitecustomize.py"
            write_sitecustomize_with_gate_minor(sitecustomize_path, 7)

            completed = run_sync_minimum_python_version(
                "--check", payload_directory, vendor_directory,
                sitecustomize_path)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("derived minimum Python: 3.7", completed.stdout)

    def test_strictest_payload_distribution_decides_the_floor(self):
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            payload_directory = temporary_path / "payload"
            write_dist_info_with_requires_python(
                payload_directory, "lenient_thing", "1.0", ">=3.9")
            write_dist_info_with_requires_python(
                payload_directory, "strict_thing", "2.0", ">=3.11")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            sitecustomize_path = temporary_path / "sitecustomize.py"
            write_sitecustomize_with_gate_minor(sitecustomize_path, 11)

            completed = run_sync_minimum_python_version(
                "--check", payload_directory, vendor_directory,
                sitecustomize_path)

        self.assertEqual(completed.returncode, 0, completed.stderr)
        self.assertIn("derived minimum Python: 3.11", completed.stdout)

    def test_missing_payload_directory_fails(self):
        # A mistyped or unbuilt payload path must be an error rather than a
        # derivation from the vendored pyproject files alone, which would
        # silently report a floor lower than the one the package ships.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            vendor_directory = temporary_path / "vendor" / "some-package"
            vendor_directory.mkdir(parents=True)
            (vendor_directory / "pyproject.toml").write_text(
                "[project]\n"
                'name = "some-package"\n'
                'requires-python = ">=3.10"\n',
                encoding="utf-8")
            sitecustomize_path = temporary_path / "sitecustomize.py"
            write_sitecustomize_with_gate_minor(sitecustomize_path, 10)

            completed = run_sync_minimum_python_version(
                "--check", temporary_path / "no-such-payload",
                temporary_path / "vendor", sitecustomize_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("no distributions found", completed.stderr)

    def test_empty_payload_directory_fails(self):
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            payload_directory = temporary_path / "payload"
            payload_directory.mkdir()
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            sitecustomize_path = temporary_path / "sitecustomize.py"
            write_sitecustomize_with_gate_minor(sitecustomize_path, 10)

            completed = run_sync_minimum_python_version(
                "--check", payload_directory, vendor_directory,
                sitecustomize_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("no distributions found", completed.stderr)


class TestVendorPyprojectParsing(TestCase):

    def test_check_reads_requires_python_from_vendor_pyproject(self):
        # The payload floor (3.8) is below the only vendored pyproject floor
        # (3.12), which must therefore drive --check to report the gate (3.10)
        # as out of sync. This proves the tool reads [project].requires-python
        # from pyproject.toml files found under --vendor-dir.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            payload_directory = temporary_path / "payload"
            write_dist_info_with_requires_python(
                payload_directory, "shipped_thing", "1.0", ">=3.8")
            vendor_directory = temporary_path / "vendor" / "some-package"
            vendor_directory.mkdir(parents=True)
            (vendor_directory / "pyproject.toml").write_text(
                "[project]\n"
                'name = "some-package"\n'
                'requires-python = ">=3.12"\n',
                encoding="utf-8")
            sitecustomize_path = temporary_path / "sitecustomize.py"
            sitecustomize_path.write_text(
                _MINIMAL_SITECUSTOMIZE, encoding="utf-8")

            completed = run_sync_minimum_python_version(
                "--check", payload_directory, temporary_path / "vendor",
                sitecustomize_path)

        self.assertNotEqual(completed.returncode, 0)
        self.assertIn("3.12", completed.stdout)


class TestGateConstantReadAndRewrite(TestCase):

    def test_read_gate_minor_from_marker_line(self):
        self.assertEqual(
            read_gate_minor_from_sitecustomize(_MINIMAL_SITECUSTOMIZE), 10)

    def test_read_gate_minor_without_marker_raises(self):
        with self.assertRaises(ValueError):
            read_gate_minor_from_sitecustomize("no marker here\n")

    def test_rewrite_updates_constant_and_human_readable_comment(self):
        rewritten = rewrite_gate_minor_in_sitecustomize(
            _MINIMAL_SITECUSTOMIZE, 12)
        self.assertIn(
            "_MINIMUM_PYTHON_MINOR = 12  # sync-minimum-python-version",
            rewritten)
        self.assertIn("# Require Python >= 3.12", rewritten)
        self.assertNotIn("3.10", rewritten)
        self.assertEqual(read_gate_minor_from_sitecustomize(rewritten), 12)

    def test_rewrite_requires_exactly_one_marker(self):
        with self.assertRaises(ValueError):
            rewrite_gate_minor_in_sitecustomize("no marker here\n", 12)


class TestCheckAndWriteEndToEnd(TestCase):

    def test_check_exits_zero_when_gate_matches_and_nonzero_when_not(self):
        # A payload whose strictest distribution declares >=3.11 fixes the
        # derived floor at 3.11, so a gate of 11 must pass and a gate of 12
        # must fail.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            payload_directory = temporary_path / "payload"
            write_dist_info_with_requires_python(
                payload_directory, "shipped_thing", "1.0", ">=3.11")
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()

            matching_sitecustomize = temporary_path / "matching.py"
            write_sitecustomize_with_gate_minor(matching_sitecustomize, 11)
            matching = run_sync_minimum_python_version(
                "--check", payload_directory, vendor_directory,
                matching_sitecustomize)
            self.assertEqual(matching.returncode, 0, matching.stderr)

            mismatching_sitecustomize = temporary_path / "mismatching.py"
            write_sitecustomize_with_gate_minor(mismatching_sitecustomize, 12)
            mismatching = run_sync_minimum_python_version(
                "--check", payload_directory, vendor_directory,
                mismatching_sitecustomize)

        self.assertNotEqual(mismatching.returncode, 0)

    def test_write_rewrites_marker_and_comment_to_derived_floor(self):
        # A vendored pyproject with a 3.13 floor above the payload's 3.10 puts
        # the derived floor at 3.13. --write must rewrite the temp
        # sitecustomize to that minor in both the constant and the
        # human-readable comment.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            payload_directory = temporary_path / "payload"
            write_dist_info_with_requires_python(
                payload_directory, "shipped_thing", "1.0", ">=3.10")
            vendor_directory = temporary_path / "vendor" / "high-floor"
            vendor_directory.mkdir(parents=True)
            (vendor_directory / "pyproject.toml").write_text(
                "[project]\n"
                'name = "high-floor"\n'
                'requires-python = ">=3.13"\n',
                encoding="utf-8")
            sitecustomize_path = temporary_path / "sitecustomize.py"
            sitecustomize_path.write_text(
                _MINIMAL_SITECUSTOMIZE, encoding="utf-8")

            completed = run_sync_minimum_python_version(
                "--write", payload_directory, temporary_path / "vendor",
                sitecustomize_path)
            self.assertEqual(completed.returncode, 0, completed.stderr)

            rewritten = sitecustomize_path.read_text(encoding="utf-8")
            self.assertIn(
                "_MINIMUM_PYTHON_MINOR = 13  # sync-minimum-python-version",
                rewritten)
            self.assertIn("# Require Python >= 3.13", rewritten)
