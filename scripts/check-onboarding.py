#!/usr/bin/env python3
"""Check onboarding links/JSON and execute its complete offline Go examples."""

import json
from pathlib import Path
import re
import subprocess
import tempfile
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "docs/getting-started"


def anchors(path):
    text = path.read_text()
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    counts = {}
    found = set()
    for heading in re.findall(r"^#{1,6} (.+)$", text, flags=re.M):
        slug = re.sub(r"[^\w\- ]", "", heading.lower()).replace(" ", "-")
        number = counts.get(slug, 0)
        counts[slug] = number + 1
        found.add(slug + (f"-{number}" if number else ""))
    return found


def main():
    paths = sorted(DOCS.glob("*.md")) + [ROOT / "deploy/quickstart/README.md"]
    for path in paths:
        text = path.read_text()
        for raw in re.findall(r"\]\(([^\s)]+)\)", text):
            link = urlsplit(raw)
            if link.scheme or link.netloc:
                continue
            target = (path.parent / unquote(link.path)).resolve() if link.path else path
            if not target.exists():
                raise ValueError(f"{path.relative_to(ROOT)}: missing {raw}")
            if link.fragment and target.suffix == ".md" and unquote(link.fragment) not in anchors(target):
                raise ValueError(f"{path.relative_to(ROOT)}: missing anchor {raw}")
        for block in re.findall(r"```json\n(.*?)\n```", text, flags=re.S):
            json.loads(block)
        for index, block in enumerate(re.findall(r"```go\n(.*?)\n```", text, flags=re.S), 1):
            with tempfile.TemporaryDirectory(prefix="dm-onboarding-") as directory:
                source = Path(directory) / "main.go"
                source.write_text(block + "\n")
                result = subprocess.run(["go", "run", str(source)], cwd=ROOT,
                                        capture_output=True, text=True, check=True)
                print(f"{path.name}: Go example {index} passed")
                if "expected the device identity gate" in block:
                    assert result.stdout.strip() == "Check-in without a device certificate: 403"
                else:
                    assert "DeviceInformation" in result.stdout and "CommandUUID" in result.stdout
        print(f"{path.name}: local links and JSON passed")


if __name__ == "__main__":
    main()
