#!/usr/bin/env python3
"""Check upcoming Apple schemas with the generator on main and publish actionable failures."""
import argparse
import copy
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time
from urllib.parse import quote

from device_management_client_schema_diagnostics import digest, findings

ROOT = Path(__file__).resolve().parents[2]
UPSTREAM = "https://github.com/apple/device-management.git"
CANARY_MIRROR = "https://github.com/deploymenttheory/go-apple-dm.git"
SCHEMA = "devicemanagement/schema"
CURRENT = "third_party/apple-device-management/current"
SHA = re.compile(r"^[0-9a-f]{40}$")
REF = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._/-]*$")
STAGES = ("snapshot", "generate", "verify", "compile")
MARKER = re.compile(r"<!-- schema-monitor (\{.*?\}) -->")
START, END = "<!-- schema-monitor:evidence:start -->", "<!-- schema-monitor:evidence:end -->"
SCRIPT = ".github/scripts/device_management_client_schema_monitor.py"


def run(args, cwd=None, env=None, timeout=1800):
    return subprocess.run([str(a) for a in args], cwd=cwd, env=env, timeout=timeout,
                          check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE).stdout


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


def snapshot_ref(ref, commit):
    if not REF.fullmatch(ref) or ".." in ref or ref.endswith("/") or not SHA.fullmatch(commit):
        raise ValueError("Snapshot requires a safe ref name and full commit SHA")
    return "refs/heads/schema-source/" + ref + "/" + commit


def entry(kind, ref, commit):
    return {"kind": kind, "ref": ref, "commit": commit, "key": digest(kind + ":" + ref + ":" + commit)[:16],
            "snapshotRef": snapshot_ref(ref, commit)}


def ancestor(repo, before, after):
    p = subprocess.run(["git", "merge-base", "--is-ancestor", before, after], cwd=repo,
                       capture_output=True, text=True, timeout=60)
    if p.returncode not in (0, 1):
        raise RuntimeError(p.stderr)
    return p.returncode == 0


def discover(repo, output, upstream=UPSTREAM, canary_mirror=CANARY_MIRROR):
    manifest = {"schemaVersion": 4, "complete": False, "branches": [], "upstream": upstream,
                "canaryMirror": canary_mirror}
    try:
        manifest["projectCommit"] = run(["git", "rev-parse", "HEAD"], repo).strip()
        pin = run(["git", "rev-parse", "HEAD:" + CURRENT], repo).strip()
        provenance = json.loads((repo / SCHEMA / "GENERATED_FROM.json").read_text())
        if provenance["commit"] != pin or not SHA.fullmatch(pin):
            raise ValueError("Published schema provenance differs from the current gitlink")
        history = provenance.get("history", {}).get("commit")
        if history:
            history_path = run(["git", "config", "--file", ".gitmodules", "--get",
                                "submodule.apple-device-management-compatibility.path"], repo).strip()
            if not SHA.fullmatch(history) or run(["git", "rev-parse", "HEAD:" + history_path], repo).strip() != history:
                raise ValueError("Published compatibility provenance differs from its gitlink")
        manifest.update(control=entry("control", "control", pin), historyCommit=history)
        default, heads = parse_refs(run(["git", "ls-remote", "--symref", upstream, "HEAD", "refs/heads/*"]))
        with tempfile.TemporaryDirectory(prefix="dm-codegen-discover-") as temp:
            apple = Path(temp) / "apple"
            run(["git", "clone", "--quiet", "--no-checkout", upstream, apple])
            for ref, commit in sorted(heads.items()):
                if ref != default and not ref.startswith("seed"):
                    continue
                # No historical mirror enumeration: only current upstream heads
                # beyond the published source can produce new incidents.
                if ancestor(apple, commit, pin):
                    continue
                if ref != default and ancestor(apple, commit, heads[default]):
                    continue  # Apple already promoted this retained branch.
                manifest["branches"].append(entry("release" if ref == default else "seed", ref, commit))
        manifest["complete"] = True
    except (subprocess.SubprocessError, OSError, ValueError, KeyError, RuntimeError) as exc:
        manifest["error"] = failure_text(exc)
    write_json(output, manifest)
    return manifest
