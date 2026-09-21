#!/usr/bin/env python3
"""Check local documentation links and pinned diagram evidence without network I/O.

This proves reference integrity, not the truth of prose or vendor conformance.
Archify delivery and browser review remain separate checks.
"""

import argparse
from functools import lru_cache
import html
from html.parser import HTMLParser
import json
from itertools import combinations
from pathlib import Path
import re
import subprocess
from urllib.parse import unquote, urlsplit


REPOSITORY = "https://github.com/deploymenttheory/go-apple-dm"
URL = re.compile(r'https?://[^\s<>"\x27]+')
INLINE_LINK = re.compile(r"\[[^\]\n]*\]\((<[^>]+>|[^\s)]+)(?:\s+\"[^\"]*\")?\)")
LINKED_IMAGE = re.compile(r"\[\s*!\[[^\]\n]*\]\([^\n)]*\)\s*\]\((<[^>]+>|[^\s)]+)(?:\s+\"[^\"]*\")?\)")
REFERENCE_LINK = re.compile(r"^\s*\[[^\]]+\]:\s*(<[^>]+>|\S+)", re.MULTILINE)
HTML_LINK = re.compile(r'(?:href|src)=["\x27]([^"\x27]+)["\x27]')
LINE_ANCHOR = re.compile(r"L([1-9][0-9]*)(?:-L([1-9][0-9]*))?\Z")
PIN = re.compile(r"[0-9a-f]{40}\Z")
DIAGRAM_COLLECTIONS = {
    "architecture": ("components", "connections"),
    "workflow": ("nodes", "edges"),
    "sequence": ("participants", "messages"),
    "dataflow": ("nodes", "flows"),
    "lifecycle": ("states", "transitions"),
}


def normalized_text(value):
    """Ignore formatting whitespace while retaining authored words and punctuation."""
    return " ".join(value.split())


def card_text(value):
    """Match reference captions after the renderer turns their URLs into anchors."""
    return normalized_text(re.sub(r"\s+[—–-]\s+https?://\S+", "", value))


def route_segments(path):
    """Extract straight portions of the renderer's absolute SVG paths, excluding curves."""
    tokens = re.findall(r"[A-Za-z]|[-+]?(?:\d*\.)?\d+(?:[eE][-+]?\d+)?", path)
    position, segments, index, command = None, [], 0, None
    arity = {"M": 2, "L": 2, "Q": 4, "C": 6}
    while index < len(tokens):
        if tokens[index].isalpha():
            command = tokens[index]
            index += 1
        count = arity.get(command)
        if count is None or index + count > len(tokens):
            return segments
        try:
            values = [float(value) for value in tokens[index:index + count]]
        except ValueError:
            return segments
        target = tuple(values[-2:])
        if command == "L" and position is not None:
            segments.append((position, target))
        position = target
        index += count
        if command == "M":
            command = "L"
    return segments


def opposing_overlap(first, second):
    """Measure collinear overlap only when the two directed segments oppose each other."""
    for axis in (0, 1):
        cross = 1 - axis
        coordinates = [point[cross] for segment in (first, second) for point in segment]
        if max(coordinates) - min(coordinates) > 0.01:
            continue
        a, b = (point[axis] for point in first)
        c, d = (point[axis] for point in second)
        if (b - a) * (d - c) < 0:
            return max(0, min(max(a, b), max(c, d)) - max(min(a, b), min(c, d)))
    return 0


