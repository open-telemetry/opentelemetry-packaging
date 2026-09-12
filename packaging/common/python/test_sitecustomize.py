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

    def test_the_unparsable_requirement_warning_is_one_record(self):
        # packaging renders InvalidRequirement across three lines, with a caret
        # pointer under the offending text. This is the real message, not a
        # stand-in, so it fails if packaging changes shape.
        self._check("===bogus===", installed_version="1.0.0")
        output = self.stderr.getvalue()
        self.assertEqual(1, len(output.splitlines()))
        self.assertIn("Expected package name at the start of dependency specifier", output)

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

    def test_unreadable_installed_version_is_skipped_with_a_warning(self):
        # distribution() is lazy and does not touch METADATA, so a file that is
        # not valid UTF-8 surfaces here, at .version. The parse handler used to
        # quote that attribute in its own message, reading it a second time,
        # which raised again from inside the handler and escaped the function
        # that exists to skip exactly this. A real object is used because a
        # MagicMock cannot raise on attribute access.
        class DistributionWithUndecodableMetadata(object):
            @property
            def version(self):
                raise UnicodeDecodeError("utf-8", b"\xe9", 0, 1, "invalid continuation byte")

        with patch(
            "importlib.metadata.distribution",
            return_value=DistributionWithUndecodableMetadata(),
        ):
            # Returning at all is the assertion: this raised before.
            self.module._check_dependency_version_conflict("foo==1.0.0", self.conflicts)

        self.assertEqual({}, self.conflicts)
        output = self.stderr.getvalue()
        self.assertIn("WARN", output)
        self.assertIn('cannot read the installed version of package "foo"', output)
        self.assertIn("UnicodeDecodeError", output)

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


