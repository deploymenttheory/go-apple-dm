#!/usr/bin/env python3
"""Record nonsensitive, read-only host evidence before/after the macOS upgrade."""
import argparse
import datetime
import hashlib
import json
from pathlib import Path
import platform
import subprocess

ROOT = Path(__file__).resolve().parents[2]


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--phase", choices=["before", "after"], required=True)
    parser.add_argument("--out", type=Path, required=True, help="new private evidence JSON file")
    parser.add_argument("--workspace", type=Path, default=ROOT / "test-lab/local",
                        help="actual bench workspace; presence is not proof of live acceptance")
    args = parser.parse_args()
    if platform.system() != "Darwin":
        raise SystemExit("This baseline requires the physical Mac.")
    version = run("sw_vers", "-productVersion")
    expected = "26" if args.phase == "before" else "27"
    if version.split(".")[0] != expected:
        raise SystemExit(f"{args.phase} phase requires macOS {expected}; found {version}")
    schema = json.loads((ROOT / "devicemanagement/schema/GENERATED_FROM.json").read_text())
    fixture_paths = sorted((ROOT / "test-lab/apple-features/declarations").glob("*.json"))
    report = {
        "at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "phase": args.phase,
        "macOS": version, "build": run("sw_vers", "-buildVersion"), "architecture": platform.machine(),
        "go": run("go", "version"), "revision": run("git", "rev-parse", "HEAD"),
        "workingTree": run("git", "status", "--short"), "schema": schema,
        "fixtureSHA256": {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in fixture_paths},
        "benchWorkspace": str(args.workspace.resolve()),
        "benchConfigured": (args.workspace / "bench.json").exists(),
        "liveAcceptance": "pending; this records the host only",
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    with args.out.open("x") as output:
        args.out.chmod(0o600)
        output.write(json.dumps(report, indent=2) + "\n")
    print(f"Recorded macOS {version} host baseline at {args.out}; device acceptance remains pending.")


if __name__ == "__main__":
    main()
