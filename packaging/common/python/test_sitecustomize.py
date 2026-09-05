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
import logging
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


def _load_benign(extra_env=None):
    """Load sitecustomize with import_distro() short-circuiting harmlessly.

    An unsupported OTEL_EXPORTER_OTLP_PROTOCOL makes import_distro()
    self-deactivate at the protocol guard, before reading files or package
    metadata. os.environ and sys.path are patched so the deactivation cannot
    leak into the test process. OTEL_INJECTOR_LOG_LEVEL is dropped from the
    inherited environment, so the diagnostics level is decided by extra_env
    alone and not by the shell running the suite. Returns (module, stderr
    buffer).
    """
    buf = StringIO()
    env = {
        k: v for k, v in os.environ.items()
        if k not in ("OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_CONFIG_FILE", "OTEL_INJECTOR_LOG_LEVEL")
    }
    # http/json is unsupported, so the guard deactivates without side effects.
    env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/json"
    env.update(extra_env or {})
    with patch.dict(os.environ, env, clear=True), patch.object(sys, "path", list(sys.path)):
        module = _load_sitecustomize(buf)
    # Discard the protocol-guard warning the short circuit itself produced, so
    # tests observe only the output of the code they exercise.
    buf.seek(0)
    buf.truncate(0)
    return module, buf


class NormalizedPackageNameTests(unittest.TestCase):
    """The PEP 503 rule: runs of -, _ and . collapse to one -, then lowercase."""

    def setUp(self):
        self.module, self.stderr = _load_benign()

    def test_canonical_name_is_unchanged(self):
        self.assertEqual(
            "opentelemetry-sdk", self.module._normalized_package_name("opentelemetry-sdk"))

    def test_separators_become_hyphens(self):
        for spelling in ("opentelemetry_sdk", "opentelemetry.sdk", "opentelemetry-sdk"):
            with self.subTest(spelling=spelling):
                self.assertEqual(
                    "opentelemetry-sdk", self.module._normalized_package_name(spelling))

    def test_runs_of_separators_collapse_to_one(self):
        self.assertEqual(
            "opentelemetry-sdk", self.module._normalized_package_name("opentelemetry_-.sdk"))

    def test_case_is_folded(self):
        self.assertEqual("pyyaml", self.module._normalized_package_name("PyYAML"))

    def test_real_world_names_normalize_as_the_specification_says(self):
        # Every one of these is a real PyPI distribution whose declared Name
        # differs from its normalized form.
        expected = {
            "PyYAML": "pyyaml",
            "typing_extensions": "typing-extensions",
            "ruamel.yaml": "ruamel-yaml",
            "zope.interface": "zope-interface",
            "backports.tarfile": "backports-tarfile",
        }
        for declared, normalized in expected.items():
            with self.subTest(declared=declared):
                self.assertEqual(normalized, self.module._normalized_package_name(declared))

    def test_distinct_names_stay_distinct(self):
        self.assertNotEqual(
            self.module._normalized_package_name("opentelemetry-sdk"),
            self.module._normalized_package_name("opentelemetry-sdk-extras"))


