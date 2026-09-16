"""Failure and resume contracts, exercised with the built dmctl binary."""

import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile
import unittest

from bootstrap import Bootstrap


@unittest.skipUnless(os.environ.get("DM_QUICKSTART_DMCTL"), "set DM_QUICKSTART_DMCTL to the built CLI")
class BootstrapTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.bootstrap = Bootstrap(self.directory, os.environ["DM_QUICKSTART_DMCTL"])

    def snapshot(self):
        status = self.bootstrap.cli("setup", "status")
        identities = {identity["id"]: identity for identity in status["identities"]}
        secrets = {path.name: hashlib.sha256(path.read_bytes()).hexdigest()
                   for path in (self.directory / "secrets").iterdir()}
        return identities, secrets

    def test_restart_preserves_identity_keys_and_operator_configuration(self):
        self.bootstrap.initialize()
        before = self.snapshot()
        document = json.loads(self.bootstrap.path.read_text())
        document["environment"]["DM_ORGANIZATION"] = "Example Lab"
        self.bootstrap.apply_config(document)
        self.bootstrap.initialize()
        self.assertEqual(before, self.snapshot())
        self.assertEqual(document, json.loads(self.bootstrap.path.read_text()))
        self.assertEqual(0o600, self.bootstrap.path.stat().st_mode & 0o777)

    def test_resume_reuses_pending_issuer_key(self):
        self.bootstrap.cli("setup", "init", "-dir", str(self.directory), setup=False)
        identity = self.bootstrap.cli("setup", "issuer", "create", "-cn", "Local MDM enrollment CA")["identity"]
        pending = identity["pending"]
        before = self.snapshot()[1]
        self.bootstrap.initialize()
        identities, secrets = self.snapshot()
        self.assertEqual(pending, identities["issuer"]["active"])
        self.assertEqual(1, len(identities["issuer"]["revisions"]))
        self.assertEqual(before, secrets)

    def test_resume_reuses_pending_https_certificate(self):
        self.bootstrap.cli("setup", "init", "-dir", str(self.directory), setup=False)
        identity = self.bootstrap.cli("setup", "https", "lab", "-cn", "Local MDM HTTPS",
                                      "-hosts", "localhost,127.0.0.1,dmserver")["identity"]
        before = self.snapshot()[1]
        self.bootstrap.initialize()
        identities, secrets = self.snapshot()
        self.assertEqual(identity["pending"], identities["https"]["active"])
        self.assertEqual(identity["revisions"][0]["fingerprint"],
                         identities["https"]["revisions"][0]["fingerprint"])
        self.assertEqual(before, secrets)

    def test_missing_key_or_config_is_not_recreated(self):
        self.bootstrap.initialize()
        secret = self.directory / "secrets" / "storage"
        secret.unlink()
        with self.assertRaises(ValueError):
            self.bootstrap.initialize()
        self.assertFalse(secret.exists())
        self.bootstrap.path.unlink()
        with self.assertRaisesRegex(ValueError, "restore"):
            self.bootstrap.initialize()
        self.assertFalse(self.bootstrap.path.exists())

    def test_invalid_configuration_keeps_last_document(self):
        self.bootstrap.initialize()
        original = self.bootstrap.path.read_bytes()
        document = json.loads(original)
        changes = [
            ("environment", "DM_DSN", "/tmp/other.sqlite"),
            ("secretFiles", "DM_DSN", "secrets/admin"),
            ("environment", "DM_STORAGE_KEYS", "new"),
            ("setup", "httpsId", "new"),
            ("environment", "DM_AUDIT_STORE", "not-a-bool"),
            ("secretFiles", "DM_ADMIN_TOKEN", "missing-token"),
        ]
        for section, key, value in changes:
            with self.subTest(key=key, section=section):
                changed = copy.deepcopy(document)
                changed[section][key] = value
                with self.assertRaises(ValueError):
                    self.bootstrap.apply_config(changed)
                self.assertEqual(original, self.bootstrap.path.read_bytes())
                self.assertFalse((self.directory / ".setup-candidate.json").exists())


if __name__ == "__main__":
    unittest.main()
