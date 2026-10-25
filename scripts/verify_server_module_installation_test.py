"""Exercise the standalone verifier's real acceptance invocation boundary."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import unittest
from unittest import mock


SPEC = importlib.util.spec_from_file_location(
    "verify_server_module_installation", Path(__file__).with_name("verify-server-module-installation.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class InstalledAcceptance(unittest.TestCase):
    """Successful build fixtures must still select the actual installed runtime."""

    library_version = "v0.8.7-0.20261025154622-bb39d3a24680"

    def go_fixture(self, args, cwd, env):
        """Reach the actual final subprocess call without rebuilding unrelated packages."""
        if args[:4] == ["go", "mod", "download", "-json"]:
            return json.dumps({"GoMod": "fixture-server.mod"})
        if args[:4] == ["go", "mod", "edit", "-json"]:
            return json.dumps({"Require": [{"Path": MODULE.LIBRARY, "Version": self.library_version}]}
                              if len(args) > 4 else {})
        if args[:2] == ["go", "list"]:
            return json.dumps({"Version": self.library_version})
        if args[:3] == ["go", "version", "-m"]:
            return "\tdep\t" + MODULE.LIBRARY + "\t" + self.library_version + "\n"
        return ""

    def test_installed_runtime_reaches_acceptance_consumers(self):
        """Invoke real main and assert the environment read by existing acceptance tests."""
        calls = []

        def acceptance_boundary(args, *, cwd, env, check, timeout, **kwargs):
            calls.append(args)
            self.assertEqual(args[:2], ["go", "test"])
            self.assertEqual(args[-1], "./acceptance/...")
            self.assertEqual(Path(cwd), MODULE.ROOT / "server")
            self.assertEqual(env["GOWORK"], "off")
            for command, key in [("dmserver", "LAB_DMSERVER"), ("dmctl", "LAB_DMCTL")]:
                self.assertTrue(env.get(key), "acceptance never selects installed " + command + ": " + key)
                self.assertEqual(Path(env[key]).stem, command)
                self.assertEqual(Path(env[key]).parent.name, "bin")
            self.assertTrue(env.get("LAB_REPORT_DIR"), "acceptance provenance directory is missing")
            self.assertEqual(env.get("LAB_REVISION"), "v0.0.0-review.fixture")
            report = Path(env["LAB_REPORT_DIR"]) / "process" / "device-management"
            report.mkdir(parents=True)
            (report / "results.json").write_text(json.dumps([
                {"ID": "E2E-021", "Adapter": "process", "Revision": env["LAB_REVISION"], "Status": "passed"}]))
            events = "\n".join(json.dumps({"Action": "pass", "Test": name}) for name in
                               ["TestScenarios", "TestLabCLI", "TestBinaryValidationDelivery"])
            return subprocess.CompletedProcess(args, 0, stdout=events)

        with mock.patch.dict(os.environ, {}, clear=True), \
                mock.patch("sys.argv", ["verify-server-module-installation.py"]), \
                mock.patch.object(MODULE, "candidate_proxy", return_value="v0.0.0-review.fixture"), \
                mock.patch.object(MODULE, "run", side_effect=self.go_fixture), \
                mock.patch.object(MODULE.subprocess, "run", side_effect=acceptance_boundary), \
                contextlib.redirect_stdout(io.StringIO()):
            MODULE.main()
        self.assertEqual(len(calls), 1)


class RuntimeEvidence(unittest.TestCase):
    """A successful Go exit must not mask skipped tests or embedded-runtime evidence."""

    def setUp(self):
        """Prepare the same report path written by the actual acceptance runner."""
        import tempfile
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        self.report = self.root / "process" / "device-management" / "results.json"
        self.report.parent.mkdir(parents=True)
        self.version = "v0.0.0-review.fixture"
        self.result = {"ID": "E2E-021", "Adapter": "process", "Revision": self.version, "Status": "passed"}
        self.report.write_text(json.dumps([self.result]))
        self.events = [{"Action": "pass", "Test": name} for name in
                       ["TestScenarios", "TestLabCLI", "TestBinaryValidationDelivery"]]

    def evidence(self, events=None):
        """Run the verifier's real result check against this fixture's retained reports."""
        return MODULE.acceptance_evidence("\n".join(json.dumps(event) for event in
                                                   (self.events if events is None else events)),
                                          self.root, self.version)

    def test_required_runtime_tests_cannot_skip_or_disappear(self):
        """Zero-exit skipped CLI or binary validation is insufficient installation evidence."""
        for name in ["TestScenarios", "TestLabCLI", "TestBinaryValidationDelivery"]:
            for action in ["missing", "skip", "fail"]:
                with self.subTest(name=name, action=action):
                    events = [event for event in self.events if event["Test"] != name]
                    if action != "missing":
                        events.append({"Action": action, "Test": name})
                    with self.assertRaises(RuntimeError):
                        self.evidence(events)

    def test_process_report_binds_mode_version_and_success(self):
        """Actual scenario results must identify the installed process and candidate revision."""
        for field, value in [("Adapter", "inprocess"), ("Revision", "other"),
                             ("Status", "unsupported"), ("ID", "")]:
            with self.subTest(field=field):
                self.report.write_text(json.dumps([{**self.result, field: value}]))
                with self.assertRaises(RuntimeError):
                    self.evidence()
        for results in [[], [self.result, self.result]]:
            with self.subTest(results=results):
                self.report.write_text(json.dumps(results))
                with self.assertRaises(RuntimeError):
                    self.evidence()
        self.report.unlink()
        with self.assertRaises(FileNotFoundError):
            self.evidence()

    def test_retains_safe_actual_outcomes(self):
        """Retain mode/version and module outcomes without protocol evidence or private detail."""
        self.report.write_text(json.dumps([{**self.result, "Detail": "PRIVATE-FIXTURE-DETAIL",
                                           "Evidence": ["private/raw-fixture.json"]}]))
        result = self.evidence()
        self.assertEqual(result["scenarios"], [self.result])
        self.assertEqual(set(result["tests"].values()), {"pass"})
        self.assertNotIn("PRIVATE-FIXTURE", json.dumps(result))
        self.assertNotIn("private/raw-fixture.json", json.dumps(result))


if __name__ == "__main__":
    unittest.main()
