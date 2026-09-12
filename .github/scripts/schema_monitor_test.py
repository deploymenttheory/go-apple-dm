import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("monitor", Path(__file__).with_name("schema_monitor.py"))
m = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(m)


def branch(ref="seed_OS_27_0", kind="seed"):
    return {"ref": ref, "kind": kind, "key": m.digest(ref)[:16], "commit": "b" * 40,
            "baseline": "a" * 40, "baselineRef": "release"}


def report(ref="seed_OS_27_0"):
    return {"schemaVersion": 1, "branch": branch(ref), "projectCommit": "c" * 40,
            "complete": True, "patch": False, "findings": [],
            "stages": {s: {"state": "passed"} for s in m.STAGES}}


def incident(kind="failure", stage="parse"):
    return m.finding("schema-format" if kind == "failure" else "behavior-review", kind, stage,
                     "examples", "Parser rejects examples", "Implement metadata support", [
                         {"path": "a.yaml", "detail": "unknown examples field"}])


def manifest(*reports):
    return {"schemaVersion": 1, "complete": True, "projectCommit": "c" * 40,
            "stableCommit": "a" * 40, "branches": [r["branch"] for r in reports]}


def stored_issue(result, item, state="open", status="observed"):
    return {"number": 12, "state": state, "body": "Engineer preface\n" + m.evidence_section(
        item, result, "https://example.com/run", status) + "\nEngineer conclusion"}


class DiscoveryTests(unittest.TestCase):
    def test_default_is_independent_of_seed_naming_and_years(self):
        for default in ("release", "main"):
            text = "ref: refs/heads/" + default + "\tHEAD\n" + "a" * 40 + "\trefs/heads/" + default + "\n"
            text += "b" * 40 + "\trefs/heads/seed_future\n"
            name, heads = m.parse_refs(text)
            self.assertEqual(default, name)
            self.assertEqual(2, len(heads))
        for text in ("", "ref: refs/heads/seed_current\tHEAD\n" + "a" * 40 + "\trefs/heads/seed_current\n"):
            with self.assertRaises(ValueError):
                m.parse_refs(text)

    def test_unchanged_stable_does_not_suppress_seed_discovery(self):
        refs = "ref: refs/heads/release\tHEAD\n" + "a" * 40 + "\trefs/heads/release\n"
        refs += "b" * 40 + "\trefs/heads/seed_OS_27_0\n" + "d" * 40 + "\trefs/heads/seed-next/preview\n"
        with tempfile.TemporaryDirectory() as tmp, patch.object(m, "run", side_effect=["c" * 40, refs, "a" * 40]):
            result = m.discover(Path(tmp), Path(tmp) / "discovery.json")
        self.assertTrue(result["complete"])
        self.assertEqual(3, len(result["branches"]))
        self.assertEqual(result["branches"][0]["baseline"], result["branches"][0]["commit"])
        self.assertEqual({"stable", "seed"}, {b["kind"] for b in result["branches"]})

    def test_discovery_failure_is_retained(self):
        with tempfile.TemporaryDirectory() as tmp, patch.object(m, "run", side_effect=OSError("offline")):
            result = m.discover(Path(tmp), Path(tmp) / "discovery.json")
            self.assertFalse(result["complete"])
            self.assertEqual("offline", json.loads((Path(tmp) / "discovery.json").read_text())["error"])

    def test_no_seeds_is_valid_and_moved_seed_changes_identity(self):
        refs = "ref: refs/heads/release\tHEAD\n" + "a" * 40 + "\trefs/heads/release\n"
        self.assertEqual(1, len(m.parse_refs(refs)[1]))
        old = refs + "b" * 40 + "\trefs/heads/seed_27\n"
        new = refs + "e" * 40 + "\trefs/heads/seed_27\n"
        self.assertNotEqual(m.parse_refs(old)[1], m.parse_refs(new)[1])


