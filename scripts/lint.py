#!/usr/bin/env python3
"""Check both workspace modules without rewriting source, or explicitly format them.

Tests with supported feature/build tags are compiled before analysis. External
services are not required. The server is also built without the workspace, so a
call into the library that its declared version does not yet contain fails here
rather than only in the publication checks; verify-server-module-installation.py still
covers installation and standalone runtime.
"""

import argparse
import os
from pathlib import Path
import platform
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parent.parent
TAGS = "integration,e2e,acceptance,schema_seed_os_27"
MODULES = {"library": ROOT, "server": ROOT / "server"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--module", choices=MODULES)
    parser.add_argument("--format", action="store_true", help="rewrite authored Go formatting explicitly")
    parser.add_argument("--linter", default=os.environ.get("GOLANGCI_LINT", "golangci-lint"))
    parser.add_argument("--go", default=os.environ.get("GO", "go"))
    args = parser.parse_args()
    env = os.environ.copy()
    env["GOWORK"] = str(ROOT / "go.work")
    config = str(ROOT / ".golangci.yml")
    expected = (ROOT / ".golangci-version").read_text().strip().removeprefix("v")
    version = subprocess.check_output([args.linter, "version", "--short"], text=True).strip()
    if version.removeprefix("v") != expected:
        raise SystemExit(f"Expected golangci-lint {expected}, found {version}; run make tools.")
    subprocess.run([args.linter, "config", "verify", "--config=" + config], cwd=ROOT, env=env, check=True)
    print(f"golangci-lint {version}; GOWORK={env['GOWORK']}; tags={TAGS}", flush=True)
    subprocess.run([args.go, "version"], cwd=ROOT, env=env, check=True)
    modules = [args.module] if args.module else MODULES
    for module in modules:
        cwd = MODULES[module]
        if args.format:
            # Enumerate owned sources; recursive formatter discovery can descend
            # into ignored labs and nested reference modules. Small batches also
            # respect the Windows command-line length limit.
            names = subprocess.check_output(
                ["git", "ls-files", "--cached", "--others", "--exclude-standard", "--", "*.go"],
                cwd=ROOT, text=True).splitlines()
            files = [ROOT / name for name in sorted(set(names))
                     if name.startswith("server/") == (module == "server")]
            files = [p for p in files if p.is_file() and not re.search(
                rb"(?m)^// Code generated .* DO NOT EDIT\.\r?$",
                re.split(rb"(?m)^package\s", p.read_bytes(), maxsplit=1)[0])]
            for start in range(0, len(files), 40):
                subprocess.run([args.linter, "fmt", "--config=" + config,
                                *[str(p.relative_to(cwd)) for p in files[start:start + 40]]],
                               cwd=cwd, env=env, check=True)
            continue
        if module == "server":
            # The workspace resolves the library from this checkout, which hides
            # server code calling something the declared version does not contain.
            # Building without it is what the publication checks do, cheaply.
            print(f"[{module}] Build against the declared library version, without the workspace", flush=True)
            standalone = dict(env)
            standalone["GOWORK"] = "off"
            subprocess.run([args.go, "build", "-mod=readonly", "./..."],
                           cwd=cwd, env=standalone, check=True)
        print(f"[{module}] Compile packages and tests (no test execution)", flush=True)
        subprocess.run([args.go, "test", "-mod=readonly", "-run=^$", "-vet=off", "-tags=" + TAGS, "./..."],
                       cwd=cwd, env=env, check=True)
        print(f"[{module}] Lint complete baseline", flush=True)
        reports = ROOT / "cover" / "lint"
        reports.mkdir(parents=True, exist_ok=True)
        name = f"{module}-{sys.platform}-{platform.machine()}"
        subprocess.run([args.linter, "run", "--fix=false", "--config=" + config, "--build-tags=" + TAGS,
                        "--output.json.path=" + str(reports / (name + ".json")),
                        "--output.sarif.path=" + str(reports / (name + ".sarif")), "./..."],
                       cwd=cwd, env=env, check=True)


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as exc:
        print(f"Lint prerequisite or analysis failed (exit {exc.returncode}): {' '.join(exc.cmd)}", file=sys.stderr)
        raise SystemExit(exc.returncode) from None