def capture_canaries(manifest, output):
    """Copy every journey input into immutable refs before Apple retires it."""
    captured = []
    try:
        if not manifest.get("complete"):
            raise ValueError("Cannot capture an incomplete discovery manifest")
        mirror = manifest.get("canaryMirror", CANARY_MIRROR)
        env = git_auth_env()
        for branch in manifest.get("branches", []):
            ref, commit = branch["snapshotRef"], branch["commit"]
            existing = run(["git", "ls-remote", mirror, ref], env=env).strip()
            if existing:
                actual = existing.split()[0]
                if actual != commit:
                    raise ValueError("Canary snapshot ref is immutable but points at a different commit: " + ref)
                captured.append({"ref": ref, "commit": commit, "state": "retained"})
                continue
            with tempfile.TemporaryDirectory(prefix="dm-schema-capture-") as scratch:
                source = Path(scratch) / "source"
                run(["git", "clone", "--quiet", "--no-checkout", branch.get("captureSource", manifest["upstream"]), source])
                run(["git", "checkout", "--quiet", "--detach", commit], source)
                actual = run(["git", "rev-parse", "HEAD"], source).strip()
                if actual != commit:
                    raise ValueError("Advertised source changed during capture: " + branch["ref"])
                run(["git", "push", mirror, commit + ":" + ref], source, env=env)
            retained = run(["git", "ls-remote", mirror, ref], env=env).strip().split()
            if not retained or retained[0] != commit:
                raise ValueError("Canary snapshot was not retained: " + ref)
            captured.append({"ref": ref, "commit": commit, "state": "captured"})
        result = {"complete": True, "captured": captured}
    except (subprocess.SubprocessError, OSError, ValueError, KeyError) as exc:
        result = {"complete": False, "captured": captured, "error": failure_text(exc)}
    write_json(output, result)
    return result


def git_auth_env():
    """Let Actions' GH_TOKEN authenticate mirror pushes without logging it."""
    env = os.environ.copy()
    if env.get("GH_TOKEN"):
        env.update(GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="credential.helper", GIT_CONFIG_VALUE_0="",
                   GIT_CONFIG_KEY_1="credential.https://github.com.helper", GIT_CONFIG_VALUE_1="!gh auth git-credential")
    return env


def failure_text(exc):
    if isinstance(exc, subprocess.CalledProcessError):
        return (exc.stderr or exc.stdout or str(exc))[-6000:]
    return str(exc)


def checkout(source, commit, destination):
    run(["git", "clone", "--quiet", "--no-checkout", source, destination])
    run(["git", "checkout", "--quiet", "--detach", commit], destination)
    if run(["git", "rev-parse", "HEAD"], destination).strip() != commit:
        raise ValueError("Checkout does not match recorded commit")