class IssueLifecycleTests(unittest.TestCase):
    def test_create_then_unchanged_rerun_has_no_writes(self):
        result, item = report(), incident()
        result["findings"] = [item]
        first = m.issue_actions([], [result], manifest(result), "url")
        self.assertEqual(1, len(first))
        self.assertEqual("POST", first[0][0])
        issue = dict(first[0][2], number=12, state="open")
        self.assertEqual([], m.issue_actions([issue], [result], manifest(result), "a-new-run-url"))

    def test_changed_evidence_preserves_engineer_notes(self):
        result, item = report(), incident()
        issue = stored_issue(result, item)
        updated = copy.deepcopy(item)
        updated["fingerprint"] = "changed"
        updated["evidence"].append({"path": "b.yaml", "detail": "same unsupported field"})
        result["findings"] = [updated]
        actions = m.issue_actions([issue], [result], manifest(result), "url")
        self.assertEqual(1, len(actions))
        self.assertTrue(actions[0][2]["body"].startswith("Engineer preface"))
        self.assertTrue(actions[0][2]["body"].endswith("Engineer conclusion"))

    def test_blocked_failed_or_incomplete_checks_cannot_close(self):
        for stage_state, complete in (("blocked", True), ("failed", True), ("passed", False)):
            result, item = report(), incident()
            result["stages"]["parse"]["state"] = stage_state
            result["complete"] = complete
            self.assertEqual([], m.issue_actions([stored_issue(result, item)], [result], manifest(result), "url"))

    def test_verified_failure_closes_and_recurrence_reopens(self):
        result, item = report(), incident()
        issue = stored_issue(result, item)
        actions = m.issue_actions([issue], [result], manifest(result), "url")
        self.assertEqual("closed", actions[0][2]["state"])
        closed = dict(issue, **actions[0][2])
        self.assertEqual("verified", m.metadata(closed["body"])["status"])
        result["findings"] = [item]
        actions = m.issue_actions([closed], [result], manifest(result), "url")
        self.assertEqual("open", actions[0][2]["state"])

    def test_review_requires_engineer_and_acknowledgment_is_stable(self):
        result, item = report(), incident("review", "audit")
        issue = stored_issue(result, item)
        self.assertEqual([], m.issue_actions([issue], [result], manifest(result), "url"))
        result["findings"] = [item]
        issue["state"] = "closed"
        self.assertEqual([], m.issue_actions([issue], [result], manifest(result), "url"))
        item["fingerprint"] = "new-behavior"
        self.assertEqual("open", m.issue_actions([issue], [result], manifest(result), "url")[0][2]["state"])

    def test_retired_branch_is_inactive_not_fixed(self):
        result, item = report(), incident()
        issue = stored_issue(result, item)
        actions = m.issue_actions([issue], [], manifest(), "url")
        self.assertNotIn("state", actions[0][2])
        self.assertEqual("inactive", m.metadata(actions[0][2]["body"])["status"])
        self.assertEqual([], m.issue_actions([issue], [], {"complete": False, "branches": []}, "url"))

    def test_recovered_discovery_can_resolve_its_own_issue(self):
        result = report("discovery")
        item = incident(stage="snapshot")
        action = m.issue_actions([stored_issue(result, item)], [], manifest(), "url")
        self.assertEqual("closed", action[0][2]["state"])

    def test_unmanaged_issues_and_bad_markers_are_untouched(self):
        existing = [{"number": 1, "body": "ordinary issue", "state": "open"},
                    {"number": 2, "body": "<!-- schema-monitor {invalid} -->", "state": "open"}]
        self.assertEqual([], m.issue_actions(existing, [], manifest(), "url"))
        self.assertIn("notes", m.replace_section("notes", "new evidence"))

    def test_new_project_commit_updates_evidence_without_creating_issue(self):
        result, item = report(), incident()
        existing = stored_issue(result, item)
        result["findings"] = [item]
        result["projectCommit"] = "d" * 40
        actions = m.issue_actions([existing], [result], manifest(result), "url")
        self.assertEqual(["PATCH"], [a[0] for a in actions])


