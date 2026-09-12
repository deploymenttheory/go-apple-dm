#!/usr/bin/env python3
"""Verify server module dependencies, package builds and command installation.

Default: package this checkout's server sources in a temporary module proxy.
--server-version: download an already published server module version instead.
Both modes disable Go workspaces, reject module replacements, require the exact
declared root library version, build server packages, and install dmserver and
dmctl into a temporary directory. Dependencies resolve through the configured
Go proxy with public checksum verification. Commands are compiled, not executed.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent
LIBRARY = "github.com/deploymenttheory/go-apple-dm"
SERVER = LIBRARY + "/server"


def run(args, cwd, env):
    return subprocess.run(args, cwd=cwd, env=env, check=True, text=True,
                          stdout=subprocess.PIPE).stdout


def candidate_proxy(temp, env):
    files = subprocess.check_output(
        ["git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "server/"],
        cwd=ROOT).decode().split("\0")
    sources = [(Path(name).relative_to("server"), (ROOT / name).read_bytes())
               for name in sorted(set(files)) if name and (ROOT / name).is_file()]
    digest = hashlib.sha256()
    for name, data in sources:
        digest.update(str(name).encode() + b"\0" + data)
    version = "v0.0.0-review." + digest.hexdigest()[:16]
    proxy = temp / "proxy"
    versions = proxy / SERVER / "@v"
    versions.mkdir(parents=True)
    (versions / (version + ".mod")).write_bytes((ROOT / "server/go.mod").read_bytes())
    (versions / (version + ".info")).write_text(json.dumps(
        {"Version": version, "Time": "2026-09-11T00:00:00Z"}))
    (versions / "list").write_text(version + "\n")
    with zipfile.ZipFile(versions / (version + ".zip"), "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in sources:
            archive.writestr(SERVER + "@" + version + "/" + name.as_posix(), data)
    previous_proxy = run(["go", "env", "GOPROXY"], temp, env).strip()
    env["GOPROXY"] = proxy.as_uri() + "," + previous_proxy
    # Only the locally packaged candidate lacks a public checksum entry.
    previous_nosum = run(["go", "env", "GONOSUMDB"], temp, env).strip()
    env["GONOSUMDB"] = ",".join(filter(None, [previous_nosum, SERVER]))
    return version


def main():
    parser = argparse.ArgumentParser(description=__doc__,
                                     formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--server-version", metavar="VERSION",
                        help="download and verify this published server version instead of packaging local sources")
    args = parser.parse_args()
    env = os.environ.copy()
    env["GOWORK"] = "off"
    env["GOFLAGS"] = ""
    # This is a public-module check. Developer-wide private-module filters must
    # not bypass the candidate proxy or public dependency checksum verification.
    env["GOPRIVATE"] = ""
    env["GONOPROXY"] = "none"
    env["GONOSUMDB"] = "none"
    env["GOSUMDB"] = "sum.golang.org"
    with tempfile.TemporaryDirectory(prefix="dm-server-module-installation-") as directory:
        temp = Path(directory)
        env["GOBIN"] = str(temp / "bin")
        version = args.server_version or candidate_proxy(temp, env)
        mode = "published module" if args.server_version else "local source candidate (temporary module artifact)"
        print("Server module installation verification", flush=True)
        print("Source: " + mode, flush=True)
        print("Server version: " + version, flush=True)
        print("[1/4] Resolve dependencies with GOWORK=off and no module replacements", flush=True)
        (temp / "go.mod").write_text(
            "module example.com/server-installation-verification\n\ngo 1.27.0\n\nrequire " + SERVER + " " + version + "\n")
        packages = ["service", "httpapi", "sqlstore/sqlite", "sqlstore/postgres",
                    "sqlstore/mysql", "ddmadapter/inproc", "ddmadapter/proxyclient"]
        (temp / "main.go").write_text("package main\nimport (\n" + "".join(
            '\t_ "' + SERVER + "/" + package + '"\n' for package in packages
        ) + ")\nfunc main() {}\n")
        # Downloading preserves the published requirement; tidy must not repair it
        # by searching for missing APIs in newer library versions.
        run(["go", "mod", "download", "all"], temp, env)
        server_info = json.loads(run(["go", "mod", "download", "-json", SERVER + "@" + version], temp, env))
        server_mod = json.loads(run(["go", "mod", "edit", "-json", server_info["GoMod"]], temp, env))
        assert not server_mod.get("Replace"), "server go.mod must not contain replacements"
        declared = next(dep["Version"] for dep in server_mod["Require"] if dep["Path"] == LIBRARY)
        metadata = json.loads(run(["go", "mod", "edit", "-json"], temp, env))
        assert not metadata.get("Replace"), "verification application unexpectedly uses module replacements"
        selected = json.loads(run(["go", "list", "-mod=mod", "-m", "-json", LIBRARY], temp, env))
        assert not selected.get("Replace"), "library unexpectedly uses a replacement"
        assert selected["Version"] == declared, "dependency graph masks the declared minimum"
        print("Declared and selected root library: " + declared, flush=True)
        print("[2/4] Build a separate application importing the server's public packages", flush=True)
        run(["go", "build", "-mod=mod", "-o", str(temp / "verification-app"), "."], temp, env)
        print("[3/4] Build all server packages and verify the root library version remains unchanged", flush=True)
        run(["go", "build", "-mod=readonly", SERVER + "/..."], temp, env)
        after = json.loads(run(["go", "list", "-mod=readonly", "-m", "-json", LIBRARY], temp, env))
        assert after["Version"] == declared, "build silently upgraded the library"
        print("[4/4] Install dmserver and dmctl into a temporary directory", flush=True)
        for command in ["dmserver", "dmctl"]:
            run(["go", "install", SERVER + "/cmd/" + command + "@" + version], temp, env)
        print("PASS: declared dependency resolution, public-package integration build, all server package builds, "
              "and dmserver/dmctl installation")
        print("Verified server " + version + " against its declared root library " + declared)
        print("Scope: build and installation compatibility. Runtime behavior is covered by separate tests.")


if __name__ == "__main__":
    main()
