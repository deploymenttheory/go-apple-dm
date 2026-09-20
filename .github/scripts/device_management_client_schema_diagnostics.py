"""Explain generator failures using recorded diagnostics and source locations."""
import difflib
import hashlib
import json
from pathlib import Path
import re


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":")).encode()).hexdigest()


def diagnosis(stage, message):
    """Return a stable cause and evidence-based implementation guidance."""
    unknown = re.search(r"field (\S+) not found in type (\S+)", message)
    shape = re.search(r"cannot unmarshal (!!\w+).*? into (\S+)", message)
    scalar = re.search(r'unsupported type "([^"]+)"', message)
    if unknown:
        field, owner = unknown.groups()
        cause = f"unknown-field:{owner}:{field}"
        title = f"Support {owner}.{field} schema metadata"
        why = f"Strict decoding rejects Apple's `{field}` field because `{owner}` does not model it."
        change = (f"Check `{field}` against Apple's meta-schema and examples; add its typed YAML field to `{owner}`. "
                  "If it affects generated payloads, extend type construction and emission. Keep KnownFields enabled.")
        source, symbol = "internal/schemagen/model.go", "type " + owner.rsplit(".", 1)[-1] + " struct"
        test = "Add a minimal schema fixture with the new field, its valid values and invalid shapes; assert parsing and the relevant generated output."
    elif shape:
        actual, expected = shape.groups()
        cause = f"yaml-shape:{actual}:{expected}"
        title = f"Support Apple's {actual} representation for {expected}"
        why = f"Apple supplies `{actual}`, but the strict decoder expects `{expected}`."
        change = ("Confirm the representation in Apple's source and meta-schema; normalise the supported YAML forms "
                  "before strict decoding, retaining validation of nested fields.")
        if expected == "[]schemagen.Example":
            change = ("Normalise a single examples mapping to a one-element sequence in Parse before strict decoding into "
                      "Schema.Examples. Preserve list support and rejection of unknown nested example fields.")
        source, symbol = "internal/schemagen/model.go", "func Parse("
        test = "Add fixtures for both observed YAML shapes, unknown nested fields and malformed values; assert the intended decoded values and generated output."
    elif scalar:
        value = scalar.group(1)
        cause, title = "unsupported-type:" + value, "Support Apple schema type " + value
        why = f"The generator has no Go type mapping for Apple's `{value}` type."
        change = ("Establish the wire representation from Apple's meta-schema, add its mapping in scalarType/resolveType, "
                  "and extend emitters or validation where the type requires it.")
        source, symbol = "internal/schemagen/types.go", "func (b *builder) resolveType("
        test = "Add a minimal schema fixture for the new type, assert its generated Go field and validation, and compile the generated package."
    else:
        # Preserve diagnostic distinctions; only ephemeral locations are removed.
        stable = re.sub(r"line \d+:", "line:", message.strip())
        stable = re.sub(r"(?:[^\s:]+/)*[^\s:]+\.(?:yaml|go):(?:\d+:\d+:)?", "<source>:", stable)
        cause, title = stage + ":" + digest(stable)[:20], "Investigate " + stage + " failure in schema generation"
        source, symbol = {
            "parse": ("internal/schemagen/model.go", "func Parse("),
            "build-types": ("internal/schemagen/types.go", "func Build("),
            "generate": ("internal/schemagen/emit.go", "func Generate("),
            "verify": ("internal/schemagen/generate.go", "func Verify("),
            "compile": ("internal/schemagen/emit_types.go", ""),
        }.get(stage, ("internal/schemagen/generate.go", "func Run("))
        why = "The recorded diagnostic identifies a failure in " + stage + "; a precise implementation fix needs investigation."
        change = "Minimise the linked input and trace the diagnostic through the linked generator stage. The monitor has not verified a specific code change."
        if stage == "parse" and any(s in message for s in ("did not find", "could not find", "mapping values are not allowed")):
            why = "The YAML parser rejects the input syntax. This may require an upstream correction rather than new generator support."
            change = "Verify the original YAML at the recorded Apple commit and report malformed input upstream; do not weaken strict parsing to accept it."
        test = "Add the smallest reproducing schema fixture and assert the corrected output, deterministic regeneration and successful compilation."
    return {"key": "codegen:" + cause, "stage": stage, "title": title, "why": why,
            "change": change, "test": test, "source": source, "symbol": symbol, "evidence": []}


def failure_stage(text):
    if "decode:" in text or "unmarshal" in text:
        return "parse"
    if "schemagen: naming" in text:
        return "build-types"
    return "generate"


def source_location(root, item):
    path = Path(root) / item["source"]
    if item["symbol"] and path.is_file():
        for number, line in enumerate(path.read_text().splitlines(), 1):
            if line.startswith(item["symbol"]):
                return item["source"] + "#L" + str(number)
    return item["source"]


def schema_excerpt(root, relative):
    """Read only a schema file contained in the recorded checkout."""
    path = (Path(root) / relative).resolve()
    if not path.is_relative_to(Path(root).resolve()) or not path.is_file():
        return ""
    return path.read_text(errors="replace")


def findings(stage, text, project, baseline, candidate, audit=None):
    observations = []
    if stage == "generate" and audit:
        for item in audit.get("findings", []):
            if item["stage"] == "parse" and item["kind"] == "failure":
                observations.extend(("parse", e["path"], e["detail"]) for e in item["evidence"])
    if not observations:
        path = re.search(r"((?:mdm|declarative|other)/[^\s:]+\.yaml)", text)
        observations = [(failure_stage(text) if stage == "generate" else stage,
                         path.group(1) if path else stage + ".log", text[-6000:])]
    grouped = {}
    for phase, path, detail in observations:
        # A single YAML document can report several unrelated unsupported fields.
        messages = re.findall(r"field \S+ not found in type \S+|cannot unmarshal !![^\n]+", detail)
        for message in messages or [detail]:
            item = diagnosis(phase, message)
            entry = grouped.setdefault(item["key"], dict(item, location=source_location(project, item)))
            evidence = {"path": path, "detail": message}
            if evidence not in entry["evidence"]:
                entry["evidence"].append(evidence)
    for item in grouped.values():
        item["evidence"].sort(key=lambda e: (e["path"], e["detail"]))
        item["examples"] = []
        for relative in sorted({e["path"] for e in item["evidence"]})[:3]:
            before, after = schema_excerpt(baseline, relative), schema_excerpt(candidate, relative)
            diff = list(difflib.unified_diff(before.splitlines(), after.splitlines(),
                                           fromfile="control/" + relative, tofile="candidate/" + relative, lineterm=""))
            item["examples"].append({"path": relative, "diff": "\n".join(diff[:100]),
                                     "truncated": len(diff) > 100})
    return sorted(grouped.values(), key=lambda item: item["key"])