class AssessmentTests(unittest.TestCase):
    def test_promoted_sources_retain_history_and_compare_older_release(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            provenance = {"commit": "b" * 40, "ref": "seed_OS_27_0",
                          "history": {"commit": "a" * 40}}
            m.write_json(root / m.SCHEMA / "GENERATED_FROM.json", provenance)

            def source(args, *unused):
                return "a" * 40 if args[-1] == "HEAD:" + m.HISTORY_SUBMODULE else "b" * 40

            with patch.object(m, "run", side_effect=source), patch.object(m, "is_ancestor", return_value=False):
                seed = m.assessment_sources(root, root, branch())
                self.assertEqual("a" * 40, seed["historyCommit"])
                self.assertFalse(seed["comparisonOnly"])
                self.assertTrue(seed["adoptedOS27"])
                stable = m.assessment_sources(root, root, branch("release", "stable"))
                self.assertTrue(stable["comparisonOnly"])
                self.assertEqual("a" * 40, stable["auditBaseline"])
                self.assertFalse(stable["adoptedOS27"])
            with patch.object(m, "run", side_effect=source), patch.object(m, "is_ancestor", return_value=True):
                stable = m.assessment_sources(root, root, branch("release", "stable"))
                self.assertFalse(stable["comparisonOnly"])
                self.assertTrue(stable["adoptedOS27"])
                self.assertEqual("a" * 40, stable["historyCommit"])
            with patch.object(m, "run", return_value="b" * 40):
                with self.assertRaisesRegex(ValueError, "historical provenance"):
                    m.assessment_sources(root, root, branch())
            provenance.pop("history")
            m.write_json(root / m.SCHEMA / "GENERATED_FROM.json", provenance)
            with patch.object(m, "run", return_value="b" * 40):
                self.assertEqual("b" * 40, m.assessment_sources(root, root, branch())["historyCommit"])

    def test_ancestry_errors_are_not_treated_as_older_stable(self):
        with patch.object(m, "run", return_value=""):
            self.assertTrue(m.is_ancestor(Path("/tmp"), "a", "b"))
        with patch.object(m, "run", side_effect=subprocess.CalledProcessError(1, "git")):
            self.assertFalse(m.is_ancestor(Path("/tmp"), "a", "b"))
        with patch.object(m, "run", side_effect=subprocess.CalledProcessError(128, "git")):
            with self.assertRaises(subprocess.CalledProcessError):
                m.is_ancestor(Path("/tmp"), "a", "b")

    def test_release_issue_evidence_names_the_actual_comparison_baseline(self):
        result = report("release")
        result.update(comparisonOnly=True, auditBaseline="d" * 40)
        body = m.evidence_section(incident(), result, "url")
        self.assertIn("Baseline: ` " + "d" * 40 + " `", body)
        self.assertNotIn("Baseline: ` " + result["branch"]["baseline"] + " `", body)

    def test_routine_contract_gate_rejects_missing_skipped_and_failed_tests(self):
        events = "\n".join(json.dumps({"Action": "pass", "Package": test.rsplit("/", 1)[0],
                                     "Test": test.rsplit("/", 1)[1]}) for test in m.OS27_TESTS)
        for ok, output, expected in [(True, events, True), (True, "", False),
                                      (True, events.replace('"pass"', '"skip"'), False),
                                      (False, events, False)]:
            with self.subTest(ok=ok, expected=expected), tempfile.TemporaryDirectory() as tmp:
                with patch.object(m, "command_stage", return_value=(ok, output)), patch("builtins.print"):
                    self.assertEqual(expected, m.verify_contracts(Path(tmp), Path(tmp)))
                self.assertEqual(expected, json.loads((Path(tmp) / "result.json").read_text())["passed"])

    def test_seed_contract_requires_executed_tests(self):
        tags, required = m.assessment_test_contract({"kind": "seed", "ref": "seed_OS_27_0"})
        self.assertEqual(["-tags", "schema_seed_os_27"], tags)
        self.assertEqual(8, len(required))
        self.assertEqual(sorted(required), m.missing_test_evidence("", required))
        lines = []
        for test in required:
            package, name = test.rsplit("/", 1)
            lines.append(json.dumps({"Action": "pass", "Package": package, "Test": name}))
        self.assertEqual([], m.missing_test_evidence("\n".join(lines), required))
        self.assertEqual(sorted(required), m.missing_test_evidence("\n".join(lines).replace('"pass"', '"skip"'), required))
        self.assertEqual(sorted(required), m.missing_test_evidence('garbled\nnull\n[]', required))
        self.assertEqual(([], set()), m.assessment_test_contract({"kind": "stable", "ref": "release"}))
        self.assertEqual(([], set()), m.assessment_test_contract({"kind": "seed", "ref": "seed_future"}))
        self.assertEqual((tags, required), m.assessment_test_contract({"kind": "stable", "ref": "release"}, True))

    def test_snapshot_guard_rejects_wrong_candidate_and_project(self):
        with patch.object(m, "run", return_value="wrong"):
            with self.assertRaises(ValueError):
                m.assert_snapshot(Path("/tmp/project"), "a" * 40, "c" * 40)
        with patch.object(m, "run", side_effect=["a" * 40, "wrong"]):
            with self.assertRaises(ValueError):
                m.assert_snapshot(Path("/tmp/project"), "a" * 40, "c" * 40)

    def test_paths_allow_generated_code_but_not_removal_allowances_or_logs(self):
        for name in (".gitmodules", m.SUBMODULE, m.HISTORY_SUBMODULE, m.SCHEMA + "/commands/types.gen.go", m.SCHEMA + "/commands/conformance_gen_test.go", m.SCHEMA + "/GENERATED_FROM.json"):
            self.assertTrue(m.allowed_path(name), name)
        for name in ("seeds.md", "run.log", "server/go.mod", ".github/workflows/test.yml", m.SCHEMA + "/ALLOWED_REMOVALS.md", m.SCHEMA + "/support/support.go"):
            self.assertFalse(m.allowed_path(name), name)

    def test_api_reports_removals_and_signatures_by_package(self):
        findings = m.api_findings({"added": ["commands/New"], "removed": ["profiles/Old", "profiles/Old.Value"],
                                  "changed": [{"name": "commands/Query.Value", "before": "string", "after": "int"}]})
        self.assertEqual(2, len(findings))
        self.assertEqual(2, len(findings[1]["evidence"]))
        self.assertTrue(all(f["kind"] == "failure" for f in findings))

    def test_command_stage_retains_failed_output_and_timeout(self):
        result = report()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            with patch.object(m.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, "specific failure")):
                ok, text = m.command_stage(result, "build", ["go", "build"], root, root)
            self.assertFalse(ok)
            self.assertEqual("specific failure", (root / "build.log").read_text())
            with patch.object(m.subprocess, "run", side_effect=subprocess.TimeoutExpired("go", 1)):
                ok, _ = m.command_stage(result, "tests", ["go", "test"], root, root)
            self.assertFalse(ok)
            self.assertEqual("failed", result["stages"]["tests"]["state"])

    def test_assessment_exception_writes_blocked_report(self):
        result = report()
        with tempfile.TemporaryDirectory() as tmp, patch.object(m, "run", side_effect=OSError("offline")):
            got = m.assess(Path(tmp), manifest(result), result["branch"], Path(tmp) / "report")
            self.assertFalse(got["complete"])
            self.assertEqual("blocked", got["stages"]["tests"]["state"])
            self.assertEqual("automation", got["findings"][0]["category"])
            self.assertTrue((Path(tmp) / "report/result.json").exists())

    def test_missing_matrix_artifact_is_failure_not_pass(self):
        result = report()
        with tempfile.TemporaryDirectory() as tmp:
            got = m.collect_reports(manifest(result), Path(tmp))[0]
        self.assertFalse(got["complete"])
        self.assertEqual("blocked", got["stages"]["tests"]["state"])
        self.assertEqual("automation", got["findings"][0]["category"])

    def test_artifact_identity_must_match_discovery(self):
        result = report()
        with tempfile.TemporaryDirectory() as tmp:
            changed = copy.deepcopy(result)
            changed["projectCommit"] = "different"
            m.write_json(Path(tmp) / result["branch"]["key"] / "result.json", changed)
            with self.assertRaises(ValueError):
                m.collect_reports(manifest(result), Path(tmp))

    def test_runtime_fingerprint_ignores_run_times_and_random_scratch_paths(self):
        events = [{"Action": "fail", "Package": "example/service", "Test": "TestCheckin", "Elapsed": 0.01}]
        before = m.stage_evidence("tests", json.dumps(events[0]))
        events[0]["Elapsed"] = 13.37
        self.assertEqual(before, m.stage_evidence("tests", json.dumps(events[0])))
        self.assertIn("$ASSESSMENT", m.stage_evidence("build", "/tmp/dm-schema-assessment-abc/project/error")[0]["detail"])

    def test_boundary_generation_failure_does_not_suppress_protocol_tests(self):
        result = report()
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp).resolve()

            def stage(result, name, args, cwd, directory, env=None):
                text = '{"added":[],"removed":[],"changed":[]}' if name == "api" else "fixture outcome"
                if name == "tests":
                    text = "\n".join(json.dumps({"Action": "pass", "Package": test.rsplit("/", 1)[0], "Test": test.rsplit("/", 1)[1]}) for test in m.OS27_TESTS)
                result["stages"][name] = {"state": "failed" if name == "boundaries" else "passed"}
                (directory / (name + ".log")).write_text(text)
                return name != "boundaries", text

            def run(args, *unused):
                return json.dumps({"Dir": str(root)}) if args[:3] == ["go", "list", "-m"] else ""

            with patch.object(m, "command_stage", side_effect=stage), patch.object(m, "run", side_effect=run), patch.object(m, "assert_snapshot"):
                m.assess_generated(result, ["tool"], root, root, "tool", root, root)
        self.assertEqual("passed", result["stages"]["tests"]["state"])
        self.assertEqual("boundaries", result["findings"][0]["stage"])


