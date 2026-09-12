import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import schema_issue_content as content
import schema_monitor as m
from schema_monitor_test import report, manifest, incident, stored_issue


def source_finding(key, evidence):
    return dict(key=key, category=key.split(":")[0], kind="review", stage="audit",
                title="Old machine title", action="Inspect the input", evidence=evidence,
                fingerprint=m.digest(evidence))


def retry_finding():
    prefix = "mdm/checkin/returntoservice.yaml#responsekeys[ReturnToService].subkeys[ShouldRetryEnrollment]."
    return source_finding("behavior-review:protocol:mdm:requesttype:ReturnToService", [
        {"path": prefix + key, "detail": "<absent> → " + value, "before": "<absent>", "after": value}
        for key, value in (("type", "<boolean>"), ("default", "false"), ("supportedOS.iOS.introduced", "27.0"))])


class EngineeringBriefTests(unittest.TestCase):
    def test_retry_is_a_bounded_verification_task_with_real_blocker_links(self):
        result = report()
        result["stages"].update({s: {"state": "blocked"} for s in ("generate", "build", "tests")})
        result["projectContext"] = {"returnToServiceResponse": True, "retryFieldPresent": False, "returnToServiceHandler": True}
        retry, blocker = retry_finding(), incident()
        result["findings"] = [retry, blocker]
        content.enrich_findings(result)
        body = m.evidence_section(retry, result, "https://example.com/run", issue_links={result["branch"]["ref"] + ":" + blocker["key"]: 91})
        visible = body.split("<details>", 1)[0]
        for expected in ("device retries enrollment", "default `false`", "true, false and omitted", "bootstrap-token", "Candidate verification blocked", "[#91]", "## Completion criteria", "does not expose this option"):
            self.assertIn(expected, visible)
        self.assertEqual(1, visible.count("ReturnToService.ShouldRetryEnrollment"))
        self.assertNotIn("responsekeys[", visible)
        self.assertEqual(2, body.count("<details>"))
        self.assertLess(len(body), 60000)

    def test_new_requirement_at_end_of_paragraph_is_visible(self):
        before = "Existing signing instructions. " * 150
        after = before + "The Apple Identity Service requires that the signing algorithm is RS256."
        item = source_finding("behavior-review:protocol-wording", [{"path": "mdm/checkin/gettoken.yaml#notes[1].content", "detail": before + " → " + after, "before": before, "after": after}])
        result = report()
        result.update(findings=[item], projectContext={"getTokenHandler": True})
        content.enrich_findings(result)
        body = m.evidence_section(item, result, "url")
        visible = body.split("<details>", 1)[0]
        self.assertIn("requires that the signing algorithm is RS256", visible)
        self.assertIn("GetTokenHandler", visible)
        self.assertNotIn(before, visible)
        self.assertIn("not demonstrated an incorrect signing implementation", visible)

    def test_optional_capability_is_not_claimed_missing_without_code_evidence(self):
        item = retry_finding()
        unknown = content.finding_brief(item, {})
        present = content.finding_brief(item, {"returnToServiceResponse": True, "retryFieldPresent": True})
        self.assertNotIn("does not expose", unknown["impact"])
        self.assertIn("already exposes", present["impact"])

    def test_availability_groups_count_objects_and_preserve_removal_meaning(self):
        evidence = [{"path": "mdm/commands/update.yaml", "detail": "payload.supportedOS.iOS.removed: <absent> → 27.0", "before": "<absent>", "after": "27.0"},
                    {"path": "mdm/commands/update.yaml", "detail": "payload.supportedOS.macOS.removed: <absent> → 27.0", "before": "<absent>", "after": "27.0"},
                    {"path": "mdm/commands/update.yaml", "detail": "payloadkeys[Old].supportedOS.iOS.removed: 18.0 → <absent>", "before": "18.0", "after": "<absent>"}]
        groups = content.availability_groups(evidence)
        self.assertEqual(["Removal boundaries", "Removed metadata or fields"], [g["category"] for g in groups])
        self.assertEqual([1, 1], [g["objects"] for g in groups])

    def test_parser_failures_and_scope_decisions_have_different_acceptance(self):
        for suffix in ("schemagen.Schema:examples", "schemagen.ReasonDetail:valuetype"):
            item = source_finding("schema-format:field:" + suffix, [{"path": "mdm/a.yaml", "detail": "unknown field"}])
            value = content.finding_brief(item, {})
            self.assertEqual("Confirmed parsing failure", value["classification"])
            self.assertTrue(any("strict" in step.lower() for step in value["requiredWork"]))
        item = source_finding("upstream-input:area:openapi", [{"path": "openapi/content-cache/metrics_report.json", "detail": "new input"}])
        value = content.finding_brief(item, {})
        self.assertEqual("Support decision", value["classification"])
        self.assertIn("inclusion or exclusion", value["requiredWork"][1])

    def test_project_links_reference_existing_files(self):
        root = Path(__file__).resolve().parents[2]
        self.assertTrue(content.project_context(root)["returnToServiceHandler"])
        for key, path in (("schema-format:field:schemagen.Schema:examples", "a.yaml"),
                          ("schema-format:field:schemagen.ReasonDetail:valuetype", "a.yaml"),
                          ("behavior-review:availability", "a.yaml"),
                          ("behavior-review:new-commands", "mdm/commands/trigger.enhanced.log.collection.yaml"),
                          ("upstream-input:area:openapi", "openapi/content-cache/metrics_report.json")):
            value = content.finding_brief(source_finding(key, [{"path": path, "detail": "new"}]), {})
            for reference in value["references"]:
                if reference["source"] == "project":
                    self.assertTrue((root / reference["path"]).is_file(), reference)
        with tempfile.TemporaryDirectory() as tmp:
            self.assertFalse(content.project_context(Path(tmp))["returnToServiceResponse"])


