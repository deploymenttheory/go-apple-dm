#!/usr/bin/env python3
"""Local Compose bootstrap. Certificate/key operations belong to dmctl."""

import argparse
import contextlib
from datetime import datetime, timezone
import fcntl
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


class Bootstrap:
    def __init__(self, directory=Path("/data"), binary="dmctl"):
        self.directory = directory
        self.path = directory / "setup.json"
        self.binary = binary

    def cli(self, *args, setup=True):
        # Compose's bootstrap owns its configuration; ambient CLI contexts and
        # server overrides must not redirect local identity operations.
        env = {k: v for k, v in os.environ.items() if not k.startswith(("DM_", "DMCTL_"))}
        command = [self.binary, *args]
        if setup:
            command += ["-setup-file", str(self.path)]
        result = subprocess.run(command, env=env, capture_output=True, text=True, check=False)
        if result.returncode:
            raise ValueError(result.stderr.strip() or "dmctl operation failed")
        return json.loads(result.stdout) if result.stdout.strip() else None

    def write_json(self, path, document):
        # Rename within the volume: a failed write never truncates configuration.
        fd, temp = tempfile.mkstemp(prefix=".quickstart-", dir=self.directory)
        try:
            with os.fdopen(fd, "w") as stream:
                json.dump(document, stream, indent=2)
                stream.write("\n")
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temp, path)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temp)

    def initialize(self):
        complete = self.directory / ".quickstart-complete"
        if not self.path.exists():
            if complete.exists():
                raise ValueError("setup.json is missing from initialized state; restore it from backup")
            self.cli("setup", "init", "-dir", str(self.directory), "-role", "customer",
                     "-public-url", "https://localhost:8443", "-listen", "0.0.0.0:8443", setup=False)
        document = json.loads(self.path.read_text())
        if not complete.exists():
            for name, value in {"DM_AUDIT_STORE": "true",
                                "DM_AUDIT_RETENTION": "720h"}.items():
                document["environment"].setdefault(name, value)
            self.write_json(self.path, document)
        # Load first. Missing keys, invalid references or a changed storage key
        # must fail before any certificate generation is attempted.
        status = self.cli("setup", "status")
        identities = {item["id"]: item for item in (status.get("identities") or [])}
        setup = document["setup"]
        for group, field, operation, options in (
            ("server-https", "httpsId", "lab", ["-cn", "Local MDM HTTPS", "-hosts", "localhost,127.0.0.1,dmserver"]),
            ("enrollment-ca", "issuerId", "create", ["-cn", "Local MDM enrollment CA"]),
        ):
            identity = identities.get(setup[field], {})
            if complete.exists():
                revision = next((item for item in identity.get("revisions", [])
                                 if item["id"] == identity.get("active")), None)
                if not revision or datetime.fromisoformat(revision["notAfter"].replace("Z", "+00:00")) <= datetime.now(timezone.utc):
                    raise ValueError(f"{group} identity is missing or expired; restore or renew it explicitly")
                continue
            if not identity.get("active"):
                identity = self.cli("setup", group, operation, *options)["identity"]
                self.cli("setup", group, "activate", "-revision", identity["pending"])

        ca = self.directory / "https-ca.pem"
        if not ca.exists():
            self.cli("setup", "workflow", "export", "-id", setup["httpsCaId"],
                     "-artifact", "certificate", "-out", str(ca))
        if not complete.exists():
            self.write_json(complete, {"version": 1})
        print("Local HTTPS and enrollment issuer ready. Existing keys and configuration preserved.")
        print("Apple push credentials and device admission are still required for real enrollment.")

    def apply_config(self, document):
        current = json.loads(self.path.read_text())
        # This helper edits one local installation, not its storage or certificate
        # identity. Migration and key rotation have separate maintained workflows.
        if not isinstance(document, dict) or document.get("version") != 1:
            raise ValueError("configuration must be a version 1 setup document")
        for name in ("environment", "secretFiles", "setup"):
            if not isinstance(document.get(name), dict):
                raise ValueError(f"{name} must be a JSON object")
        for name in ("environment", "secretFiles"):
            if not all(isinstance(value, str) for value in document[name].values()):
                raise ValueError(f"{name} values must be strings")
        for key in ("DM_STORAGE", "DM_DSN", "DM_STORAGE_KEYS", "DM_SECRETS_DIR"):
            if document["environment"].get(key) != current["environment"].get(key):
                raise ValueError(f"use the recovery/key-rotation workflow to change {key}")
            if document["secretFiles"].get(key) != current["secretFiles"].get(key):
                raise ValueError(f"use the recovery/key-rotation workflow to change {key}")
        for key in ("httpsId", "httpsCaId", "issuerId", "pushId", "vendorId"):
            if document["setup"].get(key) != current["setup"].get(key):
                raise ValueError(f"identity reference {key} must be preserved")
        candidate = self.directory / ".setup-candidate.json"
        self.write_json(candidate, document)
        original = self.path
        try:
            self.path = candidate
            self.cli("setup", "status")
            os.replace(candidate, original)
        finally:
            self.path = original
            candidate.unlink(missing_ok=True)
        print("Configuration saved. Restart dmserver to apply it.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", nargs="?", default="init", choices=("init", "config", "apply-config"))
    args = parser.parse_args()
    bootstrap = Bootstrap()
    bootstrap.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    # Serialize one-shot helpers; the server owns database transaction locking.
    with (bootstrap.directory / ".bootstrap.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if args.operation == "init":
            bootstrap.initialize()
        elif args.operation == "config":
            print(bootstrap.path.read_text(), end="")
        else:
            raw = sys.stdin.buffer.read((1 << 20) + 1)
            if len(raw) > 1 << 20:
                raise ValueError("configuration exceeds 1 MiB")
            bootstrap.apply_config(json.loads(raw))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError) as error:
        print(f"quickstart: {error}", file=sys.stderr)
        sys.exit(1)
