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


class TestVendorPyprojectParsing(TestCase):

    def test_check_reads_requires_python_from_vendor_pyproject(self):
        # A temp vendor dir whose only pyproject floor (3.12) exceeds the gate
        # (3.10) must drive --check to report an out-of-sync gate. This proves
        # the tool reads [project].requires-python from pyproject.toml files
        # found under --vendor-dir.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
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

            completed = run(
                [
                    executable, _TOOL_PATH, "--check",
                    "--vendor-dir", str(temporary_path / "vendor"),
                    "--sitecustomize", str(sitecustomize_path),
                ],
                capture_output=True, text=True)

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
        # The running interpreter's installed distributions plus an empty
        # vendor dir yield some 3.x floor. Whatever it is, a gate set to that
        # exact minor must pass --check, and a gate set one minor higher must
        # fail. We discover the floor from the passing run, then perturb it.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            empty_vendor = temporary_path / "vendor"
            empty_vendor.mkdir()

            probe_sitecustomize = temporary_path / "probe_sitecustomize.py"
            probe_sitecustomize.write_text(
                _MINIMAL_SITECUSTOMIZE, encoding="utf-8")
            probe = run(
                [
                    executable, _TOOL_PATH, "--check",
                    "--vendor-dir", str(empty_vendor),
                    "--sitecustomize", str(probe_sitecustomize),
                ],
                capture_output=True, text=True)
            derived_line = next(
                line for line in probe.stdout.splitlines()
                if line.startswith("derived minimum Python: 3."))
            derived_minor = int(derived_line.rsplit(".", 1)[1])

            matching_sitecustomize = temporary_path / "matching.py"
            matching_sitecustomize.write_text(
                _MINIMAL_SITECUSTOMIZE.replace(
                    "= 10  #", "= {}  #".format(derived_minor)).replace(
                    ">= 3.10", ">= 3.{}".format(derived_minor)),
                encoding="utf-8")
            matching = run(
                [
                    executable, _TOOL_PATH, "--check",
                    "--vendor-dir", str(empty_vendor),
                    "--sitecustomize", str(matching_sitecustomize),
                ],
                capture_output=True, text=True)
            self.assertEqual(matching.returncode, 0, matching.stderr)

            mismatching_sitecustomize = temporary_path / "mismatching.py"
            mismatching_sitecustomize.write_text(
                _MINIMAL_SITECUSTOMIZE.replace(
                    "= 10  #", "= {}  #".format(derived_minor + 1)).replace(
                    ">= 3.10", ">= 3.{}".format(derived_minor + 1)),
                encoding="utf-8")
            mismatching = run(
                [
                    executable, _TOOL_PATH, "--check",
                    "--vendor-dir", str(empty_vendor),
                    "--sitecustomize", str(mismatching_sitecustomize),
                ],
                capture_output=True, text=True)
            self.assertNotEqual(mismatching.returncode, 0)

    def test_write_rewrites_marker_and_comment_to_derived_floor(self):
        # A temp vendor pyproject with a 3.13 floor forces the derived floor to
        # at least 3.13. --write must rewrite the temp sitecustomize to that
        # minor in both the constant and the human-readable comment.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
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

            completed = run(
                [
                    executable, _TOOL_PATH, "--write",
                    "--vendor-dir", str(temporary_path / "vendor"),
                    "--sitecustomize", str(sitecustomize_path),
                ],
                capture_output=True, text=True)
            self.assertEqual(completed.returncode, 0, completed.stderr)

            rewritten = sitecustomize_path.read_text(encoding="utf-8")
            self.assertIn(
                "_MINIMUM_PYTHON_MINOR = 13  # sync-minimum-python-version",
                rewritten)
            self.assertIn("# Require Python >= 3.13", rewritten)