class PresentationMigrationTests(unittest.TestCase):
    def test_old_title_and_body_migrate_once_without_changing_identity(self):
        result, item = report(), incident()
        result["findings"] = [item]
        issue = stored_issue(result, item)
        old = m.metadata(issue["body"])
        old.pop("presentationVersion")
        old.pop("presentation")
        issue["body"] = m.MARKER.sub("<!-- schema-monitor " + json.dumps(old) + " -->", issue["body"], count=1)
        issue["title"] = "Old title"
        actions = m.issue_actions([issue], [result], manifest(result), "url")
        self.assertEqual(1, len(actions))
        self.assertEqual("PATCH", actions[0][0])
        updated = dict(issue, **actions[0][2])
        self.assertEqual(old["key"], m.metadata(updated["body"])["key"])
        self.assertEqual(old["fingerprint"], m.metadata(updated["body"])["fingerprint"])
        self.assertTrue(updated["body"].startswith("Engineer preface"))
        self.assertTrue(updated["body"].endswith("Engineer conclusion"))
        self.assertEqual([], m.issue_actions([updated], [result], manifest(result), "another-run"))

    def test_wording_change_does_not_reopen_maintainer_closed_issue(self):
        result, item = report(), incident()
        result["findings"] = [item]
        issue = stored_issue(result, item, state="closed")
        changed = copy.deepcopy(item)
        changed["brief"] = content.finding_brief(item, {})
        changed["brief"]["title"] = "Clearer wording"
        result["findings"] = [changed]
        actions = m.issue_actions([issue], [result], manifest(result), "url")
        self.assertEqual(1, len(actions))
        self.assertNotIn("state", actions[0][2])
        self.assertIn("Clearer wording", actions[0][2]["title"])

    def test_review_evidence_migration_preserves_closure_for_same_apple_snapshot(self):
        result, item = report(), retry_finding()
        result["findings"] = [item]
        issue = stored_issue(result, item, state="closed")
        old = m.metadata(issue["body"])
        old.pop("presentationVersion")
        old.pop("presentation")
        old["fingerprint"] = "legacy-evidence-format"
        issue["body"] = m.MARKER.sub("<!-- schema-monitor " + json.dumps(old) + " -->", issue["body"], count=1)
        actions = m.issue_actions([issue], [result], manifest(result), "url")
        self.assertEqual(1, len(actions))
        self.assertNotIn("state", actions[0][2])
        updated = dict(issue, **actions[0][2])
        self.assertEqual([], m.issue_actions([updated], [result], manifest(result), "another-run"))
        result["branch"]["commit"] = "new-apple-snapshot"
        actions = m.issue_actions([issue], [result], manifest(result), "url")
        self.assertEqual("open", actions[0][2]["state"])

    def test_checked_tasks_and_notes_survive_body_refresh(self):
        body = "Notes before\n" + m.START + "\n- [x] Verify plist output\n" + m.END + "\nNotes after"
        section = m.START + "\n- [ ] Verify plist output\n- [ ] New task\n" + m.END
        updated = m.replace_section(body, section)
        self.assertIn("- [x] Verify plist output", updated)
        self.assertIn("- [ ] New task", updated)
        self.assertTrue(updated.endswith("Notes after"))

    def test_blockers_are_linked_on_first_publication(self):
        result, blocker, retry = report(), incident(), retry_finding()
        result["findings"] = [retry, blocker]
        result["stages"]["tests"]["state"] = "blocked"
        content.enrich_findings(result)
        calls = []

        class API:
            repository = "owner/repo"
            def ensure_labels(self): pass
            def issues(self): return []
            def request(self, method, endpoint, body=None):
                calls.append((method, endpoint, body))
                return dict(body, number=90 + len(calls), state="open") if endpoint == "issues" else {}

        with tempfile.TemporaryDirectory() as tmp, patch.object(m, "GitHub", return_value=API()), patch.object(m, "publish_patch", return_value="not-applicable"), patch.object(m, "retire_previews"):
            root = Path(tmp)
            m.write_json(root / result["branch"]["key"] / "result.json", result)
            m.publish(root, manifest(result), root, "owner/repo", False, "url")
        self.assertEqual(2, len(calls))
        self.assertEqual("failure", m.metadata(calls[0][2]["body"])["kind"])
        self.assertIn("[#91](https://github.com/owner/repo/issues/91)", calls[1][2]["body"])


if __name__ == "__main__":
    unittest.main()
