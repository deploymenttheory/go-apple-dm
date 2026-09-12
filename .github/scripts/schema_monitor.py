#!/usr/bin/env python3
"""Assess immutable Apple schema snapshots and publish deduplicated evidence.

Discovery and assessment have no GitHub write operations. Publication consumes
reports and restricted patches; it never executes generated candidate code.
"""
import argparse
import hashlib
import html
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
from urllib.parse import quote, urlencode

from schema_issue_content import changed_fragments, enrich_findings, evidence_values, finding_brief, project_context

ROOT = Path(__file__).resolve().parents[2]
UPSTREAM = "https://github.com/apple/device-management.git"
LIBRARY = "github.com/deploymenttheory/go-apple-dm"
SCHEMA = "devicemanagement/schema"
SUBMODULE = "third_party/device-management"
STAGES = ("snapshot", "audit", "parse", "generate", "verify", "api", "build", "boundaries", "tests")
SHA = re.compile(r"^[0-9a-f]{40}$")
MARKER = re.compile(r"<!-- schema-monitor (\{.*?\}) -->")
START, END = "<!-- schema-monitor:evidence:start -->", "<!-- schema-monitor:evidence:end -->"
PRESENTATION_VERSION = 2


def run(args, cwd=None, env=None, timeout=1800):
    return subprocess.run([str(a) for a in args], cwd=cwd, env=env, timeout=timeout,
                          check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def write_json(path, value):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


def parse_refs(text):
    default, heads = None, {}
    for line in text.splitlines():
        value, name = line.split("\t", 1)
        if value.startswith("ref: refs/heads/") and name == "HEAD":
            default = value.removeprefix("ref: refs/heads/")
        elif name.startswith("refs/heads/") and SHA.fullmatch(value):
            heads[name.removeprefix("refs/heads/")] = value
    if default not in heads or default.startswith("seed"):
        raise ValueError("Apple's stable default branch is absent or is a seed branch")
    return default, heads


def discover(repo, output, upstream=UPSTREAM):
    manifest = {"schemaVersion": 1, "complete": False, "branches": [], "upstream": upstream}
    try:
        manifest["projectCommit"] = run(["git", "rev-parse", "HEAD"], repo).strip()
        default, heads = parse_refs(run(["git", "ls-remote", "--symref", upstream, "HEAD", "refs/heads/*"]))
        pinned = run(["git", "rev-parse", "HEAD:" + SUBMODULE], repo).strip()
        manifest["stableRef"], manifest["stableCommit"] = default, heads[default]
        for branch in [default] + sorted(b for b in heads if b.startswith("seed")):
            stable = branch == default
            manifest["branches"].append({
                "key": digest(branch)[:16], "ref": branch, "commit": heads[branch],
                "baseline": pinned if stable else heads[default],
                "baselineRef": "project-pin" if stable else default,
                "kind": "stable" if stable else "seed"})
        manifest["complete"] = True
    except (subprocess.SubprocessError, OSError, ValueError) as exc:
        manifest["error"] = failure_text(exc)
    write_json(output, manifest)
    return manifest


def failure_text(exc):
    if isinstance(exc, subprocess.CalledProcessError):
        return (exc.stderr or exc.stdout or str(exc))[-6000:]
    return str(exc)


def finding(category, kind, stage, subject, title, action, evidence):
    return {"key": category + ":" + subject, "category": category, "kind": kind,
            "stage": stage, "title": title, "action": action, "evidence": evidence,
            "fingerprint": digest(evidence)}


def command_stage(result, stage, args, cwd, directory, env=None):
    try:
        process = subprocess.run([str(a) for a in args], cwd=cwd, env=env, timeout=1800,
                                 text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        text, code = process.stdout, process.returncode
    except (subprocess.SubprocessError, OSError) as exc:
        text, code = failure_text(exc), -1
    (directory / (stage + ".log")).write_text(text)
    result["stages"][stage] = {"state": "passed" if code == 0 else "failed", "exitCode": code,
                               "command": [str(a) for a in args], "log": stage + ".log"}
    return code == 0, text


def assert_snapshot(root, expected, project):
    actual = run(["git", "rev-parse", "HEAD"], root / SUBMODULE).strip()
    if actual != expected or run(["git", "rev-parse", "HEAD"], root).strip() != project:
        raise ValueError("Candidate or project SHA changed during assessment")


def api_findings(report):
    groups = {}
    for name in report["removed"]:
        groups.setdefault(name.split("/", 1)[0], []).append({"path": name, "detail": "Exported declaration is no longer generated"})
    for change in report["changed"]:
        groups.setdefault(change["name"].split("/", 1)[0], []).append(
            {"path": change["name"], "detail": change["before"] + " → " + change["after"]})
    return [finding("public-api", "failure", "api", package,
                    "Generated " + package + " API is incompatible with the project pin",
                    "Preserve public names/signatures or propose an intentional migration. Do not automatically authorize removals.", evidence)
            for package, evidence in sorted(groups.items())]


def assess(repo, manifest, branch, directory):
    directory.mkdir(parents=True, exist_ok=True)
    result = {"schemaVersion": 1, "projectCommit": manifest["projectCommit"], "branch": branch,
              "complete": False, "patch": False, "findings": [],
              "stages": {name: {"state": "blocked"} for name in STAGES}}
    try:
        for value in (branch["commit"], branch["baseline"], manifest["projectCommit"]):
            if not SHA.fullmatch(value):
                raise ValueError("Assessment requires full commit SHAs")
        with tempfile.TemporaryDirectory(prefix="dm-schema-assessment-") as scratch:
            # Go workspace matching requires one physical spelling of the path
            # (macOS /var and /private/var can otherwise refer to the same tree).
            scratch = Path(scratch).resolve()
            root, apple, baseline = scratch / "project", scratch / "apple", scratch / "baseline"
            run(["git", "clone", "--quiet", "--shared", repo, root])
            run(["git", "checkout", "--quiet", "--detach", manifest["projectCommit"]], root)
            run(["git", "clone", "--quiet", "--no-checkout", manifest["upstream"], apple])
            run(["git", "clone", "--quiet", "--shared", apple, baseline])
            run(["git", "checkout", "--quiet", "--detach", branch["baseline"]], baseline)
            run(["git", "clone", "--quiet", "--shared", apple, root / SUBMODULE])
            run(["git", "checkout", "--quiet", "--detach", branch["commit"]], root / SUBMODULE)
            assert_snapshot(root, branch["commit"], manifest["projectCommit"])
            result["stages"]["snapshot"] = {"state": "passed"}
            result["projectContext"] = project_context(root)
            old_api = scratch / "published-api"
            shutil.copytree(root / SCHEMA, old_api)
            tool = scratch / "schemagen"
            run(["go", "build", "-o", tool, "./cmd/schemagen"], root)
            base_args = [tool, "-schema", root / SUBMODULE, "-ref", branch["ref"]]
            ok, text = command_stage(result, "audit", base_args + ["-baseline", baseline, "-report", directory, "audit"], root, directory)
            if not ok:
                raise ValueError("The schema audit did not produce a complete report")
            audit = json.loads(text)
            if audit["candidateCommit"] != branch["commit"] or audit["baselineCommit"] != branch["baseline"]:
                raise ValueError("Audit provenance does not match discovered commits")
            result["findings"].extend(audit["findings"])
            result["changes"] = audit["changes"]
            result["counts"] = {"baseline": audit["baselineCount"], "candidate": audit["candidateCount"]}
            result["stages"]["parse"] = {"state": "passed" if audit["parsePassed"] else "failed"}
            if audit["parsePassed"]:
                assess_generated(result, base_args, baseline, old_api, tool, root, directory)
            assert_snapshot(root, branch["commit"], manifest["projectCommit"])
            result["complete"] = True
    except (subprocess.SubprocessError, OSError, ValueError, KeyError) as exc:
        result["findings"].append(finding("automation", "failure", "snapshot", "assessment",
            "Apple schema assessment could not complete", "Repair the reported workflow failure and rerun this immutable snapshot.",
            [{"path": branch["ref"], "detail": failure_text(exc)}]))
        result["stages"]["snapshot"] = {"state": "failed"}
    enrich_findings(result)
    write_json(directory / "result.json", result)
    (directory / "summary.md").write_text(render_report(result))
    return result


def assess_generated(result, base_args, baseline, old_api, tool, root, directory):
    ok, text = command_stage(result, "generate", base_args + ["-out", root / SCHEMA, "generate"], root, directory)
    assert_snapshot(root, result["branch"]["commit"], result["projectCommit"])
    if not ok:
        result["findings"].append(finding("schema-format", "failure", "generate", "generation",
            "schemagen cannot generate the Apple candidate", "Reproduce the generation failure and correct the generator in a separate PR.",
            [{"path": "generate.log", "detail": text[-5000:]}]))
        return
    # Provenance and local make commands on the preview must name the seed.
    run(["git", "config", "--file", ".gitmodules", "submodule.third_party/device-management.branch", result["branch"]["ref"]], root)
    command_stage(result, "verify", base_args + ["-out", root / SCHEMA, "verify"], root, directory)
    ok, text = command_stage(result, "api", [tool, "-ref", result["branch"]["ref"], "-baseline", old_api, "-schema", root / SCHEMA, "api-diff"], root, directory)
    if ok:
        api = json.loads(text)
        write_json(directory / "api.json", api)
        changes = api_findings(api)
        result["findings"].extend(changes)
        if changes:
            result["stages"]["api"]["state"] = "failed"
    env = os.environ.copy()
    env["GOWORK"] = str(root / "go.work")
    selected = json.loads(run(["go", "list", "-m", "-json", LIBRARY], root / "server", env))
    if Path(selected["Dir"]).resolve() != root.resolve():
        raise ValueError("Server tests do not resolve the candidate library")
    ok, _ = command_stage(result, "build", ["go", "build", "./...", "./server/..."], root, directory, env)
    if ok:
        cases_ok, probe_cases = command_stage(result, "boundaries", base_args + ["-baseline", baseline, "boundaries"], root, directory)
        if cases_ok:
            (directory / "boundaries.json").write_text(probe_cases)
            # Go ignores .txt in source discovery; compile the probe only here.
            probe = directory / "support-probe.go.txt"
            shutil.copyfile(root / ".github/scripts/schema_support_probe.go.txt", probe)
            source = root.parent / "support-probe.go"
            shutil.copyfile(probe, source)
            command_stage(result, "boundaries", ["go", "run", source, directory / "boundaries.json"], root, directory, env)
        command_stage(result, "tests", ["go", "test", "-race", "-count=1", "./devicemanagement/schema/...", "./devicemanagement/mdmprotocol/...", "./server/service", "./server/ddmadapter/...", "-json"], root, directory, env)
    for stage in ("verify", "api", "build", "boundaries", "tests"):
        if result["stages"][stage]["state"] == "failed" and not any(f["stage"] == stage for f in result["findings"]):
            result["findings"].append(finding("runtime" if stage in ("build", "boundaries", "tests") else "public-api", "failure", stage, stage,
                "Apple candidate fails " + stage + " checks", "Reproduce the failing check using the recorded source commits and log; repair or explicitly review the incompatibility.",
                stage_evidence(stage, (directory / (stage + ".log")).read_text())))
    assert_snapshot(root, result["branch"]["commit"], result["projectCommit"])
    run(["git", "add", "--", ".gitmodules", SUBMODULE, SCHEMA], root)
    names = run(["git", "diff", "--cached", "--name-only"], root).splitlines()
    if any(not allowed_path(name) for name in names):
        raise ValueError("Candidate patch contains unexpected paths")
    if names:
        patch = run(["git", "diff", "--cached", "--binary"], root)
        (directory / "candidate.patch").write_text(patch)
        result["patch"] = True



def stage_evidence(stage, text):
    failures = []
    if stage == "tests":
        for line in text.splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Action") == "fail":
                failures.append({"path": event.get("Package", "") + "/" + event.get("Test", "package"), "detail": "Go reports failure; see tests.log for the complete output."})
    if failures:
        return sorted(failures, key=lambda item: item["path"])
    text = re.sub(r"/[^ \n]*/dm-schema-assessment-[^/ \n]+", "$ASSESSMENT", text)
    return [{"path": stage + ".log", "detail": text[-5000:]}]

def allowed_path(name):
    return name in (".gitmodules", SUBMODULE) or (name.startswith(SCHEMA + "/") and
        (name.endswith(".gen.go") or name.endswith("conformance_gen_test.go") or
         name in (SCHEMA + "/GENERATED_FROM.json", SCHEMA + "/EXPORTED_IDENTIFIERS.lock")))


def render_report(result):
    branch = result["branch"]
    text = ["Apple schema " + branch["kind"] + " assessment: `" + branch["ref"] + "`", "",
            "Project: `" + result["projectCommit"] + "`", "",
            "Apple baseline: `" + branch["baseline"] + "`; candidate: `" + branch["commit"] + "`.", "",
            "| Stage | Result |", "|---|---|"]
    names = list(STAGES) + [name for name in result["stages"] if name not in STAGES]
    text += ["| " + name + " | " + result["stages"][name]["state"] + " |" for name in names]
    text += ["", "Passing checks establish the listed scenarios, not complete OS or real-device support.", ""]
    for item in result["findings"]:
        text += ["- **" + item["title"] + "** (" + item["kind"] + "): " + item["action"]]
    text += ["", "Generated preview available: **" + str(result["patch"]).lower() + "**.", ""]
    return "\n".join(text)


class GitHub:
    """REST operations with structured bodies; no shell interpolation."""
    def __init__(self, repository, token=None):
        self.repository = repository
        self.env = os.environ.copy()
        if token:
            self.env["GH_TOKEN"] = token

    def request(self, method, endpoint, body=None):
        route = "graphql" if endpoint == "graphql" else ("repos/" + self.repository + ("/" + endpoint if endpoint else ""))
        args = ["gh", "api", "--method", method, route]
        if body is not None:
            args += ["--input", "-"]
        completed = subprocess.run(args, input=json.dumps(body) if body is not None else None,
                                   text=True, capture_output=True, env=self.env, timeout=120)
        if completed.returncode:
            raise RuntimeError("GitHub " + method + " " + endpoint + " failed: " + completed.stderr[-2000:])
        return json.loads(completed.stdout) if completed.stdout.strip() else None

    def issues(self):
        result, page = [], 1
        while True:
            batch = self.request("GET", "issues?state=all&labels=schema-monitor&per_page=100&page=" + str(page))
            result.extend(i for i in batch if "pull_request" not in i)
            if len(batch) < 100:
                return result
            page += 1

    def ensure_labels(self):
        existing, page = set(), 1
        while True:
            labels = self.request("GET", "labels?per_page=100&page=" + str(page))
            existing.update(label["name"] for label in labels)
            if len(labels) < 100:
                break
            page += 1
        for name, color in (("schema-monitor", "0366d6"), ("schema-gap", "b60205"),
                            ("schema-review", "fbca04"), ("schema-automation", "d93f0b"),
                            ("schema-preview", "5319e7"), ("schema-update", "0366d6")):
            if name not in existing:
                self.request("POST", "labels", {"name": name, "color": color})


def metadata(body):
    match = MARKER.search(body or "")
    if not match:
        return None
    try:
        meta = json.loads(match.group(1))
        required = ("key", "branch", "fingerprint", "stage", "kind")
        return meta if isinstance(meta, dict) and all(isinstance(meta.get(k), str) for k in required) else None
    except ValueError:
        return None


def inline_code(value):
    value = str(value).replace("\n", " ")
    fence = "`" * (max((len(m[0]) for m in re.finditer(r"`+", value)), default=0) + 1)
    return fence + " " + value + " " + fence


def reference_link(reference, result, repository):
    apple = reference.get("source") == "apple"
    commit = result["branch"]["commit"] if apple else result["projectCommit"]
    repo = "apple/device-management" if apple else repository
    path = reference["path"].split("#", 1)[0]
    url = "https://github.com/" + repo + "/blob/" + quote(commit, safe="") + "/" + quote(path, safe="/")
    label = reference.get("label", path).replace("[", "\\[").replace("]", "\\]")
    return "[" + html.escape(label, quote=False) + "](" + url + ")"


def issue_title(item, result):
    content = item.get("brief") or finding_brief(item, result.get("projectContext", {}))
    return "[Apple " + result["branch"]["ref"] + "] " + content["title"]


def evidence_section(item, result, run_url, status="observed", issue_links=None, repository="deploymenttheory/go-apple-dm"):
    content = item.get("brief") or finding_brief(item, result.get("projectContext", {}))
    branch, issue_links = result["branch"], issue_links or {}
    blockers = []
    if content["needsCandidate"]:
        for other in result["findings"]:
            if other["kind"] == "failure" and other["stage"] in ("parse", "generate"):
                key = branch["ref"] + ":" + other["key"]
                label = other.get("brief", {}).get("title", other["title"])
                number = issue_links.get(key)
                blockers.append("[#" + str(number) + "](https://github.com/" + repository + "/issues/" + str(number) + ") — " + label if number else label)
    blocked = content["needsCandidate"] and any(result["stages"].get(s, {}).get("state") == "blocked" for s in ("generate", "build", "tests"))
    state = "Candidate verification blocked" if blocked else ("Check failed" if item["kind"] == "failure" else "Engineer assessment required")
    if status != "observed":
        state = {"verified": "Relevant check passed", "inactive": "Apple branch retired; not verified fixed"}.get(status, status)
    meta = {"key": branch["ref"] + ":" + item["key"], "branch": branch["ref"],
            "fingerprint": item["fingerprint"], "stage": item["stage"], "kind": item["kind"],
            "status": status, "project": result["projectCommit"], "candidate": branch["commit"],
            "presentationVersion": PRESENTATION_VERSION,
            "presentation": digest({"version": PRESENTATION_VERSION, "content": content, "blockers": blockers, "blocked": blocked})}
    text = [START, "<!-- schema-monitor " + json.dumps(meta, sort_keys=True) + " -->", "",
            "**Type:** " + content["classification"], "", "**Status:** " + state, "",
            "## What changed", "", content["summary"], "", "## Project impact", "", content["impact"], "",
            "## Required work", ""]
    text += ["- [ ] " + task for task in content["requiredWork"]]
    text += ["", "## Completion criteria", ""]
    text += ["- " + criterion for criterion in content["completionCriteria"]]
    if blocked:
        text += ["", "## Blockers", "", "Build and runtime checks have not run against the unmodified candidate.", ""]
        text += ["- " + blocker for blocker in blockers] or ["- Complete the blocked assessment stages before claiming candidate compatibility."]
    if content.get("changes"):
        text += ["", "## Relevant change", "", "| Setting | Baseline | Candidate |", "|---|---|---|"]
        for change in content["changes"]:
            text.append("| " + " | ".join(html.escape(change[k]).replace("|", "\\|") for k in ("subject", "before", "after")) + " |")
    if content.get("groups"):
        text += ["", "## Changes to check", "", "Counts are distinct schema objects per group, not individual YAML properties.", "",
                 "| Group | Objects | Files |", "|---|---:|---:|"]
        for group in content["groups"]:
            text.append("| " + group["category"] + " | " + str(group["objects"]) + " | " + str(len(group["files"])) + " |")
        for group in content["groups"]:
            if group["category"] in ("Removal boundaries", "Deprecation boundaries"):
                text += ["", "**" + group["category"] + " — start here:**", ""]
                text += ["- " + reference_link({"source": "apple", "path": path}, result, repository) for path in group["files"]]
    # Prose evidence shows the changed clauses before ancillary metadata. Never
    # crop the beginning of a paragraph and discard its new requirement.
    if item["key"] == "behavior-review:protocol-wording":
        text += ["", "## Changed requirement", ""]
        for evidence in item["evidence"]:
            before, after = evidence_values(evidence)
            text += [reference_link({"source": "apple", "path": evidence["path"]}, result, repository), ""]
            for old, new in changed_fragments(before, after):
                text += ["- **Before:** " + html.escape(old), "- **After:** " + html.escape(new)]
            text.append("")
    text += ["", "## Where to work", ""]
    text += ["- " + reference_link(reference, result, repository) for reference in content["references"]]
    source_files = sorted({e["path"].split("#", 1)[0] for e in item["evidence"] if e["path"].split("#", 1)[0].endswith((".yaml", ".json"))})
    if len(source_files) <= 3:
        text += ["- " + reference_link({"source": "apple", "path": path, "label": "Apple: " + path}, result, repository) for path in source_files]
    if not content["references"]:
        text += ["- Use the failing check and source locations in the evidence below."]
    total = len(item["evidence"])
    text += ["", "<details>", "<summary>Source evidence (" + str(total) + " locations)</summary>", ""]
    size, shown = 0, 0
    for evidence in item["evidence"]:
        location = evidence["path"]
        file = location.split("#", 1)[0]
        link = reference_link({"source": "apple", "path": file}, result, repository) if file.endswith((".yaml", ".json")) else inline_code(location)
        lines = ["- " + link]
        if "#" in location:
            lines.append("  - Field: " + inline_code(location.split("#", 1)[1]))
        elif "before" in evidence and ": " in evidence["detail"]:
            lines.append("  - Field: " + inline_code(evidence["detail"].split(": ", 1)[0]))
        before, after = evidence_values(evidence)
        if before:
            for old, new in changed_fragments(before, after):
                lines += ["  - Before: " + html.escape(old), "  - After: " + html.escape(new)]
        else:
            lines += ["  - " + html.escape(after)]
        if evidence.get("context"):
            lines += ["  - Meaning: " + html.escape(evidence["context"])]
        entry = "\n".join(lines)
        if size + len(entry) > 20000:
            break
        size += len(entry)
        shown += 1
        text.append(entry)
    if shown < total:
        text += ["", "Showing " + str(shown) + " of " + str(total) + " locations. Full evidence is in `audit.json` in the assessment artifacts."]
    text += ["", "</details>", "", "<details>", "<summary>Assessment and reproduction</summary>", "",
             "Apple branch: " + inline_code(branch["ref"]) + ".", "",
             "Baseline: " + inline_code(branch["baseline"]) + "; candidate: " + inline_code(branch["commit"]) + ".", "",
             "Project: " + inline_code(result["projectCommit"]) + ".", "",
             "| Stage | Result |", "|---|---|"]
    text += ["| " + stage + " | " + result["stages"][stage]["state"] + " |" for stage in STAGES]
    text += ["", "[Assessment run and artifacts](" + run_url + ")." if run_url else "Full evidence is in the retained assessment artifacts.", "",
             "For the same historical inputs, check out the recorded project commit and download this run's `schema-discovery` artifact. Use that discovery.json; running discovery again selects current Apple commits.", "",
             "```sh", "python3 .github/scripts/schema_monitor.py assess \\",
             "  --manifest /path/to/downloaded/discovery.json \\",
             "  --key " + branch["key"] + " --output /tmp/schema-report", "```", "", "</details>", "", END]
    return "\n".join(text)


def replace_section(body, section):
    # Keep checked tasks when their instruction text is unchanged.
    for task in re.findall(r"(?m)^- \[[xX]\] (.+)$", body):
        section = section.replace("- [ ] " + task + "\n", "- [x] " + task + "\n")
    if START in body and END in body:
        left = body.index(START)
        right = body.index(END, left) + len(END)
        return body[:left] + section + body[right:]
    return body.rstrip() + "\n\n" + section


def issue_actions(existing, reports, manifest, run_url, repository="deploymenttheory/go-apple-dm"):
    """Pure reconciliation: a blocked stage can never resolve its failures."""
    indexed = {}
    for issue in existing:
        meta = metadata(issue.get("body", ""))
        if meta:
            indexed[meta["key"]] = (issue, meta)
    actions, observed = [], set()
    issue_links = {key: issue["number"] for key, (issue, _) in indexed.items()}
    by_branch = {r["branch"]["ref"]: r for r in reports}
    active = {b["ref"] for b in manifest.get("branches", [])}
    for result in reports:
        for item in result["findings"]:
            key = result["branch"]["ref"] + ":" + item["key"]
            observed.add(key)
            section = evidence_section(item, result, run_url, issue_links=issue_links, repository=repository)
            desired = metadata(section)
            title = issue_title(item, result)
            if key not in indexed:
                label = "schema-review" if item["kind"] == "review" else "schema-gap"
                if item["category"] == "automation":
                    label = "schema-automation"
                actions.append(("POST", "issues", {"title": title,
                    "body": section + "\n\n## Engineer notes\n", "labels": ["schema-monitor", label]}))
                continue
            issue, old = indexed[key]
            if old.get("presentationVersion", 1) > PRESENTATION_VERSION:
                continue  # An older publisher must not downgrade a newer brief.
            changed = old["fingerprint"] != item["fingerprint"]
            presentation_changed = old.get("presentation") != desired["presentation"] or old.get("presentationVersion") != PRESENTATION_VERSION
            title_changed = issue.get("title", title) != title
            if issue["state"] == "closed":
                if not changed and old.get("status") != "verified":
                    if presentation_changed or title_changed:
                        actions.append(("PATCH", "issues/" + str(issue["number"]), {
                            "title": title, "body": replace_section(issue["body"], section)}))
                    continue  # A maintainer acknowledged this exact finding.
                actions.append(("PATCH", "issues/" + str(issue["number"]), {
                    "state": "open", "title": title, "body": replace_section(issue["body"], section)}))
            elif changed or presentation_changed or title_changed or old.get("status") != "observed" or old.get("project") != result["projectCommit"] or old.get("candidate") != result["branch"]["commit"]:
                actions.append(("PATCH", "issues/" + str(issue["number"]), {"title": title, "body": replace_section(issue["body"], section)}))
    for key, (issue, old) in indexed.items():
        if key in observed or issue["state"] != "open":
            continue
        result = by_branch.get(old["branch"])
        status = None
        if result and result["complete"] and old["kind"] == "failure" and result["stages"].get(old["stage"], {}).get("state") == "passed":
            status = "verified"
        elif manifest.get("complete") and old["branch"] == "discovery":
            status = "verified"
        elif manifest.get("complete") and old["branch"] not in active:
            status = "inactive"
        if status and old.get("status") != status:
            updated = dict(old, status=status)
            if result:
                updated.update(project=result["projectCommit"], candidate=result["branch"]["commit"])
            body = MARKER.sub("<!-- schema-monitor " + json.dumps(updated, sort_keys=True) + " -->", issue["body"], count=1)
            body = re.sub(r"(?m)^\*\*Status:\*\* .*$", "**Status:** " + ("Relevant check passed" if status == "verified" else "Apple branch retired; not verified fixed"), body, count=1)
            note = "\n\nLatest scan: **" + status + "**. " + (run_url or "See retained assessment artifacts.")
            body = body.replace(END, note + "\n" + END, 1)
            values = {"body": body}
            if status == "verified":
                values.update(state="closed", state_reason="completed")
            actions.append(("PATCH", "issues/" + str(issue["number"]), values))
    return actions


def patch_branch(branch):
    return "schema/update-stable" if branch["kind"] == "stable" else "schema/preview/" + branch["ref"]


def publish_patch(repo, directory, result, github, token, run_url):
    branch = result["branch"]
    head = patch_branch(branch)
    pulls = github.request("GET", "pulls?" + urlencode({"state": "open", "head": github.repository.split("/")[0] + ":" + head}))
    current = pulls[0] if pulls else None
    if current and "<!-- schema-monitor-pr -->" not in (current.get("body") or ""):
        raise ValueError("The schema branch has a PR without the monitor ownership marker")
    body = "<!-- schema-monitor-pr -->\n" + render_report(result) + "\nFull evidence: " + run_url + "\n"
    body += "\nEngineering issues: https://github.com/" + github.repository + "/issues?q=is%3Aissue+label%3Aschema-monitor\n"
    if branch["kind"] == "seed":
        body += "\nThis draft is a preview. Stable adoption, server dependency updates and compatibility fixes require separate review.\n"
    if current and branch["kind"] == "seed" and branch["commit"] == branch["baseline"]:
        github.request("PATCH", "pulls/" + str(current["number"]), {"state": "closed", "body": body + "\nSeed and stable are the same snapshot. Engineering issues retain their own verification requirements.\n"})
        return "not-applicable"
    if branch["kind"] == "seed" and branch["commit"] == branch["baseline"]:
        return "not-applicable"
    if current and branch["kind"] == "seed" and not current.get("draft", True):
        github.request("POST", "graphql", {"query": "mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { pullRequest { isDraft } } }", "variables": {"id": current["node_id"]}})
    if not result["patch"] or not result["complete"] or result["stages"]["generate"]["state"] != "passed":
        if current:
            stale = body + "\n**No generated update for this candidate. The PR branch retains its last generated snapshot; its contents do not certify this candidate.**\n"
            if current["body"] != stale:
                github.request("PATCH", "pulls/" + str(current["number"]), {"body": stale})
        return "not-applicable" if result["complete"] and result["stages"]["generate"]["state"] == "passed" else "blocked"
    if not token:
        raise RuntimeError("App/PAT unavailable: automatic PR CI cannot be guaranteed; report and incident publication remain available")
    with tempfile.TemporaryDirectory(prefix="dm-schema-publish-") as scratch:
        root = Path(scratch) / "project"
        run(["git", "clone", "--quiet", "--shared", repo, root])
        run(["git", "checkout", "--quiet", "--detach", result["projectCommit"]], root)
        run(["git", "apply", "--index", directory / "candidate.patch"], root)
        names = run(["git", "diff", "--cached", "--name-only"], root).splitlines()
        if any(not allowed_path(name) for name in names):
            raise ValueError("Publication rejected unexpected patch paths")
        if run(["git", "rev-parse", ":" + SUBMODULE], root).strip() != branch["commit"]:
            raise ValueError("Patch gitlink is not the assessed candidate")
        run(["git", "remote", "set-url", "origin", "https://github.com/" + github.repository + ".git"], root)
        env = os.environ.copy()
        env["GH_TOKEN"] = token
        # Git asks gh for credentials; the token is never embedded in URLs or logs.
        env.update(GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="credential.helper", GIT_CONFIG_VALUE_0="",
                   GIT_CONFIG_KEY_1="credential.https://github.com.helper", GIT_CONFIG_VALUE_1="!gh auth git-credential")
        remote = run(["git", "ls-remote", "origin", "refs/heads/" + head], root, env).strip()
        previous = remote.split()[0] if remote else ""
        changed = True
        if previous:
            run(["git", "fetch", "--quiet", "origin", "refs/heads/" + head], root, env)
            changed = run(["git", "write-tree"], root).strip() != run(["git", "rev-parse", "FETCH_HEAD^{tree}"], root).strip()
        if changed:
            run(["git", "-c", "user.name=github-actions[bot]", "-c", "user.email=41898282+github-actions[bot]@users.noreply.github.com",
                 "commit", "--quiet", "-m", "chore(schema): assess Apple " + branch["ref"] + " " + branch["commit"][:12]], root)
            run(["git", "push", "--quiet", "--force-with-lease=refs/heads/" + head + ":" + previous,
                 "origin", "HEAD:refs/heads/" + head], root, env)
    title = "chore(schema): " + ("preview " if branch["kind"] == "seed" else "update ") + "Apple " + branch["ref"]
    if current:
        if current["body"] != body or current["title"] != title:
            github.request("PATCH", "pulls/" + str(current["number"]), {"body": body, "title": title})
    else:
        default = github.request("GET", "")["default_branch"]
        pull = github.request("POST", "pulls", {"head": head, "base": default, "title": title, "body": body, "draft": branch["kind"] == "seed"})
        github.request("POST", "issues/" + str(pull["number"]) + "/labels", {"labels": ["schema-preview" if branch["kind"] == "seed" else "schema-update"]})
    return "passed"


def collect_reports(manifest, directory):
    reports = []
    for branch in manifest.get("branches", []):
        path = directory / branch["key"] / "result.json"
        if path.exists():
            result = json.loads(path.read_text())
            if result["projectCommit"] != manifest["projectCommit"] or result["branch"] != branch:
                raise ValueError("Assessment identity differs from discovery")
        else:
            result = {"schemaVersion": 1, "projectCommit": manifest["projectCommit"], "branch": branch,
                      "complete": False, "patch": False, "stages": {s: {"state": "blocked"} for s in STAGES},
                      "findings": [finding("automation", "failure", "snapshot", "assessment",
                          "Apple schema assessment did not return evidence", "Inspect the missing or cancelled matrix job and rerun it.",
                          [{"path": branch["ref"], "detail": "No result.json artifact was returned"}])]}
        reports.append(result)
    if not manifest.get("complete"):
        branch = {"ref": "discovery", "key": "discovery", "commit": "unknown", "baseline": "unknown", "kind": "stable"}
        reports.append({"projectCommit": manifest.get("projectCommit", "unknown"), "branch": branch,
            "complete": False, "patch": False, "stages": {s: {"state": "blocked"} for s in STAGES},
            "findings": [finding("automation", "failure", "snapshot", "discovery", "Apple branch discovery failed",
                "Repair upstream access or branch configuration and rerun discovery.", [{"path": "discovery", "detail": manifest.get("error", "Discovery was incomplete")}])]})
    return reports


def publish(repo, manifest, directory, repository, report_only, run_url):
    reports = collect_reports(manifest, directory)
    (directory / "summary.md").write_text("\n\n".join(render_report(r) for r in reports))
    if report_only:
        actions = issue_actions([], reports, manifest, run_url, repository)
        write_json(directory / "proposed-issues.json", actions)
        previews = directory / "issue-previews"
        previews.mkdir(exist_ok=True)
        for _, _, values in actions:
            key = metadata(values["body"])["key"]
            (previews / (digest(key)[:16] + ".md")).write_text("# " + values["title"] + "\n\n" + values["body"])
        print("Report only: " + str(len(actions)) + " proposed issues; no GitHub writes.")
        return all(r["complete"] for r in reports)
    github = GitHub(repository)
    github.ensure_labels()
    token = os.environ.get("SCHEMA_PR_TOKEN", "")
    pull_api = GitHub(repository, token)
    failures = []
    for result in reports:
        if result["branch"]["ref"] == "discovery":
            continue
        try:
            outcome = publish_patch(repo, directory / result["branch"]["key"], result, pull_api, token, run_url)
            result["stages"]["publication"] = {"state": outcome}
        except (subprocess.SubprocessError, OSError, ValueError, RuntimeError) as exc:
            message = failure_text(exc)
            failures.append(message)
            result["stages"]["publication"] = {"state": "failed"}
            result["findings"].append(finding("automation", "failure", "publication", "publication",
                "Schema PR publication failed", "Repair the App/PAT or publication error and rerun the workflow. Assessment evidence remains available.",
                [{"path": result["branch"]["ref"], "detail": message}]))
    existing = github.issues()
    # Create missing parser/generation blockers first so review issues link to
    # their real numbers on the first publication cycle as well as later runs.
    initial = issue_actions(existing, reports, manifest, run_url, repository)
    for method, endpoint, body in initial:
        meta = metadata(body.get("body", ""))
        if method == "POST" and meta and meta["kind"] == "failure" and meta["stage"] in ("parse", "generate"):
            created = github.request(method, endpoint, body)
            existing.append(created)
    for method, endpoint, body in issue_actions(existing, reports, manifest, run_url, repository):
        github.request(method, endpoint, body)
    if manifest.get("complete"):
        retire_previews(pull_api, manifest)
    (directory / "summary.md").write_text("\n\n".join(render_report(r) for r in reports))
    if failures:
        raise RuntimeError("PR publication failed: " + "; ".join(failures))
    return all(r["complete"] for r in reports)


def retire_previews(github, manifest):
    active = {patch_branch(b) for b in manifest["branches"] if b["kind"] == "seed"}
    page = 1
    while True:
        pulls = github.request("GET", "pulls?state=open&per_page=100&page=" + str(page))
        for pull in pulls:
            head = pull["head"]["ref"]
            if head.startswith("schema/preview/") and head not in active and "<!-- schema-monitor-pr -->" in (pull["body"] or ""):
                github.request("PATCH", "pulls/" + str(pull["number"]), {"state": "closed", "body": pull["body"] + "\nApple retired this seed branch. This does not mark outstanding engineering findings fixed.\n"})
        if len(pulls) < 100:
            break
        page += 1



def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("discover", "assess", "publish"))
    parser.add_argument("--repo", type=Path, default=ROOT)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--key")
    parser.add_argument("--upstream", default=UPSTREAM)
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", "deploymenttheory/go-apple-dm"))
    parser.add_argument("--report-only", action="store_true")
    parser.add_argument("--run-url", default="")
    args = parser.parse_args()
    if args.action == "discover":
        manifest = discover(args.repo, args.output, args.upstream)
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                output.write("matrix=" + json.dumps(manifest["branches"]) + "\n")
                output.write("has_branches=" + str(bool(manifest["branches"])).lower() + "\n")
        return 0 if manifest["complete"] else 1
    if not args.manifest:
        parser.error("--manifest is required for assessment/publication")
    if args.action == "publish" and not args.manifest.exists():
        manifest = {"schemaVersion": 1, "complete": False, "branches": [], "projectCommit": os.environ.get("GITHUB_SHA", "unknown"), "error": "Discovery artifact is missing"}
    else:
        manifest = json.loads(args.manifest.read_text())
    if args.action == "assess":
        branch = next(b for b in manifest["branches"] if b["key"] == args.key)
        result = assess(args.repo, manifest, branch, args.output.resolve() / branch["key"])
        return 0 if result["complete"] else 1
    args.output.mkdir(parents=True, exist_ok=True)
    return 0 if publish(args.repo, manifest, args.output.resolve(), args.repository, args.report_only, args.run_url) else 1


if __name__ == "__main__":
    raise SystemExit(main())