class DiagramHTML(HTMLParser):
    """Read delivered graph metadata and visible cards without executing viewer code."""

    def __init__(self, source):
        """Collect all occurrences so an inconsistent path and label cannot conceal drift."""
        super().__init__(convert_charrefs=True)
        self.nodes = {}
        self.edges = {}
        self.cards = []
        self.routes = []
        self.card_depth = 0
        self.field = None
        self.card_links = None
        self.graph_stack = []
        self.feed(source)

    def handle_starttag(self, tag, attributes):
        """Capture graph elements and the caption/list structure of explanatory cards."""
        attrs = dict(attributes)
        graph_element = None
        for key, collection in (("data-node-id", self.nodes), ("data-edge-id", self.edges)):
            if key in attrs:
                collection.setdefault(attrs[key], []).append(attrs)
                attrs["visible_text"] = []
                graph_element = attrs
        if tag == "svg" or self.graph_stack:
            parent = self.graph_stack[-1] if self.graph_stack else (None, None, False)
            self.graph_stack.append((tag, graph_element if graph_element is not None else parent[1],
                                     tag == "text" or parent[2]))
            if tag == "text" and self.graph_stack[-1][1] is not None:
                self.graph_stack[-1][1]["visible_text"].append([])
            element = self.graph_stack[-1][1]
            if tag == "path" and element is not None and "data-edge-id" in element:
                self.routes.append((element["data-edge-id"], route_segments(attrs.get("d", ""))))
        if tag == "div":
            if self.card_depth:
                self.card_depth += 1
            elif "card" in (attrs.get("class") or "").split():
                self.card_depth = 1
                self.cards.append({"title": [], "items": [], "links": []})
        if self.card_depth:
            if tag == "h3":
                self.field = self.cards[-1]["title"]
            elif tag == "li":
                self.field = []
                self.cards[-1]["items"].append(self.field)
                self.card_links = []
                self.cards[-1]["links"].append(self.card_links)
            elif tag == "a" and self.card_links is not None:
                self.card_links.append(attrs.get("href", ""))

    def handle_endtag(self, tag):
        """End the current card or visible text field."""
        if self.graph_stack and self.graph_stack[-1][0] == tag:
            self.graph_stack.pop()
        if tag in ("h3", "li"):
            self.field = None
            self.card_links = None
        if tag == "div" and self.card_depth:
            self.card_depth -= 1

    def handle_data(self, data):
        """Keep visible graph and card text, excluding copies in scripts or attributes."""
        if self.graph_stack:
            _, element, visible = self.graph_stack[-1]
            if visible and element is not None:
                element["visible_text"][-1].append(data)
        if self.field is not None:
            self.field.append(data)

    def card_values(self):
        """Return normalized captions and list items in their delivered order."""
        return [{"title": normalized_text("".join(card["title"])),
                 "items": [re.sub(r"^•\s*", "", normalized_text("".join(item)))
                           for item in card["items"]], "links": card["links"]}
                for card in self.cards]


def prose(text):
    """Remove fenced examples while preserving line numbers for diagnostics."""
    lines = []
    fence = None
    for line in text.splitlines():
        opening = re.match(r"^\s*(`{3,}|~{3,})", line)
        if opening:
            mark = opening.group(1)
            if fence is None:
                fence = mark
            elif mark[0] == fence[0] and len(mark) >= len(fence):
                fence = None
            lines.append("")
        else:
            lines.append("" if fence else line)
    return "\n".join(lines)


def anchors(text):
    """Read GitHub-style ATX heading anchors, duplicates and explicit HTML IDs."""
    text = prose(text)
    found = set(re.findall(r'(?:id|name)=["\x27]([^"\x27]+)["\x27]', text))
    counts = {}
    for title in re.findall(r"^#{1,6}\s+(.+?)\s*#*\s*$", text, re.MULTILINE):
        title = re.sub(r"<[^>]+>", "", html.unescape(title))
        title = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", title)
        slug = re.sub(r"[^\w\- ]", "", title.lower()).replace(" ", "-")
        count = counts.get(slug, 0)
        counts[slug] = count + 1
        found.add(slug + (f"-{count}" if count else ""))
    return found


def local_links(text):
    """Yield explicit Markdown and HTML destinations outside fenced examples."""
    text = prose(text)
    for pattern in (INLINE_LINK, LINKED_IMAGE, REFERENCE_LINK, HTML_LINK):
        for match in pattern.finditer(text):
            yield text.count("\n", 0, match.start()) + 1, match.group(1).strip("<>")


def strings(value):
    """Walk JSON values to find links in source cards and supporting manifests."""
    if isinstance(value, str):
        yield value
    elif isinstance(value, list):
        for item in value:
            yield from strings(item)
    elif isinstance(value, dict):
        for item in value.values():
            yield from strings(item)