def stage(result, name, args, root, directory, env):
    p = subprocess.run([str(a) for a in args], cwd=root, env=env, timeout=1800,
                       text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    (directory / (name + ".log")).write_text(p.stdout)
    result["stages"][name] = {"state": "passed" if p.returncode == 0 else "failed", "exitCode": p.returncode}
    return p.returncode == 0, p.stdout


def prepare_output(root):
    """Keep handwritten generator support, discard the production API lock."""
    (root / SCHEMA / "EXPORTED_IDENTIFIERS.lock").unlink(missing_ok=True)
    # Write removes stale generated files itself. Tests are not compiled: they
    # belong to the production pin and include server/OS feature assumptions.


def infrastructure_failure(text):
    return any(word in text for word in ("dial tcp", "TLS handshake timeout", "i/o timeout", "no such host",
                                         "proxy.golang.org", "missing go.sum entry", "connection refused",
                                         "operation not permitted", "permission denied", "no space left on device",
                                         "failed to initialize build cache"))


def assess(repo, manifest, branch, directory):
    directory = Path(directory) / branch["key"]
    directory.mkdir(parents=True, exist_ok=True)
    result = {"schemaVersion": 4, "projectCommit": manifest["projectCommit"], "branch": branch,
              "controlCommit": manifest["control"]["commit"], "complete": False, "findings": [],
              "stages": {s: {"state": "blocked"} for s in STAGES}, "automationErrors": []}
    try:
        for commit in (manifest["projectCommit"], branch["commit"], manifest["control"]["commit"]):
            if not SHA.fullmatch(commit):
                raise ValueError("Assessment requires immutable commit SHAs")
        with tempfile.TemporaryDirectory(prefix="dm-codegen-assess-") as temp:
            temp = Path(temp).resolve()
            project, candidate, baseline = temp / "project", temp / "candidate", temp / "control"
            checkout(repo, manifest["projectCommit"], project)
            source = manifest.get("canaryMirror", manifest["upstream"])
            checkout(source, branch["commit"], candidate)
            checkout(source, manifest["control"]["commit"], baseline)
            history = ""
            if manifest.get("historyCommit"):
                history = temp / "history"
                checkout(manifest["upstream"], manifest["historyCommit"], history)
            result["stages"]["snapshot"] = {"state": "passed"}
            env = dict(os.environ, GOWORK="off")
            tool = temp / "schemagen"
            run(["go", "build", "-o", tool, "./cmd/schemagen"], project, env)
            # Resolve dependencies before classifying compiler errors. A failed
            # download is monitoring infrastructure, not schema incompatibility.
            run(["go", "mod", "download"], project, env)
            prepare_output(project)
            base = [tool, "-schema", candidate, "-history", history, "-ref", branch["ref"], "-out", project / SCHEMA]
            commands = [("generate", base + ["generate"]), ("verify", base + ["verify"]),
                        ("compile", ["go", "build", "./devicemanagement/schema/..."])]
            for name, command in commands:
                ok, text = stage(result, name, command, project, directory, env)
                if ok:
                    continue
                if infrastructure_failure(text):
                    raise RuntimeError(text[-6000:])
                audit = None
                if name == "generate":
                    try:
                        audit = json.loads(run([tool, "-schema", candidate, "-baseline", baseline,
                                                "-history", history, "-ref", branch["ref"], "audit"], project, env))
                        write_json(directory / "diagnostics.json", {
                            "candidateCommit": audit["candidateCommit"], "baselineCommit": audit["baselineCommit"],
                            "findings": [f for f in audit["findings"] if f["stage"] == "parse" and f["kind"] == "failure"]})
                    except (subprocess.SubprocessError, ValueError):
                        pass  # Keep the original failure when further diagnosis cannot run.
                result["findings"] = findings(name, text, project, baseline, candidate, audit)
                break
            result["complete"] = True
    except (subprocess.SubprocessError, OSError, ValueError, KeyError, RuntimeError) as exc:
        result["automationErrors"].append(failure_text(exc))
    write_json(directory / "result.json", result)
    return result


def passed(result):
    return result["complete"] and not result["automationErrors"] and all(
        result["stages"][s]["state"] == "passed" for s in STAGES)


def collect_reports(manifest, directory):
    reports = []
    for branch in [manifest["control"], *manifest["branches"]]:
        path = directory / branch["key"] / "result.json"
        if path.exists():
            result = json.loads(path.read_text())
            if (result["projectCommit"] != manifest["projectCommit"] or result["branch"] != branch
                    or result["controlCommit"] != manifest["control"]["commit"]):
                raise ValueError("Assessment identity differs from discovery")
        else:
            result = {"projectCommit": manifest["projectCommit"], "branch": branch, "complete": False,
                      "findings": [], "stages": {s: {"state": "blocked"} for s in STAGES},
                      "automationErrors": ["Assessment artifact is missing; compatibility is unknown."]}
        reports.append(result)
    return reports


def aggregate(reports):
    """Group all affected candidates before making any publication decisions."""
    groups = {}
    for result in reports:
        if result["branch"]["kind"] == "control" or not result["complete"] or result["automationErrors"]:
            continue
        for finding in result["findings"]:
            item = groups.setdefault(finding["key"], dict(copy.deepcopy(finding), observations=[]))
            item["observations"].append({"branch": result["branch"], "evidence": finding["evidence"],
                                         "examples": finding["examples"]})
    for item in groups.values():
        item["observations"].sort(key=lambda o: (o["branch"]["commit"], o["branch"]["ref"]))
    return groups


def metadata(body):
    match = MARKER.search(body or "")
    return json.loads(match.group(1)) if match else None


def replace_section(body, section):
    left, right = body.find(START), body.find(END)
    if left >= 0 and right >= left:
        return body[:left] + section + body[right + len(END):]
    return body.rstrip() + "\n\n" + section


def evidence_section(item, project, control, run_url, repository):
    candidates = [o["branch"] for o in item["observations"]]
    meta = {"monitor": "codegen", "key": item["key"], "status": "observed", "project": project,
            "candidates": candidates, "fingerprint": digest([project, item])}
    text = [START, "<!-- schema-monitor " + json.dumps(meta, sort_keys=True) + " -->",
            "## Why generation fails", item["why"], "", "## Required generator work", item["change"],
            "", "Generator location: [" + item["location"] + "](https://github.com/" + repository + "/blob/" + project + "/" + item["location"] + ").",
            "", "## Regression test", item["test"], "", "## Recorded evidence",
            "Generator: `" + project + "`; passing control: `" + control + "`. Stage: `" + item["stage"] + "`."]
    for obs in item["observations"]:
        b = obs["branch"]
        text += ["", "### " + b["ref"] + " — `" + b["commit"] + "`",
                 str(len(obs["evidence"])) + " affected observations; the assessment artifact retains the full inventory."]
        for evidence in obs["evidence"][:15]:
            url = "https://github.com/" + repository + "/blob/" + b["snapshotRef"].removeprefix("refs/heads/") + "/" + quote(evidence["path"], safe="/")
            text += ["- [" + evidence["path"] + "](" + url + "): `" + evidence["detail"][:400].replace("`", "'").replace("\n", " ") + "`"]
        for example in obs["examples"]:
            if example["diff"]:
                text += ["", "````diff", example["diff"], "````"]
                if example["truncated"]:
                    text.append("Excerpt truncated; inspect the linked source and retained diagnostics for full details.")
        text += ["", "Download this run's discovery artifact and reproduce:", "```sh",
                 "python3 " + SCRIPT + " assess --manifest /path/to/discovery.json --key " + b["key"] + " --output /tmp/device-management-codegen", "```"]
    text += ["", "Assessment artifacts: " + run_url, END]
    return "\n".join(text)


def issue_actions(existing, reports, manifest, run_url, repository):
    control = next((r for r in reports if r["branch"]["kind"] == "control"), None)
    if not manifest["complete"] or control is None or not passed(control):
        return []  # No future-schema attribution without a working control.
    indexed = {}
    for issue in existing:
        meta = metadata(issue.get("body"))
        if meta and meta.get("monitor") == "codegen":
            indexed[meta["key"]] = (issue, meta)
    actions = []
    groups = aggregate(reports)
    successful = {r["branch"]["commit"] for r in reports if passed(r)}
    for key, item in sorted(groups.items()):
        # A moving branch must not erase a previously failing immutable input.
        if key in indexed:
            current = {o["branch"]["commit"] for o in item["observations"]}
            for previous in indexed[key][1].get("candidates", []):
                if previous["commit"] not in current | successful:
                    item["observations"].append({"branch": previous, "examples": [], "evidence": [
                        {"path": "result.json", "detail": "Previously affected candidate has not been reassessed; retained for verification."}]})
            item["observations"].sort(key=lambda o: (o["branch"]["commit"], o["branch"]["ref"]))
        section = evidence_section(item, manifest["projectCommit"], manifest["control"]["commit"], run_url, repository)
        title = "[Device Management Client Schema] " + item["title"]
        if key not in indexed:
            actions.append(("POST", "issues", {"title": title, "body": section + "\n\n## Engineer notes\n",
                                                 "labels": ["schema-monitor", "schema-gap"]}))
        else:
            issue, old = indexed[key]
            new = metadata(section)
            if old["fingerprint"] != new["fingerprint"] or old["status"] != "observed":
                actions.append(("PATCH", "issues/" + str(issue["number"]),
                                {"title": title, "body": replace_section(issue["body"], section), "state": "open"}))
    for key, (issue, meta) in sorted(indexed.items()):
        if key in groups or issue["state"] != "open":
            continue
        affected = {b["commit"] for b in meta.get("candidates", [])}
        if affected and affected <= successful:
            revised = dict(meta, status="verified", project=manifest["projectCommit"])
            body = MARKER.sub(lambda _: "<!-- schema-monitor " + json.dumps(revised, sort_keys=True) + " -->", issue["body"], count=1)
            body = body.replace(END, "All affected candidates passed generation, regeneration and compilation. " + run_url + "\n" + END, 1)
            actions.append(("PATCH", "issues/" + str(issue["number"]), {"state": "closed", "state_reason": "completed", "body": body}))
    return actions


class GitHub:
    """Serial, paced REST writes with bounded secondary-rate-limit recovery."""
    def __init__(self, repository, sleep=time.sleep):
        self.repository, self.sleep, self.last_write = repository, sleep, None

    def request(self, method, endpoint, body=None):
        for attempt in range(4):
            if method != "GET" and self.last_write is not None:
                self.sleep(max(0, 1.1 - (time.monotonic() - self.last_write)))
            args = ["gh", "api", "--include", "--method", method, "repos/" + self.repository + "/" + endpoint]
            if body is not None:
                args += ["--input", "-"]
            p = subprocess.run(args, input=json.dumps(body) if body is not None else None,
                               capture_output=True, text=True, timeout=120)
            if method != "GET":
                self.last_write = time.monotonic()
            headers, _, payload = p.stdout.replace("\r\n", "\n").partition("\n\n")
            if not p.returncode:
                return json.loads(payload) if payload.strip() else None
            text = p.stderr + payload
            limited = ("secondary rate limit" in text or "rate limit exceeded" in text.lower()
                       or re.search(r"^HTTP/\S+ 429", headers))
            if not limited or attempt == 3:
                raise RuntimeError("GitHub " + method + " " + endpoint + " failed: " + text[-2000:])
            fields = dict(line.lower().split(":", 1) for line in headers.splitlines() if ":" in line)
            delay = float(fields.get("retry-after", 60 * 2**attempt))
            if fields.get("x-ratelimit-remaining", "").strip() == "0":
                delay = max(delay, float(fields.get("x-ratelimit-reset", time.time())) - time.time())
            if delay > 240:
                raise RuntimeError("GitHub write deferred until the rate-limit window resets")
            self.sleep(max(0, delay))

    def issues(self):
        result, page = [], 1
        while True:
            batch = self.request("GET", "issues?state=all&labels=schema-monitor&per_page=100&page=" + str(page))
            result.extend(i for i in batch if "pull_request" not in i)
            if len(batch) < 100:
                return result
            page += 1


def render_report(manifest, reports):
    lines = ["# Device Management Client Schema Code Generation Monitor", "",
             "Project: `" + manifest.get("projectCommit", "unknown") + "`.", "",
             "| Source | Generate | Verify | Compile |", "|---|---|---|---|"]
    for r in reports:
        b = r["branch"]
        lines.append("| " + b["kind"] + " " + b["ref"] + " `" + b["commit"][:12] + "` | " +
                     " | ".join(r["stages"][s]["state"] for s in ("generate", "verify", "compile")) + " |")
        for error in r["automationErrors"]:
            lines += ["", "Monitoring failure: " + error[-1000:]]
    if reports and not passed(reports[0]):
        lines += ["", "**The control failed. Future-schema incompatibility cannot be established. No candidate incidents are published.**"]
    if not manifest["complete"]:
        lines += ["", "Discovery failed: " + manifest.get("error", "incomplete discovery")]
    return "\n".join(lines) + "\n"


def publish(manifest, directory, github, report_only, run_url, max_new=10, max_writes=50):
    directory.mkdir(parents=True, exist_ok=True)
    reports = collect_reports(manifest, directory) if manifest.get("control") else []
    (directory / "summary.md").write_text(render_report(manifest, reports))
    actions = issue_actions(github.issues(), reports, manifest, run_url, github.repository)
    write_json(directory / "proposed-issues.json", actions)
    state = {"reportOnly": report_only, "completed": 0, "deferred": [], "error": None}
    if not report_only:
        created = 0
        for index, (method, endpoint, body) in enumerate(actions):
            if state["completed"] >= max_writes or (method == "POST" and created >= max_new):
                state["deferred"].append(index)
                continue
            try:
                github.request(method, endpoint, body)
            except (RuntimeError, subprocess.SubprocessError, OSError) as exc:
                state["error"] = failure_text(exc)
                state["deferred"].extend(range(index, len(actions)))
                break
            state["completed"] += 1
            created += method == "POST"
    write_json(directory / "publication.json", state)
    with (directory / "summary.md").open("a") as out:
        out.write(f"\nPublication: {state['completed']} writes; {len(state['deferred'])} deferred. Report only: {report_only}.\n")
        if state["error"]:
            out.write("Publication failure: " + state["error"] + "\n")
    return (manifest["complete"] and bool(reports) and all(passed(r) for r in reports)
            and not state["error"] and not state["deferred"])


def consolidation_actions(existing, historical_commit):
    """Only retire the known historical examples incident family, preserving notes."""
    issues = []
    for issue in existing:
        meta = metadata(issue.get("body"))
        if (meta and meta.get("monitor") != "codegen" and meta.get("stage") == "parse"
                and meta.get("candidate") == historical_commit
                and "cannot unmarshal !!map into []schemagen.Example" in issue["body"]):
            issues.append(issue)
    if not issues:
        return []
    issues.sort(key=lambda i: i["number"])
    canonical = issues[0]["number"]
    marker = "<!-- device-management-codegen:historical-examples -->"
    actions = []
    for issue in issues:
        if marker in issue["body"] and issue["state"] == "closed":
            continue
        note = ("\n\n" + marker + "\nThis finding comes from a historical seed older than the published schema. "
                "The monitor now checks future schema code generation. Closing this record does not claim that "
                "historical parsing was repaired.\n")
        if issue["number"] == canonical:
            note += "\nConsolidated affected-file records: " + ", ".join("#" + str(i["number"]) for i in issues[1:]) + ".\n"
        else:
            note += "\nConsolidated into #" + str(canonical) + ".\n"
        values = {"state": "closed", "state_reason": "not_planned", "body": issue["body"] + note}
        if issue["number"] == canonical:
            values["title"] = "[Device Management Client Schema] Historical examples parser limitation (consolidated)"
        actions.append(("PATCH", "issues/" + str(issue["number"]), values))
    return actions


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("discover", "capture", "assess", "publish", "consolidate"))
    parser.add_argument("--repo", type=Path, default=ROOT)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--key")
    parser.add_argument("--upstream", default=UPSTREAM)
    parser.add_argument("--canary-mirror", default=CANARY_MIRROR)
    parser.add_argument("--repository", default=os.environ.get("GITHUB_REPOSITORY", "deploymenttheory/go-apple-dm"))
    parser.add_argument("--report-only", action="store_true")
    parser.add_argument("--run-url", default="")
    parser.add_argument("--historical-commit", help="Reviewed historical commit whose legacy incidents may be consolidated")
    args = parser.parse_args()
    if args.action == "discover":
        result = discover(args.repo, args.output, args.upstream, args.canary_mirror)
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as out:
                out.write("matrix=" + json.dumps([result["control"], *result["branches"]] if result["complete"] else []) + "\n")
                out.write("has_branches=" + str(result["complete"]).lower() + "\n")
        return 0 if result["complete"] else 1
    if args.action == "consolidate":
        if not args.historical_commit or not SHA.fullmatch(args.historical_commit):
            parser.error("consolidate requires --historical-commit with the reviewed historical SHA")
        api = GitHub(args.repository)
        actions = consolidation_actions(api.issues(), args.historical_commit)
        write_json(args.output / "consolidation.json", actions)
        if not args.report_only:
            for method, endpoint, body in actions:
                api.request(method, endpoint, body)
        print(f"Historical incident consolidation: {len(actions)} actions; report only: {args.report_only}")
        return 0
    manifest = json.loads(args.manifest.read_text())
    if args.action == "capture":
        capture_manifest = dict(manifest, branches=[manifest["control"], *manifest["branches"]])
        return 0 if capture_canaries(capture_manifest, args.output)["complete"] else 1
    if args.action == "assess":
        branch = next(b for b in [manifest["control"], *manifest["branches"]] if b["key"] == args.key)
        return 0 if passed(assess(args.repo, manifest, branch, args.output)) else 1
    return 0 if publish(manifest, args.output, GitHub(args.repository), args.report_only, args.run_url) else 1


if __name__ == "__main__":
    raise SystemExit(main())
