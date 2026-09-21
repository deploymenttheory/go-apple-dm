"""Failure-contract tests for the offline documentation reference checker."""

import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location("check_docs", Path(__file__).with_name("check-docs.py"))
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class DocumentationLinks(unittest.TestCase):
    """Exercise failures without relying on network state or the repository contents."""

    def setUp(self):
        """Provide a private documentation tree for each test."""
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        subprocess.run(["git", "init", "--quiet", str(self.root)], check=True, capture_output=True)
        self.checker = MODULE.Checker(self.root)

    def write(self, name, text):
        """Write only to this test's temporary tree and return the fixture path."""
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)
        subprocess.run(["git", "add", "--", name], cwd=self.root, check=True, capture_output=True)
        return path

    def test_local_targets_and_duplicate_heading_anchors(self):
        """Missing files and fragments fail; repeated and formatted headings resolve."""
        self.write("docs/page.md", "# One\n## `Two`\n## Two\n")
        self.checker.local_link("README.md:4", "docs/page.md#two-1")
        self.assertFalse(self.checker.errors)
        self.checker.local_link("README.md:5", "docs/page.md#missing")
        self.checker.local_link("README.md:6", "docs/absent.md")
        self.assertEqual(len(self.checker.errors), 2)

    def test_link_forms_and_fenced_examples(self):
        """Inline, image, reference-definition and HTML links are checked, examples are not."""
        text = ('[a](a.md)\n![b](b.png)\n[x]: c.md\n<a href="d.md">d</a>\n'
                '```md\n[example](nonexistent.md)\n```\n')
        self.assertEqual([url for _, url in MODULE.local_links(text)], ["a.md", "b.png", "c.md", "d.md"])

    def test_directory_targets_require_tracked_contents(self):
        """Local leftovers must not make directory links pass before a clean checkout."""
        (self.root / "empty").mkdir()
        (self.root / "untracked").mkdir()
        (self.root / "untracked/notes.md").write_text("local only\n")
        self.write("tracked/nested/page.md", "# Tracked\n")
        self.checker.local_link("README.md:1", "tracked")
        self.checker.local_link("README.md:2", "tracked/nested/")
        self.assertFalse(self.checker.errors)
        for directory in ("empty", "untracked"):
            self.checker.local_link("README.md:3", directory)
            self.checker.code_link("doc", MODULE.REPOSITORY + "/tree/main/" + directory)
        self.assertEqual(len(self.checker.errors), 4)
        self.assertTrue(all("not tracked by Git" in error for error in self.checker.errors))

    def test_untracked_and_ignored_file_targets_fail(self):
        """Files missing from the Git index cannot validate local or current code links."""
        self.write(".gitignore", "ignored.go\n")
        for name in ("untracked.go", "ignored.go"):
            (self.root / name).write_text("package local\n")
            self.checker.local_link("README.md:1", name)
            self.checker.code_link("doc", MODULE.REPOSITORY + "/blob/main/" + name)
        self.assertEqual(len(self.checker.errors), 4)
        self.assertTrue(all("not tracked by Git" in error for error in self.checker.errors))

    def test_linked_image_checks_its_outer_destination(self):
        """A valid badge image must not conceal a missing enclosing license link."""
        self.write("image.svg", "<svg/>")
        self.write("README.md", "[![License](image.svg)](LICENSE)\n")
        self.checker.markdown("README.md")
        self.assertEqual(self.checker.errors, ["README.md:1: missing local target: LICENSE"])

    def test_pinned_lines_use_the_pinned_file(self):
        """A valid working file cannot make an invalid historical line range pass."""
        self.write("service.go", "line\n" * 100)
        self.checker.git_object = lambda revision, path: "line\n" * 3
        url = MODULE.REPOSITORY + "/blob/" + "a" * 40 + "/service.go"
        self.checker.code_link("diagram", url + "#L2-L3", require_pin=True)
        self.assertFalse(self.checker.errors)
        for fragment in ("#L4", "#L3-L2", "#L0", "#L2-other"):
            self.checker.code_link("diagram", url + fragment, require_pin=True)
        self.assertEqual(len(self.checker.errors), 4)

    def test_mutable_code_and_missing_current_path(self):
        """Diagram implementation links cannot float, and ordinary links must exist."""
        url = MODULE.REPOSITORY + "/blob/main/missing.go"
        self.checker.code_link("diagram", url, require_pin=True)
        self.checker.code_link("doc", url)
        self.assertEqual(len(self.checker.errors), 2)

    def test_internal_diagram_components_require_evidence(self):
        """An external actor is exempt; an internal service needs a source reference."""
        name = "docs/diagrams/src/flow.architecture.json"
        spec = {"meta": {}, "components": [{"id": "actor", "type": "external"}, {"id": "core", "type": "backend"}]}
        self.write(name, json.dumps(spec))
        self.write("docs/diagrams/flow.html", "<html></html>")
        self.checker.diagram(name)
        self.assertEqual(len(self.checker.errors), 1)
        self.assertIn("core", self.checker.errors[0])

    def test_source_links_must_survive_delivery(self):
        """A source-only repair cannot pass while the delivered artifact remains stale."""
        revision = "b" * 40
        name = "docs/diagrams/src/core.architecture.json"
        spec = {"meta": {"repository": {"url": MODULE.REPOSITORY, "revision": revision}},
                "components": [{"id": "core", "type": "backend", "sources": [{"path": "service.go", "line": 1}]}]}
        self.write(name, json.dumps(spec))
        self.write("docs/diagrams/core.html", "<html></html>")
        self.checker.git_object = lambda revision, path: "package service\n"
        self.checker.diagram(name)
        self.assertTrue(any("absent from delivered HTML" in x for x in self.checker.errors))

    def test_empty_html_cannot_pass_delivery(self):
        """An existing but empty artifact is not a delivered diagram."""
        name = "docs/diagrams/src/core.architecture.json"
        spec = {"meta": {}, "components": [{"id": "actor", "type": "external"}]}
        self.write(name, json.dumps(spec))
        self.write("docs/diagrams/core.html", "")
        self.checker.diagram(name)
        self.assertTrue(any("HTML is empty" in x for x in self.checker.errors))

    def test_nonarchitecture_sources_require_implementation_cards(self):
        """A vendor-only reference does not establish how this repository implements a flow."""
        name = "docs/diagrams/src/flow.workflow.json"
        self.write(name, json.dumps({"meta": {}, "nodes": [], "cards": [{"items": ["https://developer.apple.com/documentation/devicemanagement"]}]}))
        self.write("docs/diagrams/flow.html", "https://developer.apple.com/documentation/devicemanagement")
        self.checker.diagram(name)
        self.assertTrue(any("implementation card" in x for x in self.checker.errors))

    def test_implementation_cards_cover_individual_nodes(self):
        """A generic repository link cannot stand in for a code-backed node's evidence."""
        name = "docs/diagrams/src/flow.workflow.json"
        url = MODULE.REPOSITORY + "/blob/" + "c" * 40 + "/flow.go"
        spec = {"nodes": [{"id": "capture", "type": "backend"}],
                "cards": [{"items": ["Implementation — " + url]}]}
        self.write(name, json.dumps(spec))
        self.write("docs/diagrams/flow.html", url)
        self.checker.git_object = lambda revision, path: "package flow\n"
        self.checker.diagram(name)
        self.assertTrue(any("capture needs" in x for x in self.checker.errors))
        self.checker.errors.clear()
        spec["cards"][0]["items"] = ["capture: Observe exchange — " + url]
        self.write(name, json.dumps(spec))
        self.checker.diagram(name)
        self.assertFalse(self.checker.errors)

    def test_documentation_links_are_not_implementation_evidence(self):
        """Neither mutable nor pinned guide links can replace a node's code reference."""
        name = "docs/diagrams/src/flow.workflow.json"
        self.write("docs/guide.md", "# Guide\n")
        self.checker.git_object = lambda revision, path: "# Guide\n"
        for revision in ("main", "d" * 40):
            with self.subTest(revision=revision):
                self.checker.errors.clear()
                url = MODULE.REPOSITORY + "/blob/" + revision + "/docs/guide.md"
                spec = {"nodes": [{"id": "core", "type": "backend"}],
                        "cards": [{"items": ["core: Guide — " + url]}]}
                self.write(name, json.dumps(spec))
                self.write("docs/diagrams/flow.html", url)
                self.checker.diagram(name)
                self.assertTrue(any("core needs" in x for x in self.checker.errors))


if __name__ == "__main__":
    unittest.main()
