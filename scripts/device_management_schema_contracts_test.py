import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("contracts", Path(__file__).with_name("device-management-schema-contracts.py"))
m = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(m)


class ContractTests(unittest.TestCase):
    def test_routine_contract_gate_rejects_missing_skipped_and_failed_tests(self):
        events = "\n".join(json.dumps({"Action": "pass", "Package": test.rsplit("/", 1)[0],
                                     "Test": test.rsplit("/", 1)[1]}) for test in m.OS27_TESTS)
        for ok, output, expected in [(True, events, True), (True, "", False),
                                      (True, events.replace('"pass"', '"skip"'), False),
                                      (False, events, False)]:
            with self.subTest(ok=ok, expected=expected), tempfile.TemporaryDirectory() as tmp:
                with patch.object(m, "command_stage", return_value=(ok, output)), patch("builtins.print"):
                    self.assertEqual(expected, m.verify_contracts(Path(tmp), Path(tmp)))
                self.assertEqual(expected, json.loads((Path(tmp) / "result.json").read_text())["passed"])

    def test_contract_coverage_preserves_existing_unit_data(self):
        events = "\n".join(json.dumps({"Action": "pass", "Package": test.rsplit("/", 1)[0],
                                     "Test": test.rsplit("/", 1)[1]}) for test in m.OS27_TESTS)
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            coverage = root / "unit coverage"
            coverage.mkdir()
            existing = coverage / "covcounters.existing"
            existing.write_bytes(b"unit data")
            with patch.object(m, "command_stage", return_value=(True, events)) as stage, patch("builtins.print"):
                self.assertTrue(m.verify_contracts(root, root / "contracts", coverage))
            command = stage.call_args.args[2]
            self.assertIn("-cover", command)
            self.assertIn(f"-coverpkg={m.LIBRARY}/...,{m.LIBRARY}/server/...", command)
            self.assertEqual(["-args", f"-test.gocoverdir={coverage.resolve()}"], command[-2:])
            self.assertEqual(b"unit data", existing.read_bytes())


    def test_invalid_test_events_do_not_satisfy_contracts(self):
        self.assertEqual(sorted(m.OS27_TESTS), m.missing_test_evidence('garbled\nnull\n[]', m.OS27_TESTS))


if __name__ == "__main__":
    unittest.main()