class PackageListCanonicalFormTests(unittest.TestCase):
    """Both hardcoded lists are searched with a normalized name.

    Only the incoming name is normalized, which is the cheap direction: it
    happens once per candidate instead of once per list entry per candidate.
    That makes it an invariant that the lists themselves are written in
    normalized form, because an entry that is not can never be matched.
    """

    def setUp(self):
        self.module, self.stderr = _load_benign()

    def test_double_instrumentation_list_is_written_normalized(self):
        for name in self.module.double_instrumentation_check_packages:
            with self.subTest(name=name):
                self.assertEqual(name, self.module._normalized_package_name(name))

    def test_version_conflict_exempt_list_is_written_normalized(self):
        for name in self.module.version_conflict_exempt_packages:
            with self.subTest(name=name):
                self.assertEqual(name, self.module._normalized_package_name(name))


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

    def test_exemption_holds_whatever_case_the_requirement_uses(self):
        # The exempt list is matched on the normalized name, so every casing of
        # PyYAML is exempt. Neither exempt entry contains a separator, so the
        # separator half of the rule has nothing to do at this call site; it is
        # matched here the same way as in the double-instrumentation guard so
        # the two cannot drift apart again.
        for spelling in ("PyYAML", "pyyaml", "PYYAML", "PyYaml"):
            with self.subTest(spelling=spelling):
                self.conflicts = {}
                self.stderr.truncate(0)
                self.stderr.seek(0)
                self._check(spelling + "==6.0.3", installed_version="6.0")
                self.assertEqual({}, self.conflicts)
                self.assertIn("continuing anyway", self.stderr.getvalue())

    def test_a_separator_spelling_is_a_different_project_and_is_not_exempt(self):
        # PEP 503 collapses a run of separators to one hyphen, it does not
        # delete it, so py-yaml is not PyYAML and must not inherit its exemption.
        self._check("py_yaml==6.0.3", installed_version="6.0")
        self.assertEqual(
            {"py_yaml": {"version_required": "==6.0.3", "version_found": "6.0"}},
            self.conflicts,
        )

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
        initialize_side_effect=None,
    ):
        """Execute sitecustomize.py end to end.

        The module's own directory is redirected to self.site_dir (a temp dir
        acting as the bundled site directory), the OpenTelemetry
        auto-instrumentation entry point is replaced with a mock, and the host
        is hidden: no installed distributions, and every requirement resolves
        to installed_version.

        initialize_side_effect makes the mocked initialize() raise, which is
        how the SDK reports a failure it cannot recover from.

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
        auto_instrumentation.initialize.side_effect = initialize_side_effect
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
        auto_instrumentation.initialize.assert_called_once_with(swallow_exceptions=False)
        self.assertIn(self.site_dir, observed_env["PYTHONPATH"])
        self.assertEqual(exporter, observed_env["OTEL_TRACES_EXPORTER"])
        self.assertEqual(exporter, observed_env["OTEL_METRICS_EXPORTER"])
        self.assertEqual(exporter, observed_env["OTEL_LOGS_EXPORTER"])

    def _assert_initialized(self, auto_instrumentation, observed_env):
        # Activation without asserting exporter selection: under OTEL_CONFIG_FILE
        # the configuration file drives the exporter and sitecustomize leaves
        # OTEL_*_EXPORTER untouched.
        auto_instrumentation.initialize.assert_called_once_with(swallow_exceptions=False)
        self.assertIn(self.site_dir, observed_env["PYTHONPATH"])

    def _assert_deactivated_environment(self, observed_env):
        # What a deactivation must leave behind, whichever guard performed it:
        # the site gone from PYTHONPATH and the injector's agent path cleared,
        # so no child process retries the activation.
        self.assertEqual(self.OTHER_PYTHONPATH_ENTRY, observed_env["PYTHONPATH"])
        self.assertEqual("", observed_env["PYTHON_AUTO_INSTRUMENTATION_AGENT_PATH_PREFIX"])

    def _assert_deactivated(self, auto_instrumentation, observed_env):
        auto_instrumentation.initialize.assert_not_called()
        self._assert_deactivated_environment(observed_env)

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

    def test_deactivates_when_initialize_raises(self):
        # Initialization is the one stage this module does not perform itself,
        # so the handler around initialize() is what turns a failure inside the
        # SDK into a deactivation. A configuration file that otel-config-check
        # accepts and the file configurator then rejects arrives here: the
        # validator only checks the YAML and file_format, the SDK validates the
        # whole document against its schema.
        self._write_fake_validator(exit_code=0)
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_CONFIG_FILE": os.path.join(self.base_dir, "otel-config.yaml")},
            all_dependencies="foo==1.0.0\n",
            initialize_side_effect=ValueError("'file_format' is a required property"),
        )
        self._assert_deactivated_environment(observed_env)
        self.assertIn("error when importing/initializing", output)
        self.assertIn("ValueError: 'file_format' is a required property", output)

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

    def test_deactivates_whatever_spelling_the_declared_name_uses(self):
        # The Name a distribution declares is reported verbatim, and
        # non-canonical spellings are ordinary on PyPI, so every spelling of a
        # listed package has to be recognised.
        for spelling in (
            "opentelemetry_sdk",
            "OpenTelemetry-SDK",
            "OpenTelemetry_SDK",
            "opentelemetry.sdk",
            "OPENTELEMETRY-SDK",
            "opentelemetry--sdk",
        ):
            with self.subTest(declared_name=spelling):
                dist = MagicMock()
                dist.metadata = {"Name": spelling}
                dist._path = "/app/site-packages/opentelemetry_sdk-1.20.0.dist-info"
                output, auto_instrumentation, observed_env = self._exec_sitecustomize(
                    extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
                    all_dependencies="foo==1.0.0\n",
                    installed_distributions=[dist],
                )
                self._assert_deactivated(auto_instrumentation, observed_env)
                self.assertIn("already instrumented", output)

    def test_a_name_that_merely_starts_the_same_does_not_deactivate(self):
        # Normalizing must not turn the membership test into a prefix match.
        dist = MagicMock()
        dist.metadata = {"Name": "opentelemetry_sdk_extras"}
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_activated(auto_instrumentation, observed_env)

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
    """The contract of the diagnostics channel.

    Every line names the agent and the severity of the record, the severity
    decides whether the line is emitted at all, and the channel never touches
    stdout nor raises.
    """

    PREFIX = "[opentelemetry-python-autoinstrumentation]"

    def test_warning_names_the_agent_and_the_severity(self):
        module, buf = _load_benign()
        module._log_warn("a diagnostic")
        output = buf.getvalue()
        self.assertIn(self.PREFIX, output)
        self.assertIn("WARN", output)
        self.assertIn("a diagnostic", output)

    def test_warning_is_emitted_whatever_the_level(self):
        # No value of OTEL_INJECTOR_LOG_LEVEL silences a warning: it is the
        # only report an operator gets when the agent deactivates.
        module, buf = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": "debug"})
        module._log_warn("a diagnostic")
        self.assertIn("a diagnostic", buf.getvalue())

    def test_debug_names_the_agent_and_the_severity(self):
        module, buf = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": "debug"})
        module._log_debug("a trace")
        output = buf.getvalue()
        self.assertIn(self.PREFIX, output)
        self.assertIn("DEBUG", output)
        self.assertIn("a trace", output)

    def test_debug_is_suppressed_when_the_level_is_unset(self):
        module, buf = _load_benign()
        module._log_debug("a trace")
        self.assertEqual("", buf.getvalue())

    def test_suppressed_debug_record_does_not_build_the_logger(self):
        # The guard in _log_debug is what keeps a process running with debug
        # off from importing logging at all, which the injector cares about
        # because it prepends this file to every Python process on the host.
        module, _ = _load_benign()
        module._logger = None
        module._log_debug("a trace")
        self.assertIsNone(module._logger)

    def test_log_with_stderr_none_is_silent(self):
        # The daemon and pythonw case: sys.stderr is None at interpreter
        # startup, so the module's own binding is None too. Nothing may be
        # written and nothing may raise.
        module, buf = _load_benign()
        module.stderr = None
        # The load already emitted the protocol-guard warning, so the handler
        # exists and holds the buffer. Drop it to rebuild it from the None.
        module._logger = None
        with patch.object(sys, "stderr", None):
            module._log_warn("must not raise nor reach stdout")
        self.assertEqual("", buf.getvalue())

    def test_log_with_broken_stderr_does_not_raise(self):
        module, buf = _load_benign()

        class Broken(object):
            def write(self, *_args):
                raise IOError("closed")

        module.stderr = Broken()
        module._logger = None
        fallback = StringIO()
        with patch.object(sys, "stderr", fallback):
            module._log_warn("must not raise")
        # A write that fails must be dropped without a word: the logging
        # module's own error path would report it on sys.stderr instead.
        self.assertEqual("", fallback.getvalue())
        self.assertEqual("", buf.getvalue())


class ApplicationLoggingTests(unittest.TestCase):
    """sitecustomize.py must leave the application's logging untouched.

    It runs during site import, before the application's first line, so any
    logging state it changed would be inherited by the application: the root
    logger's level and handlers, the registry that dictConfig walks when it
    disables existing loggers, and the global disable threshold.
    """

    def setUp(self):
        root = logging.getLogger()
        self.before = (
            set(logging.Logger.manager.loggerDict),
            list(root.handlers),
            root.level,
            logging.Logger.manager.disable,
        )

    def _assert_logging_state_untouched(self):
        root = logging.getLogger()
        self.assertEqual(
            self.before,
            (
                set(logging.Logger.manager.loggerDict),
                list(root.handlers),
                root.level,
                logging.Logger.manager.disable,
            ),
        )

    def test_running_the_module_touches_no_logging_state(self):
        # The load emits at both severities: DEBUG for the entry trace and
        # WARN from the protocol guard that short-circuits it.
        _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": "debug"})
        self._assert_logging_state_untouched()

    def test_emitting_after_the_load_touches_no_logging_state(self):
        module, _ = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": "debug"})
        module._log_debug("a trace")
        module._log_warn("a diagnostic")
        self._assert_logging_state_untouched()


if __name__ == "__main__":
    unittest.main()
