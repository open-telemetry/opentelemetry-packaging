# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for the code in shared.py that several scripts use."""

from pathlib import Path
from tempfile import TemporaryDirectory
from unittest import TestCase

from shared import (
    MinimumRequiredPythonVersionError,
    derive_minimum_required_python_version_from_bundled_distributions,
    is_resolution_failure,
    pip_target_platform_arguments,
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
                [str(bundled_distributions_directory)], str(vendor_directory))

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

    def test_every_bundled_distributions_directory_contributes(self) -> None:
        # One directory per target architecture, and a distribution can
        # resolve to a different version on each: the strictest bound wins
        # wherever it was found.
        with TemporaryDirectory() as temporary_directory:
            temporary_path = Path(temporary_directory)
            vendor_directory = temporary_path / "vendor"
            vendor_directory.mkdir()
            directories: list[str] = []
            for architecture, requires_python in (
                    ("amd64", ">=3.10"), ("arm64", ">=3.12")):
                bundled_distributions_directory = (
                    temporary_path / architecture)
                bundled_distributions_directory.mkdir()
                write_dist_info_with_requires_python(
                    bundled_distributions_directory, "thing", "1.0",
                    requires_python)
                directories.append(str(bundled_distributions_directory))

            self.assertEqual(
                derive_minimum_required_python_version_from_bundled_distributions(
                    directories, str(vendor_directory)),
                (3, 12))

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


class TestPipTargetPlatformArguments(TestCase):
    """The flags that keep the resolution off the host's platform.

    They have to stay in step with pipPlatformArgs and manylinuxPlatforms
    in packaging/builder/download.go, so the expectations here spell out
    what those two pass rather than deriving it.
    """

    def test_the_abi_and_implementation_pin_the_interpreter(self) -> None:
        arguments = pip_target_platform_arguments((3, 10), "amd64")
        self.assertIn("--only-binary=:all:", arguments)
        for flag, value in (
                ("--python-version", "3.10"),
                ("--implementation", "cp"),
                ("--abi", "cp310"),
                ("--abi", "abi3"),
                ("--abi", "none")):
            with self.subTest(flag=flag, value=value):
                self.assertIn(
                    (flag, value),
                    list(zip(arguments, arguments[1:])))

    def test_each_architecture_carries_its_own_manylinux_tags(self) -> None:
        for architecture, machine in (
                ("amd64", "x86_64"), ("arm64", "aarch64")):
            with self.subTest(architecture=architecture):
                arguments = pip_target_platform_arguments(
                    (3, 13), architecture)
                platforms = [
                    value for flag, value
                    in zip(arguments, arguments[1:])
                    if flag == "--platform"]
                self.assertEqual(
                    platforms,
                    ["manylinux2014_" + machine,
                     "manylinux_2_17_" + machine,
                     "manylinux_2_28_" + machine,
                     "manylinux1_" + machine])

    def test_an_unknown_architecture_raises(self) -> None:
        with self.assertRaises(ValueError):
            pip_target_platform_arguments((3, 13), "riscv64")


class TestIsResolutionFailure(TestCase):
    """What separates "this interpreter is out" from "pip had a bad day".

    The outputs below are the ones pip actually prints, abbreviated to the
    line that carries the verdict.
    """

    def test_an_unsatisfiable_requirement_is_a_resolution_failure(
        self
    ) -> None:
        self.assertTrue(is_resolution_failure(
            "ERROR: Could not find a version that satisfies the requirement "
            "rpds-py==0.30.0 (from versions: none)\n"
            "ERROR: No matching distribution found for rpds-py==0.30.0\n"))

    def test_conflicting_dependencies_are_a_resolution_failure(self) -> None:
        self.assertTrue(is_resolution_failure(
            "ERROR: Cannot install -r requirements.txt (line 1) and "
            "-r requirements.txt (line 2) because these package versions "
            "have conflicting dependencies.\n"))

    def test_a_requires_python_rejection_is_a_resolution_failure(
        self
    ) -> None:
        self.assertTrue(is_resolution_failure(
            "ERROR: Package 'thing' requires a different Python: "
            "Requires-Python >=3.11\n"))

    def test_a_network_outage_is_not_a_resolution_failure(self) -> None:
        # The case that must never prune an interpreter: pip exits non-zero,
        # but nothing it printed says the requirements cannot be satisfied.
        self.assertFalse(is_resolution_failure(
            "WARNING: Retrying (Retry(total=0, connect=None, read=None, "
            "redirect=None, status=None)) after connection broken by "
            "'NewConnectionError(...: Failed to establish a new "
            "connection: [Errno -3] Temporary failure in name "
            "resolution)': /simple/rpds-py/\n"
            "ERROR: Could not install packages due to an OSError\n"))

    def test_a_full_disk_is_not_a_resolution_failure(self) -> None:
        self.assertFalse(is_resolution_failure(
            "ERROR: Could not install packages due to an OSError: "
            "[Errno 28] No space left on device\n"))

    def test_an_interrupt_leaves_no_marker(self) -> None:
        self.assertFalse(is_resolution_failure(""))


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