def implementation_reference(url):
    """Recognize pinned source and contract files, excluding documentation navigation."""
    parsed = urlsplit(html.unescape(url))
    prefix = "/deploymenttheory/go-apple-dm/blob/"
    if parsed.netloc != "github.com" or not parsed.path.startswith(prefix):
        return False
    revision, separator, name = parsed.path[len(prefix):].partition("/")
    name = unquote(name)
    path = Path(name)
    return bool(separator and PIN.fullmatch(revision) and not name.startswith("docs/") and (
        path.suffix in {".go", ".sql", ".py", ".sh", ".yml", ".yaml", ".json", ".lock"}
        or path.name in {"Dockerfile", "Makefile"}
    ))


class Checker:
    """Accumulate actionable path-level findings against one local Git checkout."""

    def __init__(self, root):
        """Use an explicit repository root so tests can isolate their fixtures."""
        self.root = Path(root).resolve()
        self.errors = []
        self.checked_links = 0
        self.checked_sources = 0

    def error(self, owner, message):
        """Attach a finding to the authored document or source that needs repair."""
        self.errors.append(f"{owner}: {message}")

    @lru_cache(maxsize=None)
    def tracked_paths(self):
        """Include indexed files, submodule contents and their directory ancestors."""
        result = subprocess.run(
            ["git", "ls-files", "--cached", "--recurse-submodules", "-z"],
            cwd=self.root, capture_output=True, text=True, check=True,
        )
        paths = set()
        for name in result.stdout.split("\0"):
            if name:
                path = Path(name)
                paths.add(path)
                paths.update(path.parents)
        return paths

    def tracked_target(self, target):
        """Reject local leftovers that will not exist in a clean Git checkout."""
        try:
            return target.resolve().relative_to(self.root) in self.tracked_paths()
        except ValueError:
            return False

    @lru_cache(maxsize=None)
    def git_object(self, revision, path):
        """Read evidence from its pinned revision, never from shifted working lines."""
        return subprocess.run(
            ["git", "show", f"{revision}:{path}"], cwd=self.root,
            capture_output=True, text=True, check=True,
        ).stdout

    def code_link(self, owner, url, require_pin=False):
        """Check repository paths and optional line spans; remote hosts are untouched."""
        parsed = urlsplit(html.unescape(url))
        prefix = "/deploymenttheory/go-apple-dm/"
        if parsed.netloc != "github.com" or not parsed.path.startswith(prefix):
            return
        parts = parsed.path[len(prefix):].split("/", 2)
        if len(parts) != 3 or parts[0] not in ("blob", "tree"):
            return
        _, revision, path = parts
        path = unquote(path)
        if require_pin and not PIN.fullmatch(revision):
            self.error(owner, f"diagram code reference must use a full commit: {url}")
            return
        self.checked_links += 1
        try:
            if revision in ("main", "HEAD"):
                target = self.root / path
                if not target.exists():
                    raise ValueError("path is absent from the checkout")
                if not self.tracked_target(target):
                    raise ValueError("target is not tracked by Git")
                text = target.read_text() if target.is_file() else ""
            else:
                text = self.git_object(revision, path)
            if parsed.fragment.startswith("L"):
                self.line_range(owner, url, text, parsed.fragment)
        except (OSError, ValueError, subprocess.CalledProcessError) as exc:
            self.error(owner, f"unresolved code reference {url}: {exc}")

    def line_range(self, owner, label, text, fragment):
        """Reject malformed, reversed and out-of-file GitHub line anchors."""
        match = LINE_ANCHOR.fullmatch(fragment)
        if not match:
            self.error(owner, f"invalid code line anchor: {label}")
            return
        start = int(match.group(1))
        end = int(match.group(2) or start)
        if not 1 <= start <= end <= len(text.splitlines()):
            self.error(owner, f"code line range is outside its pinned file: {label}")

    def local_link(self, owner, url):
        """Resolve local links relative to their containing document and check fragments."""
        parsed = urlsplit(html.unescape(url))
        if parsed.scheme or parsed.netloc:
            return
        path = unquote(parsed.path)
        if not path and not parsed.fragment:
            return
        source = self.root / str(owner).split(":", 1)[0]
        target = (self.root / path.lstrip("/")) if path.startswith("/") else source.parent / path
        if not path:
            target = source
        self.checked_links += 1
        if not target.exists():
            self.error(owner, f"missing local target: {url}")
        elif not self.tracked_target(target):
            self.error(owner, f"local target is not tracked by Git: {url}")
        elif parsed.fragment and target.suffix == ".md":
            if unquote(parsed.fragment) not in anchors(target.read_text()):
                self.error(owner, f"missing Markdown anchor: {url}")

    def markdown(self, name):
        """Check maintained Markdown links, including linked images and source citations."""
        text = (self.root / name).read_text()
        for line, link in local_links(text):
            owner = f"{name}:{line}"
            self.local_link(owner, link)
            self.code_link(owner, link)

    def diagram_content(self, owner, spec, source):
        """Detect stale graph semantics and cards independently of source-link presence."""
        collections = DIAGRAM_COLLECTIONS.get(spec.get("diagram_type"))
        if collections is None:
            return  # Archify owns schema validation; isolated reference fixtures omit the type.
        rendered = DiagramHTML(source)
        for collection, actual, fields in (
            (collections[0], rendered.nodes, {"label": "data-node-label", "sublabel": "data-node-sublabel", "purpose": "data-purpose"}),
            (collections[1], rendered.edges, {"from": "data-edge-from", "to": "data-edge-to", "label": "data-edge-label", "purpose": "data-purpose"}),
        ):
            expected = {item["id"]: item for item in spec.get(collection, [])}
            if expected.keys() != actual.keys():
                self.error(owner, f"delivered {collection} inventory differs: missing {sorted(expected.keys() - actual.keys())}; unexpected {sorted(actual.keys() - expected.keys())}")
            for identifier in expected.keys() & actual.keys():
                for field, attribute in fields.items():
                    value = normalized_text(expected[identifier].get(field, ""))
                    if any(normalized_text(element.get(attribute, "")) != value for element in actual[identifier]):
                        self.error(owner, f"delivered {collection} {identifier} {field} differs from source")
                for field in ("label", "sublabel"):
                    value = normalized_text(expected[identifier].get(field, ""))
                    if value and not any(value == normalized_text(" ".join(block))
                                         for element in actual[identifier] for block in element["visible_text"]):
                        self.error(owner, f"delivered {collection} {identifier} visible {field} differs from source")
        cards = [{"title": normalized_text(card.get("title", "")),
                  "items": [card_text(item) for item in card.get("items", [])],
                  "links": [[match.group(0).rstrip(".,;)]") for match in URL.finditer(item)]
                            for item in card.get("items", [])]}
                 for card in spec.get("cards", [])]
        if cards != rendered.card_values():
            self.error(owner, "delivered explanatory cards differ from source")
        for (first, a), (second, b) in combinations(rendered.routes, 2):
            if first == second:
                continue
            overlap = max((opposing_overlap(x, y) for x in a for y in b), default=0)
            if overlap >= 8:
                self.error(owner, f"opposing diagram routes {first} and {second} share {overlap:g}px")

    def diagram(self, name):
        """Check source coverage, immutable evidence and links in the delivered HTML."""
        spec = json.loads((self.root / name).read_text())
        stem = Path(name).name.rsplit(".", 2)[0]
        artifact = self.root / "docs/diagrams" / f"{stem}.html"
        if not artifact.is_file():
            self.error(name, f"missing delivered HTML: {artifact.name}")
            rendered = None
        else:
            raw = artifact.read_text()
            rendered = html.unescape(raw)
            if not rendered.strip():
                self.error(name, f"delivered HTML is empty: {artifact.name}")
            self.diagram_content(name, spec, raw)
        repository = spec.get("meta", {}).get("repository", {})
        revision = repository.get("revision", "")
        for component in spec.get("components", []):
            sources = component.get("sources", [])
            if component.get("type") not in ("external", "cloud") and not any(
                implementation_reference(f"{REPOSITORY}/blob/{revision}/{source['path']}")
                for source in sources
            ):
                self.error(name, f"component {component['id']} has no implementation source")
            for source in sources:
                if not PIN.fullmatch(revision) or repository.get("url", "").rstrip("/") != REPOSITORY:
                    self.error(name, "component evidence requires the repository URL and full commit")
                    continue
                url = f"{REPOSITORY}/blob/{revision}/{source['path']}"
                if "line" in source:
                    url += f"#L{source['line']}"
                    if "end_line" in source:
                        url += f"-L{source['end_line']}"
                self.code_link(name, url, require_pin=True)
                self.checked_sources += 1
                if rendered is not None and url not in rendered:
                    self.error(name, f"component source is absent from delivered HTML: {url}")
        urls = {match.group(0).rstrip(".,;)]") for value in strings(spec) for match in URL.finditer(value)}
        for url in sorted(urls):
            # Guide navigation may follow main; implementation evidence is immutable.
            code = f"{REPOSITORY}/blob/" in url or f"{REPOSITORY}/tree/" in url
            guide = re.search(r"/(?:blob|tree)/[^/]+/docs/", url)
            self.code_link(name, url, require_pin=code and not guide)
            if code and not guide:
                self.checked_sources += 1
            if rendered is not None and url not in rendered and url != repository.get("url"):
                self.error(name, f"source reference is absent from delivered HTML: {url}")
        if not spec.get("components"):
            cards = list(strings(spec.get("cards", [])))
            if not any(implementation_reference(url) for url in urls):
                self.error(name, "diagram has no pinned implementation card links")
            for kind in ("nodes", "participants", "states"):
                for entity in spec.get(kind, []):
                    if entity.get("type") in ("external", "cloud"):
                        continue
                    prefix = entity["id"] + ":"
                    if not any(item.startswith(prefix) and any(
                        implementation_reference(match.group(0).rstrip(".,;)]"))
                        for match in URL.finditer(item)
                    ) for item in cards):
                        self.error(name, f"{entity['id']} needs a named implementation card: {prefix} label — pinned URL")

    def run(self):
        """Check tracked and newly authored files, excluding ignored and vendor content."""
        result = subprocess.run(
            ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
            cwd=self.root, capture_output=True, text=True, check=True,
        )
        files = sorted(set(result.stdout.split("\0")))
        for name in files:
            if not name or name.startswith(("third_party/", "vendor/")) or not (self.root / name).is_file():
                continue
            try:
                if name.endswith(".md"):
                    self.markdown(name)
                elif name.startswith("docs/diagrams/src/") and name.endswith(".json"):
                    self.diagram(name)
                elif name.startswith("test-lab/") and name.endswith(".json"):
                    data = json.loads((self.root / name).read_text())
                    for value in strings(data):
                        for _, link in local_links(value):
                            self.local_link(name, link)
                        if value.startswith("docs/") and value.endswith(".md"):
                            self.local_link("README.md", value)
                elif name.endswith("doc.go"):
                    for match in URL.finditer((self.root / name).read_text()):
                        self.code_link(name, match.group(0).rstrip(".,;)"))
            except (OSError, ValueError) as exc:
                self.error(name, str(exc))
        sources = {Path(name).name.rsplit(".", 2)[0] for name in files if name.startswith("docs/diagrams/src/") and name.endswith(".json") and (self.root / name).is_file()}
        for artifact in (self.root / "docs/diagrams").glob("*.html"):
            if ".visual-check." not in artifact.name and artifact.stem not in sources:
                self.error(artifact.relative_to(self.root), "HTML has no diagram source")
        return self.errors


def main():
    """Report all local findings in one pass and exit nonzero on any discrepancy."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[1])
    args = parser.parse_args()
    checker = Checker(args.root)
    errors = checker.run()
    for error in errors:
        print(error)
    print(f"Documentation links: {checker.checked_links} checked; diagram code references: {checker.checked_sources}; findings: {len(errors)}")
    return bool(errors)


if __name__ == "__main__":
    raise SystemExit(main())
