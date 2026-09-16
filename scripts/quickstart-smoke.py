#!/usr/bin/env python3
"""Verify the local Compose walkthrough in a disposable, isolated project."""

import json
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]


def main():
    project = "dm-quickstart-test-" + uuid.uuid4().hex[:12]
    compose = ["docker", "compose", "-p", project, "-f", str(ROOT / "deploy/quickstart/compose.yaml")]
    env = dict(os.environ, QUICKSTART_PORT="0")

    def run(*args, stdin=None, success=True):
        result = subprocess.run([*compose, *args], cwd=ROOT, env=env, input=stdin,
                                capture_output=True, text=True, timeout=600)
        if success and result.returncode:
            raise RuntimeError(f"Compose {args}: {result.stderr}\n{result.stdout}")
        return result

    token = "@/data/secrets/admin"

    def cli(*args, success=True):
        return run("run", "--rm", "-T", "dmctl", "-token", token, *args, success=success)

    def identities():
        status = json.loads(cli("setup", "status").stdout)
        assert not status["ready"] and not status["enrollmentEnabled"]
        assert status["issues"] == ["push requires a valid active certificate"]
        return {item["id"]: (item["active"], [(r["id"], r["fingerprint"]) for r in item["revisions"]])
                for item in status["identities"]}

    try:
        print(f"Building isolated project {project}", flush=True)
        run("--profile", "tools", "build")
        run("up", "-d", "--wait", "--wait-timeout", "90")
        assert "Role:" in cli("status").stdout
        assert "CHANNEL  ID  ENABLED  SERIAL  OS  LAST SEEN" in cli("enrollments", "list").stdout
        original = identities()
        with tempfile.TemporaryDirectory(prefix="dm-quickstart-smoke-") as directory:
            ca = Path(directory) / "ca.pem"
            run("cp", "dmserver:/data/https-ca.pem", str(ca))
            port = run("port", "dmserver", "8443").stdout.strip().rsplit(":", 1)[1]
            context = ssl.create_default_context(cafile=str(ca))
            for path in ("healthz", "readyz"):
                with urllib.request.urlopen(f"https://localhost:{port}/{path}", context=context, timeout=5) as response:
                    assert response.status == 200 and response.read().strip() == b"ok"
        print("Verified HTTPS, empty inventory and explicit incomplete enrollment", flush=True)

        # Save the token as the volume's runtime user, never in logs or arguments.
        run("run", "--rm", "-T", "--entrypoint", "sh", "bootstrap", "-ec",
            "umask 077; set -C; dmctl -server https://dmserver:8443 -ca-file /data/https-ca.pem "
            "-token @/data/secrets/admin -output human principals create operator-root -root "
            "> /data/operator-root-token")
        policy = 'permit (principal == MDM::Principal::"operator-root", action, resource);\n'
        run("run", "--rm", "-T", "dmctl", "policies", "put", "operator-root", "-file", "-", stdin=policy)
        token = "@/data/operator-root-token"
        assert "operator-root" in cli("principals", "list").stdout
        cli("enrollments", "list")

        document = json.loads(run("run", "--rm", "-T", "bootstrap", "config").stdout)
        document["environment"]["DM_ORGANIZATION"] = "Onboarding smoke test"
        del document["secretFiles"]["DM_ADMIN_TOKEN"]
        run("run", "--rm", "-T", "bootstrap", "apply-config", stdin=json.dumps(document))
        run("down")
        run("up", "-d", "--wait", "--wait-timeout", "90")
        assert original == identities(), "restart replaced an identity"
        assert "operator-root" in cli("principals", "list").stdout
        cli("enrollments", "list")
        assert "ACTIVE" not in cli("status").stdout
        saved = json.loads(run("run", "--rm", "-T", "bootstrap", "config").stdout)
        assert saved == document, "bootstrap overwrote operator configuration"
        rejected = cli("-token", "@/data/secrets/admin", "enrollments", "list", success=False)
        assert rejected.returncode != 0 and "401" in rejected.stderr, rejected.stderr

        # Permissions and the original external keys remain owned by the runtime user.
        check_files = """import os, pathlib
assert os.getuid() == 65532
for name in ('setup.json', 'secrets/admin', 'secrets/storage', 'secrets/issuance', 'operator-root-token'):
    assert pathlib.Path('/data', name).stat().st_mode & 0o777 == 0o600, name
print('Protected configuration and secrets are owned by the non-root runtime user')
"""
        run("run", "--rm", "-T", "--entrypoint", "python3", "bootstrap", "-c", check_files)
        print("Verified stored-admin handoff, restart persistence and non-root permissions", flush=True)
    finally:
        # Only the unique project created above is disposable.
        result = run("down", "-v", "--remove-orphans", success=False)
        if result.returncode:
            raise RuntimeError(f"Could not remove disposable project {project}: {result.stderr}")
    print("Quickstart smoke passed; no Apple services or physical devices used", flush=True)


if __name__ == "__main__":
    main()
