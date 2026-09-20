#!/usr/bin/env python3
"""Run the published schema feature contracts in ordinary project CI."""
import argparse
import json
from pathlib import Path
import subprocess

LIBRARY = "github.com/deploymenttheory/go-apple-dm"
ROOT = Path(__file__).resolve().parents[1]


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")


OS27_TESTS = {
    LIBRARY + "/internal/schemagen/TestSeedOS27CoverageInventory",
    LIBRARY + "/devicemanagement/mdmprotocol/ddm/TestSeedOS27FeatureDelivery",
    LIBRARY + "/devicemanagement/mdmprotocol/ddm/TestSeedOS27FeatureFixtures",
    LIBRARY + "/server/service/TestSeedOS27InstallProfileCompatibility",
    LIBRARY + "/server/service/TestSeedOS27SoftwareUpdateQueryCompatibility",
    LIBRARY + "/server/service/TestSeedOS27EnhancedLogCommands",
    LIBRARY + "/server/service/TestSeedOS27SoftwareUpdateRemoval",
    LIBRARY + "/server/service/TestSeedOS27ReturnToServiceRetry",
    LIBRARY + "/server/ddmadapter/inproc/TestSeedOS27EnhancedLoggingStatus",
    LIBRARY + "/devicemanagement/contentcache/TestSeedOS27ContentCacheContract",
    LIBRARY + "/server/service/TestSeedOS27MixedFleet",
    LIBRARY + "/server/service/TestSeedOS27UpgradeRechecksQueuedCommands",
    LIBRARY + "/devicemanagement/schema/ddm/TestSeedOS27LegacyProfileCompatibility",
}


def missing_test_evidence(output, required):
    passed = set()
    for line in output.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if isinstance(event, dict) and event.get("Action") == "pass" and event.get("Test"):
            passed.add(event.get("Package", "") + "/" + event["Test"])
    return sorted(required - passed)


def verify_contracts(repo, directory, coverage_directory=None):
    """Run the published OS 27 contracts without accepting missing Go tests."""
    directory.mkdir(parents=True, exist_ok=True)
    result = {"stages": {}}
    tags, required = ["-tags", "schema_seed_os_27"], OS27_TESTS
    packages = sorted({"./" + test.removeprefix(LIBRARY + "/").rsplit("/", 1)[0] for test in required})
    command = ["go", "test", "-race", "-count=1", *tags, "-run", "^TestSeedOS27", "-json"]
    if coverage_directory is not None:
        coverage_directory = coverage_directory.resolve()
        coverage_directory.mkdir(parents=True, exist_ok=True)
        command += ["-cover", f"-coverpkg={LIBRARY}/...,{LIBRARY}/server/..."]
    command += packages
    if coverage_directory is not None:
        command += ["-args", f"-test.gocoverdir={coverage_directory}"]
    ok, output = command_stage(result, "tests", command, repo, directory)
    missing = missing_test_evidence(output, required)
    write_json(directory / "result.json", {"passed": ok and not missing,
               "requiredTests": sorted(required), "missingTests": missing})
    if not ok or missing:
        print(output[-6000:])
        print("Required contracts did not pass: " + ", ".join(missing))
        return False
    print(f"PASS: all {len(required)} OS 27 contracts; evidence: {directory}")
    return True


def failure_text(exc):
    if isinstance(exc, subprocess.CalledProcessError):
        return (exc.stderr or exc.stdout or str(exc))[-6000:]
    return str(exc)


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


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--coverage-dir", type=Path)
    args = parser.parse_args()
    raise SystemExit(0 if verify_contracts(ROOT, args.output, args.coverage_dir) else 1)