class PublicationTests(unittest.TestCase):
    def test_comparison_only_cannot_publish_a_downgrade(self):
        result = report("release")
        result.update(comparisonOnly=True, patch=True)
        for current in ([], [{"number": 3, "body": "<!-- schema-monitor-pr --> old"}]):
            api = FakeGitHub(current)
            with patch.object(m, "run", side_effect=AssertionError("No git mutation expected")):
                self.assertEqual("not-applicable", m.publish_patch(Path("/tmp"), Path("/tmp"), result, api, "token", "url"))
            if current:
                self.assertEqual("closed", api.calls[-1][2]["state"])
                self.assertIn("Comparison only", api.calls[-1][2]["body"])

    def test_release_comparison_cannot_close_published_api_failures(self):
        result = report("release")
        result["comparisonOnly"] = True
        item = incident(stage="api")
        existing = stored_issue(result, item)
        self.assertEqual([], m.issue_actions([existing], [result], manifest(result), "url"))

    def test_report_only_never_accesses_github_or_pushes(self):
        result = report()
        result["findings"] = [incident()]
        with tempfile.TemporaryDirectory() as tmp, patch.object(m, "GitHub", side_effect=AssertionError("GitHub access forbidden")):
            root = Path(tmp)
            m.write_json(root / result["branch"]["key"] / "result.json", result)
            self.assertTrue(m.publish(root, manifest(result), root, "owner/repo", True, "url"))
            self.assertEqual(1, len(json.loads((root / "proposed-issues.json").read_text())))

    def test_blocked_generation_does_not_create_pr(self):
        result = report()
        api = FakeGitHub([])
        with patch.object(m, "run", side_effect=AssertionError("No git mutation expected")):
            m.publish_patch(Path("/tmp"), Path("/tmp"), result, api, "", "url")
        self.assertEqual(["GET"], [c[0] for c in api.calls])

    def test_existing_preview_reports_latest_blocked_candidate(self):
        api = FakeGitHub([{"number": 3, "body": "<!-- schema-monitor-pr --> old preview", "draft": True}])
        m.publish_patch(Path("/tmp"), Path("/tmp"), report(), api, "", "url")
        self.assertIn("last generated snapshot", api.calls[-1][2]["body"])

    def test_preview_remains_draft_and_seed_convergence_closes_it(self):
        result = report()
        current = {"number": 3, "body": "<!-- schema-monitor-pr --> old", "draft": False, "node_id": "node"}
        api = FakeGitHub([current])
        m.publish_patch(Path("/tmp"), Path("/tmp"), result, api, "token", "url")
        self.assertTrue(any(c[1] == "graphql" for c in api.calls))
        result["branch"]["commit"] = result["branch"]["baseline"]
        api = FakeGitHub([current])
        m.publish_patch(Path("/tmp"), Path("/tmp"), result, api, "token", "url")
        self.assertEqual("closed", api.calls[-1][2]["state"])

    def test_patch_publication_requires_ci_triggering_credential(self):
        result = report()
        result["patch"] = True
        with self.assertRaisesRegex(RuntimeError, "App/PAT"):
            m.publish_patch(Path("/tmp"), Path("/tmp"), result, FakeGitHub([]), "", "url")

    def test_retirement_closes_only_bot_owned_previews(self):
        pulls = [dict(number=1, body="<!-- schema-monitor-pr -->", head={"ref": "schema/preview/seed_old"}),
                 dict(number=2, body="human PR", head={"ref": "schema/preview/seed_human"})]
        api = FakeGitHub(pulls)
        m.retire_previews(api, manifest())
        writes = [c for c in api.calls if c[0] == "PATCH"]
        self.assertEqual(1, len(writes))
        self.assertEqual("pulls/1", writes[0][1])

    def test_branch_names_are_stable_per_seed(self):
        first = branch()
        second = dict(first, commit="d" * 40)
        self.assertEqual(m.patch_branch(first), m.patch_branch(second))
        self.assertEqual("schema/update-stable", m.patch_branch(branch("release", "stable")))

    def test_incomplete_assessment_cannot_publish_its_patch(self):
        result = report()
        result.update(complete=False, patch=True)
        api = FakeGitHub([])
        with patch.object(m, "run", side_effect=AssertionError("No patch publication expected")):
            self.assertEqual("blocked", m.publish_patch(Path("/tmp"), Path("/tmp"), result, api, "token", "url"))

    def test_pr_without_ownership_marker_is_untouched(self):
        api = FakeGitHub([{"number": 3, "body": "Human-owned change"}])
        with self.assertRaisesRegex(ValueError, "ownership"):
            m.publish_patch(Path("/tmp"), Path("/tmp"), report(), api, "token", "url")
        self.assertEqual(["GET"], [call[0] for call in api.calls])

    def test_patch_is_applied_to_assessed_commit_and_unchanged_push_is_skipped(self):
        real_run = m.run
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            repo, remote, evidence = root / "source", root / "remote.git", root / "evidence"
            repo.mkdir()
            evidence.mkdir()
            real_run(["git", "init", "--quiet", repo])
            real_run(["git", "config", "user.name", "Test"], repo)
            real_run(["git", "config", "user.email", "test@example.invalid"], repo)
            real_run(["git", "init", "--quiet", "--bare", remote])
            source = repo / m.SCHEMA / "commands/types.gen.go"
            source.parent.mkdir(parents=True)
            source.write_text("package commands\ntype Old struct{}\n")
            (repo / ".gitmodules").write_text('[submodule "third_party/device-management"]\n path = third_party/device-management\n url = https://github.com/apple/device-management.git\n branch = release\n')
            real_run(["git", "add", "."], repo)
            real_run(["git", "update-index", "--add", "--cacheinfo", "160000," + "a" * 40 + "," + m.SUBMODULE], repo)
            real_run(["git", "commit", "--quiet", "-m", "baseline"], repo)
            result = report()
            result.update(projectCommit=real_run(["git", "rev-parse", "HEAD"], repo).strip(), patch=True)
            source.write_text("package commands\ntype Old struct{}\ntype New struct{}\n")
            real_run(["git", "add", "."], repo)
            real_run(["git", "update-index", "--add", "--cacheinfo", "160000," + "b" * 40 + "," + m.SUBMODULE], repo)
            (evidence / "candidate.patch").write_text(real_run(["git", "diff", "--cached", "--binary"], repo))
            calls = []

            def local_run(args, cwd=None, env=None, timeout=1800):
                calls.append([str(a) for a in args])
                if args[:4] == ["git", "remote", "set-url", "origin"]:
                    args = args[:4] + [remote]
                return real_run(args, cwd, env, timeout)

            class PullAPI(FakeGitHub):
                def request(self, method, endpoint, body=None):
                    self.calls.append((method, endpoint, body))
                    if endpoint == "":
                        return {"default_branch": "main"}
                    if method == "POST" and endpoint == "pulls":
                        return {"number": 42}
                    return self.pulls if method == "GET" else {}

            api = PullAPI([])
            with patch.object(m, "run", side_effect=local_run):
                self.assertEqual("passed", m.publish_patch(repo, evidence, result, api, "test-token", "url"))
                new_pr = next(c[2] for c in api.calls if c[:2] == ("POST", "pulls"))
                self.assertTrue(new_pr["draft"])
                self.assertEqual("main", new_pr["base"])
                head = m.patch_branch(result["branch"])
                self.assertEqual("b" * 40, real_run(["git", "rev-parse", "refs/heads/" + head + ":" + m.SUBMODULE], remote).strip())
                self.assertEqual(result["projectCommit"], real_run(["git", "rev-parse", "refs/heads/" + head + "^"], remote).strip())
                api.pulls = [dict(new_pr, number=42)]
                m.publish_patch(repo, evidence, result, api, "test-token", "url")
            self.assertEqual(1, sum(args[:2] == ["git", "push"] for args in calls))
            self.assertNotIn("test-token", json.dumps(calls))

    def test_publication_failure_still_raises_an_issue(self):
        result = report()
        api = FakeGitHub([])
        api.ensure_labels = lambda: None
        api.issues = lambda: []
        with tempfile.TemporaryDirectory() as tmp, patch.object(m, "GitHub", return_value=api), patch.object(m, "publish_patch", side_effect=RuntimeError("token missing")):
            root = Path(tmp)
            m.write_json(root / result["branch"]["key"] / "result.json", result)
            with self.assertRaisesRegex(RuntimeError, "publication"):
                m.publish(root, manifest(result), root, "owner/repo", False, "url")
            self.assertIn("| publication | failed |", (root / "summary.md").read_text())
        self.assertTrue(any(c[0:2] == ("POST", "issues") and "schema-automation" in c[2]["labels"] for c in api.calls))


class FakeGitHub:
    repository = "owner/repo"

    def __init__(self, pulls):
        self.pulls, self.calls = pulls, []

    def request(self, method, endpoint, body=None):
        self.calls.append((method, endpoint, body))
        return self.pulls if method == "GET" else {}


if __name__ == "__main__":
    unittest.main()
