#!/usr/bin/env python3
"""Prepare an isolated DDM fixture bundle; never contacts a server or device."""
import argparse
import copy
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PREFIX = "com.deploymenttheory.acceptance."


def references(value, key=""):
    """Follow reference fields used by these fixtures, excluding ordinary strings."""
    if (isinstance(value, str) and value.startswith(PREFIX)
            and (key.endswith("Reference") or key.endswith("References")
                 or key in {"PublicKeyData", "StandardConfigurations"})):
        yield value
    elif isinstance(value, dict):
        for name, child in value.items():
            yield from references(child, name)
    elif isinstance(value, list):
        for child in value:
            yield from references(child, key)


def bundle(features, overrides=None):
    manifest = json.loads((ROOT / "manifest.json").read_text())
    declarations = {}
    names = {}
    for feature in manifest["features"]:
        declaration = json.loads((ROOT / feature["file"]).read_text())
        declarations[declaration["Identifier"]] = declaration
        names[feature["id"]] = declaration["Identifier"]
    for name, payload in (overrides or {}).items():
        if name not in names or not isinstance(payload, dict):
            raise ValueError("overrides must map known fixture names to complete payload objects")
        declarations[names[name]]["Payload"] = payload
    selected = {}

    def include(identifier):
        if identifier in selected:
            return
        if identifier not in declarations:
            raise ValueError("missing fixture dependency: " + identifier)
        declaration = copy.deepcopy(declarations[identifier])
        selected[identifier] = declaration
        for reference in references(declaration["Payload"]):
            include(reference)

    for feature in features:
        if feature not in names:
            raise ValueError("unknown feature: " + feature)
        include(names[feature])
    configurations = sorted(key for key, value in selected.items()
                            if value["Type"].startswith("com.apple.configuration."))
    if not configurations:
        raise ValueError("select at least one configuration")
    activation = {"Type": "com.apple.activation.simple", "Identifier": PREFIX + "activation",
                  "Payload": {"StandardConfigurations": configurations}}
    return sorted(selected.values(), key=lambda item: item["Identifier"]) + [activation]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--feature", action="append", required=True,
                        help="fixture ID; repeat to combine reviewed features")
    parser.add_argument("--overrides", type=Path,
                        help="private JSON mapping fixture IDs to complete replacement Payload objects")
    parser.add_argument("--out", type=Path, required=True, help="new output directory; refuses overwrite")
    args = parser.parse_args()
    overrides = json.loads(args.overrides.read_text()) if args.overrides else None
    declarations = bundle(args.feature, overrides)
    args.out.mkdir(mode=0o700, parents=True, exist_ok=False)
    files = []
    for declaration in declarations:
        name = declaration["Identifier"].removeprefix(PREFIX) + ".json"
        path = args.out / name
        with path.open("x") as output:
            path.chmod(0o600)
            output.write(json.dumps(declaration, indent=2) + "\n")
        files.append(name)
    (args.out / "bundle.json").write_text(json.dumps({"features": args.feature, "files": files,
        "reviewRequired": True, "live": "pending"}, indent=2) + "\n")
    print(f"Prepared {len(declarations)} declarations in {args.out}; review lab values before assignment.")


if __name__ == "__main__":
    main()
