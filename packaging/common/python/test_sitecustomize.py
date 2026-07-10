# Copyright The OpenTelemetry Authors
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for sitecustomize.py.

sitecustomize.py runs import_distro() at import time, so every test loads a
fresh module instance via importlib under controlled environment variables,
with sys.stderr captured and importlib.metadata patched as needed.

Run with `make python-unit-tests`.
"""

import importlib.metadata
import importlib.util
import os
import shutil
import sys
import tempfile
import unittest
from io import StringIO
from os.path import dirname as real_dirname
from unittest.mock import MagicMock, patch

TEST_DIR = os.path.dirname(os.path.abspath(__file__))
SITECUSTOMIZE_PATH = os.path.join(TEST_DIR, "sitecustomize.py")


def _load_sitecustomize(stderr_buffer):
    """Load a fresh sitecustomize module instance.

    sys.stderr is patched during the load so the module's own `stderr` binding
    (taken at import time) points at stderr_buffer; warnings emitted later by
    the loaded module land there too.
    """
    spec = importlib.util.spec_from_file_location("sitecustomize_under_test", SITECUSTOMIZE_PATH)
    module = importlib.util.module_from_spec(spec)
    with patch.object(sys, "stderr", stderr_buffer):
        spec.loader.exec_module(module)
    return module


def _load_benign():
    """Load sitecustomize with import_distro() short-circuiting harmlessly.

    Without OTEL_EXPORTER_OTLP_PROTOCOL, import_distro() self-deactivates at
    the protocol guard, before reading files or package metadata. os.environ
    and sys.path are patched so the deactivation cannot leak into the test
    process. Returns (module, stderr buffer).
    """
    buf = StringIO()
    env = {k: v for k, v in os.environ.items() if k != "OTEL_EXPORTER_OTLP_PROTOCOL"}
    with patch.dict(os.environ, env, clear=True), patch.object(sys, "path", list(sys.path)):
        module = _load_sitecustomize(buf)
    # Discard the protocol-guard warning the short circuit itself produced, so
    # tests observe only the output of the code they exercise.
    buf.seek(0)
    buf.truncate(0)
    return module, buf


class CheckDependencyVersionConflictTests(unittest.TestCase):
    """Fine-grained tests for _check_dependency_version_conflict."""

    def setUp(self):
        self.module, self.stderr = _load_benign()
        self.conflicts = {}

    def _check(self, req_string, installed_version=None, distribution_side_effect=None):
        distribution = MagicMock()
        distribution.version = installed_version
        with patch(
            "importlib.metadata.distribution",
            return_value=distribution,
            side_effect=distribution_side_effect,
        ) as mock_distribution:
            self.module._check_dependency_version_conflict(req_string, self.conflicts)
        return mock_distribution

    def test_no_conflict_when_installed_version_satisfies(self):
        self._check("foo==1.2.3", installed_version="1.2.3")
        self.assertEqual({}, self.conflicts)
        self.assertEqual("", self.stderr.getvalue())

    def test_no_conflict_for_requirement_without_specifier(self):
        self._check("foo", installed_version="0.0.1")
        self.assertEqual({}, self.conflicts)

    def test_conflict_when_installed_version_outside_specifier(self):
        self._check("foo==2.0.0", installed_version="1.0.0")
        self.assertEqual(
            {"foo": {"version_required": "==2.0.0", "version_found": "1.0.0"}},
            self.conflicts,
        )

    def test_missing_package_is_recorded_as_conflict(self):
        self._check(
            "foo==1.0.0",
            distribution_side_effect=importlib.metadata.PackageNotFoundError("foo"),
        )
        self.assertEqual({"foo": {"error": "required package not found"}}, self.conflicts)

    def test_pip_is_never_checked(self):
        mock_distribution = self._check("pip==1.0.0", installed_version="99.0.0")
        self.assertEqual({}, self.conflicts)
        mock_distribution.assert_not_called()

    def test_requirement_with_false_marker_is_never_checked(self):
        mock_distribution = self._check(
            'foo==1.0.0; python_version < "3"', installed_version="99.0.0"
        )
        self.assertEqual({}, self.conflicts)
        mock_distribution.assert_not_called()

    def test_unparsable_requirement_is_skipped_with_a_warning(self):
        mock_distribution = self._check("===bogus===", installed_version="1.0.0")
        self.assertEqual({}, self.conflicts)
        mock_distribution.assert_not_called()
        output = self.stderr.getvalue()
        self.assertIn("WARN", output)
        self.assertIn('cannot parse requirement "===bogus==="', output)

    def test_unparsable_installed_version_is_skipped_with_a_warning(self):
        # Linux distros patch package versions into strings that are not
        # valid PEP 440 (e.g. Debian's dfsg suffixes).
        self._check("foo==1.0.0", installed_version="1.0-dfsg-1")
        self.assertEqual({}, self.conflicts)
        output = self.stderr.getvalue()
        self.assertIn("WARN", output)
        self.assertIn('cannot parse the installed version "1.0-dfsg-1" of package "foo"', output)

    def test_missing_installed_version_metadata_is_skipped_with_a_warning(self):
        # importlib.metadata returns None for distributions without version
        # metadata; Version(None) raises TypeError, not InvalidVersion.
        self._check("foo==1.0.0", installed_version=None)
        self.assertEqual({}, self.conflicts)
        output = self.stderr.getvalue()
        self.assertIn("WARN", output)
        self.assertIn("cannot parse the installed version", output)

    def test_conflicts_accumulate_across_calls(self):
        self._check("===bogus===")
        self._check("foo==2.0.0", installed_version="1.0.0")
        self.assertEqual(
            {"foo": {"version_required": "==2.0.0", "version_found": "1.0.0"}},
            self.conflicts,
        )


class ImportDistroTests(unittest.TestCase):
    """End-to-end tests: execute sitecustomize.py under controlled conditions."""

    OTHER_PYTHONPATH_ENTRY = "/opt/elsewhere"

    def setUp(self):
        self.site_dir = tempfile.mkdtemp(prefix="otel-sitecustomize-test-")
        self.addCleanup(shutil.rmtree, self.site_dir, ignore_errors=True)

    def _exec_sitecustomize(self, extra_env=None, all_dependencies=None, installed_version="1.0.0"):
        """Execute sitecustomize.py end to end.

        The module's own directory is redirected to self.site_dir (a temp dir
        acting as the bundled site directory), the OpenTelemetry
        auto-instrumentation entry point is replaced with a mock, and the host
        is hidden: no installed distributions, and every requirement resolves
        to installed_version.

        Returns (stderr output, auto_instrumentation mock, environment
        observed right after the run).
        """
        if all_dependencies is not None:
            with open(os.path.join(self.site_dir, "all-dependencies.txt"), "w") as f:
                f.write(all_dependencies)

        def fake_dirname(p):
            if p == SITECUSTOMIZE_PATH:
                return self.site_dir
            return real_dirname(p)

        auto_instrumentation = MagicMock()
        instrumentation_pkg = MagicMock()
        instrumentation_pkg.auto_instrumentation = auto_instrumentation

        distribution = MagicMock()
        distribution.version = installed_version

        env = {"PYTHONPATH": self.site_dir + ":" + self.OTHER_PYTHONPATH_ENTRY}
        env.update(extra_env or {})

        buf = StringIO()
        with patch.dict(os.environ, env, clear=True), \
                patch.object(sys, "path", list(sys.path) + [self.site_dir]), \
                patch("os.path.dirname", side_effect=fake_dirname), \
                patch("importlib.metadata.distributions", return_value=[]), \
                patch("importlib.metadata.distribution", return_value=distribution), \
                patch.dict(sys.modules, {
                    "opentelemetry": MagicMock(),
                    "opentelemetry.instrumentation": instrumentation_pkg,
                    "opentelemetry.instrumentation.auto_instrumentation": auto_instrumentation,
                }):
            _load_sitecustomize(buf)
            observed_env = dict(os.environ)
        return buf.getvalue(), auto_instrumentation, observed_env

    def _assert_activated(self, auto_instrumentation, observed_env):
        auto_instrumentation.initialize.assert_called_once_with()
        self.assertIn(self.site_dir, observed_env["PYTHONPATH"])
        self.assertEqual("otlp_pyproto_http", observed_env["OTEL_TRACES_EXPORTER"])
        self.assertEqual("otlp_pyproto_http", observed_env["OTEL_METRICS_EXPORTER"])
        self.assertEqual("otlp_pyproto_http", observed_env["OTEL_LOGS_EXPORTER"])

    def _assert_deactivated(self, auto_instrumentation, observed_env):
        auto_instrumentation.initialize.assert_not_called()
        self.assertEqual(self.OTHER_PYTHONPATH_ENTRY, observed_env["PYTHONPATH"])
        self.assertEqual("", observed_env["PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX"])

    def test_initializes_when_all_dependencies_match(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_activated(auto_instrumentation, observed_env)
        self.assertEqual("", output)

    def test_survives_unparsable_requirement_line(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="===bogus===\nfoo==1.0.0\n",
        )
        self._assert_activated(auto_instrumentation, observed_env)
        self.assertIn('cannot parse requirement "===bogus==="', output)

    def test_survives_unparsable_installed_version(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_version="1.0-dfsg-1",
        )
        self._assert_activated(auto_instrumentation, observed_env)
        self.assertIn('cannot parse the installed version "1.0-dfsg-1"', output)

    def test_deactivates_on_version_conflict(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==2.0.0\n",
            installed_version="1.0.0",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("dependency conflicts", output)

    def test_deactivates_when_protocol_is_not_set(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("OTEL_EXPORTER_OTLP_PROTOCOL is not set", output)

    def test_deactivates_when_protocol_is_grpc(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("OTEL_EXPORTER_OTLP_PROTOCOL=grpc is not supported", output)

    def test_deactivates_when_dependencies_file_is_missing(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("cannot read all-dependencies.txt", output)


if __name__ == "__main__":
    unittest.main()