class RenderVersionConflictsTests(unittest.TestCase):
    """How a set of conflicts reads on the one line an operator gets."""

    def setUp(self):
        self.module, self.stderr = _load_benign()

    def test_a_version_mismatch_names_both_versions(self):
        self.assertEqual(
            "foo (requires ==2.0.0, found 1.0.0)",
            self.module._render_version_conflicts(
                {"foo": {"version_required": "==2.0.0", "version_found": "1.0.0"}}),
        )

    def test_a_missing_package_reports_its_error(self):
        self.assertEqual(
            "foo (required package not found)",
            self.module._render_version_conflicts(
                {"foo": {"error": "required package not found"}}),
        )

    def test_the_two_kinds_appear_together(self):
        self.assertEqual(
            "alpha (requires ==2.0.0, found 1.0.0); beta (required package not found)",
            self.module._render_version_conflicts({
                "alpha": {"version_required": "==2.0.0", "version_found": "1.0.0"},
                "beta": {"error": "required package not found"},
            }),
        )

    def test_the_order_does_not_depend_on_insertion(self):
        # The same set of conflicts has to read the same way every run.
        forwards = self.module._render_version_conflicts({
            "alpha": {"error": "required package not found"},
            "zulu": {"error": "required package not found"},
        })
        backwards = self.module._render_version_conflicts({
            "zulu": {"error": "required package not found"},
            "alpha": {"error": "required package not found"},
        })
        self.assertEqual(forwards, backwards)
        self.assertTrue(forwards.startswith("alpha"))


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
        distributions_side_effect=None,
        extra_sys_path_entries=None,
    ):
        """Execute sitecustomize.py end to end.

        The module's own directory is redirected to self.site_dir (a temp dir
        acting as the bundled site directory), the OpenTelemetry
        auto-instrumentation entry point is replaced with a mock, and the host
        is hidden: no installed distributions, and every requirement resolves
        to installed_version.

        initialize_side_effect makes the mocked initialize() raise, which is
        how the SDK reports a failure it cannot recover from.

        distributions_side_effect makes importlib.metadata.distributions raise,
        which stands in for any failure inside import_distro() that has no
        try block of its own.

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
                patch.object(sys, "path", list(sys.path) + list(extra_sys_path_entries or [])
                             + [sys_path_entry or self.site_dir]), \
                patch("os.path.dirname", side_effect=fake_dirname), \
                patch("importlib.metadata.distributions", return_value=installed_distributions or [],
                      side_effect=distributions_side_effect), \
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

    def test_every_conflict_is_reported_not_just_the_first(self):
        # The warning is the only report an operator gets, so stopping at the
        # first conflict charges them a process restart per conflict to find
        # the rest, and the manifest is sorted, so which one they are shown is
        # decided by alphabetical order rather than by the problem.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="alpha==2.0.0\nbeta==2.0.0\ngamma==2.0.0\n",
            installed_version="1.0.0",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        for name in ("alpha", "beta", "gamma"):
            self.assertIn(name, output)

    def test_a_later_conflict_does_not_hide_an_earlier_one(self):
        # Guards the accumulation: a conflict found later must add to what the
        # loop already collected rather than replace it.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="zulu==2.0.0\nalpha==2.0.0\n",
            installed_version="1.0.0",
        )
        self.assertIn("zulu", output)
        self.assertIn("alpha", output)

    def test_the_conflicts_read_as_a_list_of_problems(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="alpha==2.0.0\nbeta==2.0.0\n",
            installed_version="1.0.0",
        )
        self.assertIn(
            "dependency conflicts: alpha (requires ==2.0.0, found 1.0.0); "
            "beta (requires ==2.0.0, found 1.0.0)",
            output,
        )
        self.assertNotIn("{", output)

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

    def test_activates_with_grpc_when_protocol_is_empty(self):
        # "The SDK MUST interpret an empty value of an environment variable the
        # same way as when the variable is unset." A blank value reaches a
        # process from a Kubernetes env: entry with no value, a docker -e with
        # nothing after the "=", or an expansion of an unset variable, and it
        # used to deactivate the agent over a configuration the SDK accepts.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": ""},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_activated(
            auto_instrumentation, observed_env, exporter="otlp_proto_grpc"
        )
        self.assertEqual("", output)

    def test_activates_with_grpc_when_protocol_is_only_whitespace(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "   "},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_activated(
            auto_instrumentation, observed_env, exporter="otlp_proto_grpc"
        )
        self.assertEqual("", output)

    def test_activates_when_a_supported_protocol_carries_whitespace(self):
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": " http/protobuf\n"},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_activated(
            auto_instrumentation, observed_env, exporter="otlp_proto_http"
        )
        self.assertEqual("", output)

    def test_signal_specific_protocol_selects_that_signal_exporter(self):
        # The signal-specific variable takes precedence over the generic one,
        # per the OTLP specification and per the SDK's own resolution. Reading
        # only the generic variable meant this request was silently discarded
        # and traces went out over grpc.
        _, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
        )
        auto_instrumentation.initialize.assert_called_once_with(swallow_exceptions=False)
        self.assertEqual("otlp_proto_http", observed_env["OTEL_TRACES_EXPORTER"])
        self.assertEqual("otlp_proto_grpc", observed_env["OTEL_METRICS_EXPORTER"])
        self.assertEqual("otlp_proto_grpc", observed_env["OTEL_LOGS_EXPORTER"])

    def test_signal_specific_protocol_overrides_the_generic_one(self):
        _, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={
                "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
                "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/protobuf",
            },
            all_dependencies="foo==1.0.0\n",
        )
        auto_instrumentation.initialize.assert_called_once_with(swallow_exceptions=False)
        self.assertEqual("otlp_proto_grpc", observed_env["OTEL_TRACES_EXPORTER"])
        self.assertEqual("otlp_proto_grpc", observed_env["OTEL_METRICS_EXPORTER"])
        self.assertEqual("otlp_proto_http", observed_env["OTEL_LOGS_EXPORTER"])

    def test_deactivates_when_a_signal_specific_protocol_is_http_json(self):
        # http/json in the signal-specific variable used to pass the guard
        # untouched and activate over grpc, which is the one value the guard
        # exists to reject.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/json"},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/json is not supported", output)
        self.assertIn("supports grpc and http/protobuf", output)

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

    def test_deactivates_when_dependencies_file_is_not_valid_utf8(self):
        # A manifest that cannot be decoded is as unusable as one that cannot
        # be opened, so it deactivates naming this file rather than reaching
        # the blanket handler as an "unexpected error".
        with open(os.path.join(self.site_dir, "all-dependencies.txt"), "wb") as f:
            f.write(b"packaging==1.0.0\n# comentario en espa\xf1ol\n")
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("cannot read all-dependencies.txt", output)
        self.assertNotIn("unexpected error", output)

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

    def test_a_multiline_validator_error_is_one_record(self):
        # otel-config-check reports a schema error across lines, and its whole
        # output becomes the reason. Without collapsing, the reason and the
        # trailing [argv] land on lines carrying neither prefix nor severity.
        self._write_fake_validator(
            exit_code=1,
            message="Configuration does not match schema:\n  42 is not of type 'object'\n    at tracer_provider",
        )
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_CONFIG_FILE": os.path.join(self.base_dir, "otel-config.yaml")},
            all_dependencies="foo==1.0.0\n",
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertEqual(1, len(output.splitlines()))
        self.assertIn("at tracer_provider", output)

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
        dist.version = "1.20.0"
        dist.locate_file.return_value = "/app/site-packages"
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("already instrumented", output)
        self.assertIn("opentelemetry-sdk 1.20.0 (/app/site-packages)", output)

    def test_deactivates_on_a_distribution_from_a_non_path_finder(self):
        # A distribution implementing only the public Distribution interface,
        # as any finder other than the path-based one produces, must still be
        # detected and named rather than raising. A real object is used here,
        # not a MagicMock, because a MagicMock answers to every attribute and
        # would hide exactly the bug this covers.
        class DistributionFromNonPathFinder(object):
            metadata = {"Name": "opentelemetry-sdk"}
            version = "1.20.0"

            def locate_file(self, path):
                return "/app/site-packages"

        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[DistributionFromNonPathFinder()],
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("already instrumented", output)
        self.assertIn("opentelemetry-sdk 1.20.0 (/app/site-packages)", output)

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
                dist.version = "1.20.0"
                dist.locate_file.return_value = "/app/site-packages"
                output, auto_instrumentation, observed_env = self._exec_sitecustomize(
                    extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
                    all_dependencies="foo==1.0.0\n",
                    installed_distributions=[dist],
                )
                self._assert_deactivated(auto_instrumentation, observed_env)
                self.assertIn("already instrumented", output)
                # Reported under the name the package declared, which is what
                # the operator will see in pip list, not the normalized form
                # the guard matched on.
                self.assertIn(
                    "{} 1.20.0 (/app/site-packages)".format(spelling), output)

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

    def test_deactivates_on_the_vendored_grpc_exporter(self):
        # The bundle vendors this exporter, so an application carrying it means
        # a second copy of the same distribution is about to enter the process.
        dist = MagicMock()
        dist.metadata = {"Name": "opentelemetry-exporter-otlp-pyproto-grpc"}
        dist._path = "/app/site-packages/opentelemetry_exporter_otlp_pyproto_grpc-1.44.0.dist-info"
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("already instrumented", output)

    def test_deactivates_on_the_vendored_pyproto_encoder(self):
        # opentelemetry-pyproto owns the public opentelemetry.proto module path,
        # the same one opentelemetry-proto owns, so it is double instrumentation
        # for the same reason its upstream counterpart is.
        dist = MagicMock()
        dist.metadata = {"Name": "opentelemetry-pyproto"}
        dist._path = "/app/site-packages/opentelemetry_pyproto-1.44.0.dist-info"
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("already instrumented", output)

    def test_unrelated_installed_distribution_does_not_deactivate(self):
        dist = MagicMock()
        dist.metadata = {"Name": "flask"}
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[dist],
        )
        self._assert_activated(auto_instrumentation, observed_env)

    def test_a_failing_deactivation_still_reports(self):
        # _self_deactivate() normalizes every sys.path entry, so a non-str one
        # makes it raise. site.py runs .pth files before execsitecustomize(),
        # so an entry like this can be in place before the agent starts.
        #
        # It used to share a try block with the report, and deactivating came
        # first, so raising there discarded the report and the process said
        # nothing whatsoever. Reporting is the one thing this handler exists to
        # guarantee, so it now happens first and is guarded on its own.
        output, auto_instrumentation, _ = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            extra_sys_path_entries=[object()],
        )
        auto_instrumentation.initialize.assert_not_called()
        self.assertIn("cannot auto-instrument Python process", output)
        self.assertIn("unexpected error while deciding whether to auto-instrument", output)
        self.assertIn("TypeError", output)

    def test_undecodable_distribution_metadata_is_skipped_with_a_warning(self):
        # importlib.metadata decodes METADATA as UTF-8, so a distribution whose
        # file is not valid UTF-8 (a Latin-1 author field written by older
        # tooling is the usual cause) raises when its name is read. Such a
        # package is almost never an OpenTelemetry one, and it used to abort
        # the whole scan and deactivate the agent for the process.
        #
        # A real object is used rather than a MagicMock, which answers to every
        # attribute and so could not raise where this needs it to.
        class DistributionWithUndecodableMetadata(object):
            @property
            def metadata(self):
                raise UnicodeDecodeError("utf-8", b"\xe9", 0, 1, "invalid continuation byte")

            def locate_file(self, path):
                return "/app/site-packages"

        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[DistributionWithUndecodableMetadata()],
        )
        self._assert_activated(auto_instrumentation, observed_env)
        self.assertIn("cannot read the metadata", output)
        # The name is what could not be read, so the directory is the only
        # thing that identifies the package the operator has to go and fix.
        self.assertIn("/app/site-packages", output)
        self.assertIn("UnicodeDecodeError", output)

    def test_an_undecodable_distribution_does_not_hide_a_real_one(self):
        # Skipping the unreadable distribution must not cost the scan the
        # distribution it exists to find, so a genuine offender behind a broken
        # one is still detected and named.
        class DistributionWithUndecodableMetadata(object):
            @property
            def metadata(self):
                raise UnicodeDecodeError("utf-8", b"\xe9", 0, 1, "invalid continuation byte")

            def locate_file(self, path):
                return "/app/site-packages"

        offender = MagicMock()
        offender.metadata = {"Name": "opentelemetry-sdk"}
        offender.version = "1.20.0"
        offender.locate_file.return_value = "/app/site-packages"

        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            installed_distributions=[DistributionWithUndecodableMetadata(), offender],
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("already instrumented", output)
        self.assertIn("opentelemetry-sdk 1.20.0 (/app/site-packages)", output)

    def test_unexpected_error_deactivates_with_a_warning(self):
        # Only four steps inside import_distro() have a try block of their
        # own. An exception from any other step escaped into
        # site.execsitecustomize(), which reports one line that does not name
        # this agent and leaves the site on PYTHONPATH for every child process
        # to retry. The outer guard turns that into an ordinary deactivation.
        output, auto_instrumentation, observed_env = self._exec_sitecustomize(
            extra_env={"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"},
            all_dependencies="foo==1.0.0\n",
            distributions_side_effect=RuntimeError("metadata backend exploded"),
        )
        self._assert_deactivated(auto_instrumentation, observed_env)
        self.assertIn("unexpected error while deciding whether to auto-instrument", output)
        self.assertIn("RuntimeError: metadata backend exploded", output)

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


class DoubleInstrumentationPackageListTests(unittest.TestCase):
    """The double-instrumentation list against the bundle it has to describe.

    The list is maintained by hand, so nothing but a test keeps it in step with
    requirements.txt. It fell behind once already: the vendored gRPC exporter
    and opentelemetry-pyproto were installed by the bundle without being added
    here, and an application carrying either was not recognised as instrumented.
    """

    def setUp(self):
        self.module, self.stderr = _load_benign()

    def test_every_vendored_package_is_checked(self):
        # A vendored package is one this bundle installs from source, so an
        # application carrying it means a second copy of the same distribution.
        with open(os.path.join(TEST_DIR, "requirements.txt")) as f:
            vendored = [
                os.path.basename(line.strip())
                for line in f
                if line.strip().startswith("./vendor/")
            ]
        self.assertTrue(vendored, "no vendored requirements found to check against")
        for name in vendored:
            self.assertIn(name, self.module.double_instrumentation_check_packages)

    def test_the_list_is_sorted(self):
        # Sorted so a new requirements.txt entry can be checked against it by eye.
        packages = self.module.double_instrumentation_check_packages
        self.assertEqual(sorted(packages), packages)

    def test_the_list_has_no_duplicates(self):
        packages = self.module.double_instrumentation_check_packages
        self.assertEqual(len(set(packages)), len(packages))


class LogLevelTests(unittest.TestCase):
    """What OTEL_INJECTOR_LOG_LEVEL accepts.

    It is named for a level, so it has to take one. It is also the documented
    way to make the agent explain itself, so a value it does not understand
    must say so rather than quietly mean "off".
    """

    def _resolve(self, value):
        module, _ = _load_benign()
        return module._resolve_log_level(value)

    def test_every_level_name_is_accepted_in_any_case(self):
        for spelling, expected in (
            ("debug", 10), ("DEBUG", 10), ("Debug", 10), ("  debug  ", 10),
            ("info", 20), ("INFO", 20),
            ("warning", 30), ("warn", 30), ("WARNING", 30),
        ):
            with self.subTest(spelling=spelling):
                level, unrecognized = self._resolve(spelling)
                self.assertEqual(expected, level)
                self.assertIsNone(unrecognized)

    def test_a_level_above_warning_is_clamped_not_honoured(self):
        # A warning is the only report an operator gets when a guard
        # deactivates the agent, so no level may turn it off.
        for spelling in ("error", "critical", "ERROR"):
            with self.subTest(spelling=spelling):
                level, unrecognized = self._resolve(spelling)
                self.assertEqual(30, level)
                self.assertIsNone(unrecognized)

    def test_unset_or_empty_means_warning(self):
        for value in (None, "", "   "):
            with self.subTest(value=value):
                level, unrecognized = self._resolve(value)
                self.assertEqual(30, level)
                self.assertIsNone(unrecognized)

    def test_an_unrecognized_value_is_handed_back_verbatim(self):
        for value in ("trace", "verbose", "1", "true"):
            with self.subTest(value=value):
                level, unrecognized = self._resolve(value)
                self.assertEqual(30, level)
                self.assertEqual(value, unrecognized)

    def test_debug_is_the_only_level_that_emits_debug_records(self):
        for value, enabled in (
            ("debug", True), ("DEBUG", True),
            ("info", False), ("warning", False), ("error", False),
            ("trace", False), (None, False),
        ):
            with self.subTest(value=value):
                module, buf = _load_benign(
                    extra_env={} if value is None else {"OTEL_INJECTOR_LOG_LEVEL": value})
                module._log_debug("a trace")
                self.assertEqual(enabled, "a trace" in buf.getvalue())

    def test_no_level_silences_a_warning(self):
        for value in ("debug", "info", "warning", "error", "critical", "trace"):
            with self.subTest(value=value):
                module, buf = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": value})
                module._log_warn("a diagnostic")
                self.assertIn("a diagnostic", buf.getvalue())

    def test_an_unrecognized_value_is_reported_at_load(self):
        # The whole point of the variable is to make the agent explain itself,
        # so answering a typo with silence is the one thing it must not do.
        buf = StringIO()
        env = {
            k: v for k, v in os.environ.items()
            if k not in ("OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_CONFIG_FILE")
        }
        env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/json"
        env["OTEL_INJECTOR_LOG_LEVEL"] = "verbose"
        with patch.dict(os.environ, env, clear=True), patch.object(sys, "path", list(sys.path)):
            _load_sitecustomize(buf)
        output = buf.getvalue()
        self.assertIn('OTEL_INJECTOR_LOG_LEVEL="verbose" is not a level', output)
        self.assertIn("debug", output)
        self.assertIn("WARNING", output)

    def test_a_recognized_value_reports_nothing_about_the_level(self):
        for value in ("debug", "info", "warning", "error"):
            with self.subTest(value=value):
                module, buf = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": value})
                self.assertNotIn("is not a level", buf.getvalue())

    def test_a_non_debug_level_still_never_builds_the_logger(self):
        # The short circuit in _log_debug is what keeps a process that is not in
        # debug from importing logging at all; making the variable level-aware
        # must not trade that away.
        for value in ("info", "warning", "error"):
            with self.subTest(value=value):
                module, _ = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": value})
                module._logger = None
                module._log_debug("a trace")
                self.assertIsNone(module._logger)


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

    def test_a_multiline_message_is_emitted_as_one_record(self):
        # A continuation line reaches stderr with no prefix and no severity, so
        # a line-oriented collector reads it as a separate record belonging to
        # the application.
        module, buf = _load_benign()
        module._log_warn("first line\nsecond line\nthird line")
        output = buf.getvalue()
        self.assertEqual(1, len(output.splitlines()))
        self.assertIn("first line second line third line", output)

    def test_collapsing_keeps_every_word_of_the_message(self):
        module, buf = _load_benign()
        module._log_warn("schema error:\n  42 is not of type 'object'\n    at tracer_provider")
        output = buf.getvalue()
        for word in ("schema", "error:", "42", "not", "type", "'object'", "at", "tracer_provider"):
            self.assertIn(word, output)

    def test_a_debug_record_is_collapsed_too(self):
        module, buf = _load_benign(extra_env={"OTEL_INJECTOR_LOG_LEVEL": "debug"})
        module._log_debug("a trace\nsplit across lines")
        self.assertEqual(1, len(buf.getvalue().splitlines()))

    def test_a_record_logged_without_the_helpers_is_collapsed(self):
        # The collapsing lives in the formatter, so it covers a diagnostic that
        # reaches for the logger directly instead of _log_warn or _log_debug.
        module, buf = _load_benign()
        module._get_logger().warning("first line\nsecond line")
        self.assertEqual(1, len(buf.getvalue().splitlines()))

    def test_a_single_line_message_is_untouched(self):
        module, buf = _load_benign()
        module._log_warn("a diagnostic")
        self.assertEqual(
            self.PREFIX + " WARNING: a diagnostic", buf.getvalue().rstrip("\n"))

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
