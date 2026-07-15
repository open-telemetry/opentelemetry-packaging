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

    An unsupported OTEL_EXPORTER_OTLP_PROTOCOL makes import_distro()
    self-deactivate at the protocol guard, before reading files or package
    metadata. os.environ and sys.path are patched so the deactivation cannot
    leak into the test process. Returns (module, stderr buffer).
    """
    buf = StringIO()
    env = {
        k: v for k, v in os.environ.items()
        if k not in ("OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_CONFIG_FILE")
    }
    # http/json is unsupported, so the guard deactivates without side effects.
    env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/json"
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

    def test_pyyaml_conflict_is_exempt_with_a_warning(self):
        # Debian 12's python3-yaml ships 6.0 while the bundle pins a newer
        # version; the application's version wins on sys.path and is expected
        # to work, so this must not deactivate instrumentation.
        self._check("PyYAML==6.0.3", installed_version="6.0")
        self.assertEqual({}, self.conflicts)
        output = self.stderr.getvalue()
        self.assertIn("WARN", output)
        self.assertIn("PyYAML", output)
        self.assertIn("continuing anyway", output)

    def test_jsonschema_conflict_is_exempt_with_a_warning(self):
        self._check("jsonschema==4.25.0", installed_version="4.17.3")
        self.assertEqual({}, self.conflicts)
        self.assertIn("continuing anyway", self.stderr.getvalue())

    def test_non_exempt_package_conflict_still_recorded(self):
        self._check("opentelemetry-sdk==1.43.0", installed_version="1.20.0")
        self.assertEqual(
            {"opentelemetry-sdk": {"version_required": "==1.43.0", "version_found": "1.20.0"}},
            self.conflicts,
        )


class ImportDistroTests(unittest.TestCase):
    """End-to-end tests: execute sitecustomize.py under controlled conditions."""

    OTHER_PYTHONPATH_ENTRY = "/opt/elsewhere"

    def setUp(self):
        # Mirror the installed layout: the site directory is <prefix>/glibc,
        # and the otel-config-check validator sits at <prefix>/.
        self.base_dir = tempfile.mkdtemp(prefix="otel-sitecustomize-test-")
        self.addCleanup(shutil.rmtree, self.base_dir, ignore_errors=True)
        self.site_dir = os.path.join(self.base_dir, "glibc")
        os.mkdir(self.site_dir)

    def _write_fake_validator(self, exit_code, message=""):
        path = os.path.join(self.base_dir, "otel-config-check")
        with open(path, "w") as f:
            f.write("#!/bin/sh\n")
            if message:
                f.write('echo "{}"\n'.format(message))
            f.write("exit {}\n".format(exit_code))
        os.chmod(path, 0o755)

    def _exec_sitecustomize(
        self,
        extra_env=None,
        all_dependencies=None,
        installed_version="1.0.0",
        installed_distributions=None,
        sys_path_entry=None,
    ):
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
                patch.object(sys, "path", list(sys.path) + [sys_path_entry or self.site_dir]), \
                patch("os.path.dirname", side_effect=fake_dirname), \
                patch("importlib.metadata.distributions", return_value=installed_distributions or []), \
                patch("importlib.metadata.distribution", return_value=distribution), \
                patch.dict(sys.modules, {
                    "opentelemetry": MagicMock(),
                    "opentelemetry.instrumentation": instrumentation_pkg,
                    "opentelemetry.instrumentation.auto_instrumentation": auto_instrumentation,
                }):
            _load_sitecustomize(buf)
            observed_env = dict(os.environ)
        return buf.getvalue(), auto_instrumentation, observed_env

    def _assert_activated(
        self, auto_instrumentation, observed_env, exporter="otlp_proto_http"
    ):
        auto_instrumentation.initialize.assert_called_once_with()
        self.assertIn(self.site_dir, observed_env["PYTHONPATH"])
        self.assertEqual(exporter, observed_env["OTEL_TRACES_EXPORTER"])
        self.assertEqual(exporter, observed_env["OTEL_METRICS_EXPORTER"])
        self.assertEqual(exporter, observed_env["OTEL_LOGS_EXPORTER"])

    def _assert_initialized(self, auto_instrumentation, observed_env):
        # Activation without asserting exporter selection: under OTEL_CONFIG_FILE
        # the configuration file drives the exporter and sitecustomize leaves
        # OTEL_*_EXPORTER untouched.
        auto_instrumentation.initialize.assert_called_once_with()
        self.assertIn(self.site_dir, observed_env["PYTHONPATH"])

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

    def test_activates_with_grpc_when_protocol_unset(self):
        # An unset protocol follows the OTel default of grpc, which this
        # package now bundles (over the pure-Python transport).
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_activated(
            auto_instrumentation, observed_env, exporter="otlp_proto_grpc"
        )
        self.assertEqual("", output)

    def test_activates_with_grpc_when_protocol_is_grpc(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_activated(
            auto_instrumentation, observed_env, exporter="otlp_proto_grpc"
        )
        self.assertEqual("", output)

    def test_deactivates_when_protocol_is_http_json(self):
        # The bundled pyproto exporter emits protobuf only; http/json must be
        # rejected until the exporter chain supports JSON encoding.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/json"},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("OTEL_EXPORTER_OTLP_PROTOCOL=http/json is not supported", output)
        self.assertIn("supports grpc and http/protobuf", output)

    def test_deactivates_when_dependencies_file_is_missing(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("cannot read all-dependencies.txt", output)

    def test_config_file_skips_protocol_guard_and_initializes(self):
        # With OTEL_CONFIG_FILE set, the SDK ignores the OTEL_* exporter
        # environment variables, so activation must proceed without
        # OTEL_EXPORTER_OTLP_PROTOCOL when the file validates.
        self._write_fake_validator(exit_code=0)
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_CONFIG_FILE": os.path.join(self.base_dir, "otel-config.yaml")},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_initialized(auto_instrumentation, observed_env)
        self.assertEqual("", output)

    def test_config_file_validation_failure_deactivates(self):
        self._write_fake_validator(
            exit_code=1, message="file_format 1.0 is required"
        )
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_CONFIG_FILE": os.path.join(self.base_dir, "otel-config.yaml")},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("is not usable", output)
        self.assertIn("file_format 1.0 is required", output)

    def test_config_file_without_validator_binary_proceeds(self):
        # The validator is an aid; its absence must not block activation.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_CONFIG_FILE": os.path.join(self.base_dir, "otel-config.yaml")},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_initialized(auto_instrumentation, observed_env)

    def test_config_file_validator_execution_failure_proceeds(self):
        # A validator that exists but cannot be executed makes subprocess.run
        # raise OSError; the guard must warn and proceed, not crash or
        # deactivate.
        validator = os.path.join(self.base_dir, "otel-config-check")
        with open(validator, "w") as f:
            f.write("not a program")
        os.chmod(validator, 0o644)
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_CONFIG_FILE": os.path.join(self.base_dir, "otel-config.yaml")},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_initialized(auto_instrumentation, observed_env)
        self.assertIn("cannot run otel-config-check", output)

    def test_deactivates_on_double_instrumentation(self):
        dist = MagicMock()
        dist.metadata = {"Name": "opentelemetry-sdk"}
        dist._path = "/app/site-packages/opentelemetry_sdk-1.20.0.dist-info"
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("already instrumented", output)
        self.assertIn("opentelemetry_sdk-1.20.0.dist-info", output)

    def test_unrelated_installed_distribution_does_not_deactivate(self):
        dist = MagicMock()
        dist.metadata = {"Name": "flask"}
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_activated(auto_instrumentation, observed_env)

    def test_trailing_slash_pythonpath_entry_does_not_crash(self):
        # The sys.path entry can differ textually from dirname(__file__)
        # (e.g. a trailing slash in the injected PYTHONPATH value); the module
        # must not raise ValueError out of the unguarded removal.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            sys_path_entry=self.site_dir + "/",
        )
        self._assert_activated(auto_instrumentation, observed_env)


class LoggingTests(unittest.TestCase):
    """The diagnostics channel must never touch stdout or raise."""

    def test_log_with_stderr_none_is_silent(self):
        module, _ = _load_benign()
        module.stderr = None
        module._log_warn("must not raise nor reach stdout")

    def test_log_with_broken_stderr_does_not_raise(self):
        module, _ = _load_benign()

        class Broken(object):
            def write(self, *_args):
                raise IOError("closed")

        module.stderr = Broken()
        module._log_warn("must not raise")


if __name__ == "__main__":
    unittest.main()
