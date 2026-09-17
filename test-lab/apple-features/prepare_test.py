import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("prepare", Path(__file__).with_name("prepare.py"))
prepare = importlib.util.module_from_spec(spec)
spec.loader.exec_module(prepare)


class PrepareTests(unittest.TestCase):
    def test_dependencies_and_activation(self):
        items = prepare.bundle(["managedapp", "network.vpn.ikev2"])
        ids = [item["Identifier"] for item in items]
        self.assertEqual(len(ids), len(set(ids)))
        for item in items:
            for ref in prepare.references(item["Payload"]):
                self.assertIn(ref, ids)
        active = items[-1]["Payload"]["StandardConfigurations"]
        self.assertEqual(len(active), 2)

    def test_overrides_isolated(self):
        payload = {"ProfileURL": "https://lab.example/profile.mobileconfig"}
        prepared = prepare.bundle(["legacy-url"], {"legacy-url": payload})
        self.assertEqual(prepared[0]["Payload"], payload)
        self.assertNotEqual(prepare.bundle(["legacy-url"])[0]["Payload"], payload)

    def test_additional_macos27_bundles_include_shared_profile_asset(self):
        selected = ["accessibility", "webcontent-filter", "siri-sensitive-content",
                    "legacy-interactive-asset", "managedapp-legacy-config"]
        items = prepare.bundle(selected)
        ids = [item["Identifier"] for item in items]
        self.assertEqual(ids.count("com.deploymenttheory.acceptance.data"), 1)
        self.assertEqual(len(items[-1]["Payload"]["StandardConfigurations"]), 5)
        for item in items:
            for ref in prepare.references(item["Payload"]):
                self.assertIn(ref, ids)

    def test_unknown_and_asset_only(self):
        for features in [["not-a-feature"], ["acme"]]:
            with self.assertRaises(ValueError):
                prepare.bundle(features)
        with self.assertRaises(ValueError):
            prepare.bundle(["managedapp"], {"unknown": {}})
