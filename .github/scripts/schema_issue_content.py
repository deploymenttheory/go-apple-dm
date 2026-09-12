"""Deterministic engineering briefs from schema evidence and inspected code.

These rules describe concrete checks, not inferred runtime failures. They do not
change incident identity or implement any of the compatibility work they request.
"""
import difflib
import re


def project_context(root):
    def read(name):
        path = root / name
        return path.read_text() if path.is_file() else ""

    types = read("devicemanagement/schema/checkin/types.gen.go")
    response = re.search(r"type ReturnToServiceResponseReturnToService struct \{(.*?)\n\}", types, re.S)
    return {
        "returnToServiceResponse": response is not None,
        "retryFieldPresent": bool(response and "ShouldRetryEnrollment" in response[1]),
        "returnToServiceHandler": "type ReturnToServiceHandler" in read("server/service/service.go"),
        "getTokenHandler": "type GetTokenHandler" in read("server/service/service.go"),
    }


def ref(path, label, source="project"):
    return {"path": path, "label": label, "source": source}


def brief(title, summary, impact, work, done, references=(), candidate=False, classification="Compatibility verification"):
    return {"title": title, "summary": summary, "impact": impact, "requiredWork": work,
            "completionCriteria": done, "references": list(references), "needsCandidate": candidate,
            "classification": classification}


def changed_fragments(before, after):
    """Show the change, including additions near the end of long paragraphs."""
    a, b = before.split(), after.split()
    chunks = []
    for group in difflib.SequenceMatcher(None, a, b, autojunk=False).get_grouped_opcodes(n=6):
        old = " ".join(a[group[0][1]:group[-1][2]]) if a else "<absent>"
        new = " ".join(b[group[0][3]:group[-1][4]]) if b else "<absent>"
        chunks.append((old, new))
    return chunks or [(before, after)]


def evidence_values(evidence):
    if "before" in evidence and "after" in evidence:
        return evidence["before"], evidence["after"]
    detail = evidence["detail"]
    return tuple(detail.split(" → ", 1)) if " → " in detail else ("", detail)


def availability_groups(evidence):
    groups = {name: set() for name in (
        "Removal boundaries", "Deprecation boundaries", "Introductions and availability",
        "Enrollment and channel restrictions", "Removed metadata or fields")}
    for item in evidence:
        field, _, _ = item["detail"].partition(": ")
        _, after = evidence_values(item)
        if after == "<absent>":
            category = "Removed metadata or fields"
        elif ".removed" in field:
            category = "Removal boundaries"
        elif ".deprecated" in field:
            category = "Deprecation boundaries"
        elif ".introduced" in field:
            category = "Introductions and availability"
        else:
            category = "Enrollment and channel restrictions"
        groups[category].add((item["path"], field.split(".supportedOS", 1)[0]))
    return [{"category": name, "objects": len(rows), "files": sorted({p for p, _ in rows})}
            for name, rows in groups.items() if rows]


def enrich_findings(result):
    for item in result["findings"]:
        item["brief"] = finding_brief(item, result.get("projectContext", {}))
        item["title"] = item["brief"]["title"]
        item["action"] = item["brief"]["requiredWork"][0]


def finding_brief(item, context):
    key, evidence = item["key"], item["evidence"]
    files = sorted({e["path"].split("#", 1)[0] for e in evidence})
    if key == "schema-format:field:schemagen.Schema:examples":
        return brief("Support Apple's examples metadata in strict schema parsing",
            "Apple added top-level `examples` metadata. The strict YAML decoder rejects it in " + str(len(files)) + " schema files.",
            "The unmodified candidate cannot be generated. This blocks its generated API, build and runtime compatibility checks.",
            ["Compare the examples structure with Apple's meta-schema and add explicit typed parser support; keep unknown-field rejection enabled.",
             "Cover empty/populated examples, file references and unsupported metadata with fixtures. Keep examples out of generated protocol fields.",
             "Rerun strict parsing against the recorded Apple commit and retain the result. Address other parser findings in their own tickets."],
            ["Every recorded `examples` occurrence parses, and unknown unrelated keys still fail.",
             "Example-reference checks still run; stable generated output is unchanged. This issue closes automatically when the parse stage passes."],
            [ref("internal/schemagen/model.go", "Schema metadata types and strict parser"), ref("internal/schemagen/audit_test.go", "Audit regression tests"), ref("docs/schema.yaml", "Apple meta-schema", "apple")],
            classification="Confirmed parsing failure")
    if key == "schema-format:field:schemagen.ReasonDetail:valuetype":
        return brief("Resolve unsupported ReasonDetail.valuetype timestamp metadata",
            "The strict decoder rejects `valuetype` on reason-detail fields in " + str(len(files)) + " schema files. The existing `type` field remains present.",
            "This is a generator input failure. It prevents generation of the unmodified candidate; it does not establish a server runtime fault.",
            ["Inspect the affected reason details and Apple's meta-schema to establish the annotation's contract, including `timestamp` alongside `type: <string>`.",
             "Add deliberate typed metadata support and fixtures, or document an upstream inconsistency and its resolution. Preserve strict decoding and the existing type semantics.",
             "Rerun parsing against the recorded commit and retain evidence for each affected file."],
            ["The recorded reason details parse with their string type intact, and unknown metadata remains rejected.",
             "The annotation's handling is documented and tested. This issue closes automatically when the parse stage passes."],
            [ref("internal/schemagen/model.go", "ReasonDetail metadata type and strict parser"), ref("internal/schemagen/emit_reasons.go", "Reason vocabulary generation"),
             ref("docs/schema.yaml", "Apple meta-schema", "apple")], classification="Confirmed parsing failure")
    if key == "behavior-review:availability":
        groups = availability_groups(evidence)
        value = brief("Verify changed OS availability and enrollment restrictions",
            "Apple changed support metadata in " + str(len(files)) + " schema files. The groups below distinguish removal/deprecation boundaries from new availability and enrollment restrictions.",
            "The project derives support checks from this metadata. Compatibility checks must establish that newly unsupported targets are rejected and older supported targets retain access. No incorrect dispatch has been demonstrated.",
            ["Start with removal and deprecation boundaries, including the affected software-update commands and profile keys listed below. Distinguish removed fields from removed restrictions.",
             "On the generated candidate, verify support at the previous supported version and the changed boundary, plus device/user channel, supervision and enrollment restrictions.",
             "Exercise `Core.Enqueue` target validation for the affected commands with validation enabled; inspect each remaining availability group and record its result."],
            ["Support tests cover changed boundaries and contexts, with expected allowed/rejected results.",
             "Command dispatch respects the updated metadata and retains supported older targets. Each group has a linked test or documented support decision."],
            [ref("server/service/service.go", "Core.Enqueue target validation"), ref("server/service/service_test.go", "Service tests"),
             ref("devicemanagement/schema/support/support.go", "Support evaluation"), ref("internal/schemagen/audit_support.go", "Source-derived support probes")], candidate=True)
        value["groups"] = groups
        return value
    if key == "behavior-review:new-commands":
        enhanced = any("enhanced.log.collection" in p for p in files)
        names = ", ".join("`" + e["detail"].rsplit(":", 1)[-1] + "`" for e in evidence)
        value = brief("Verify enhanced-log collection commands and status handling" if enhanced else "Verify delivery and result handling for new Apple commands",
            "Apple added " + names + "." + (" The trigger command requires an AppleCare token; the device reports progress through declarative status." if enhanced else ""),
            "New schema entries require candidate verification through the existing command and status paths. An additional server upload endpoint is not established by this finding.",
            ["Generate the candidate and test command encoding, queue delivery and acknowledgement/error handling for each listed command.",
             "Verify required AppleCare-token validation and platform/channel restrictions, including macOS user-channel delivery." if enhanced else "Verify each command's required fields and platform/channel restrictions.",
             "Test ingestion of `enhanced-logging.status`, `enhanced-logging.applecare-token` and `enhanced-logging.timestamp`, including completion, failure, cancellation and declined consent." if enhanced else "Trace any additional server obligations in the linked command descriptions and document the handling."],
            ["Each new command has delivery/response tests and rejects invalid required input and unsupported targets.",
             "Associated declarative status is retained correctly; record any intentionally unsupported behavior and its user-visible effect."],
            [ref("server/service/service.go", "Command enqueue and target checks"), ref("server/service/connect.go", "Command delivery and responses"),
             ref("server/ddmadapter/inproc/inproc.go", "DDM adapter"), ref("server/ddmadapter/inproc/inproc_test.go", "DDM adapter tests")], candidate=True)
        if enhanced:
            value["references"].extend(ref("declarative/status/enhanced-logging." + name + ".yaml", "Apple enhanced-logging." + name, "apple")
                                        for name in ("status", "applecare-token", "timestamp"))
        return value
    if key == "behavior-review:protocol-wording":
        rs256 = any("gettoken.yaml" in e["path"] and "RS256" in evidence_values(e)[1] for e in evidence)
        if rs256:
            impact = ("The service delegates token creation to `GetTokenHandler`; the monitor has not demonstrated an incorrect signing implementation. The extension-point contract and its verification need to account for this requirement."
                      if context.get("getTokenHandler") else "The signing requirement needs a code-path assessment. The monitor has not identified or tested the project's token issuer.")
            return brief("Verify RS256 signing for Managed Apple Account GetToken responses",
                "Apple now explicitly requires `RS256` for the JWT returned for `TokenServiceType: com.apple.maid`. The changed passage is shown below; purely editorial changes are excluded.",
                impact,
                ["Trace `com.apple.maid` token creation from `GetTokenHandler` and document the requirement on that extension point.",
                 "For any in-repository issuer, verify its JWT header and RSA/SHA-256 signature against the signing certificate. If issuance remains caller-supplied, document that boundary and add a valid signed-token contract fixture.",
                 "Add regression coverage for the signing contract and response transport; record any caller-side work that this repository cannot verify."],
                ["The handler contract explicitly requires RS256 for `com.apple.maid`, with a linked signed-token test or example.",
                 "The issue records who supplies the token and what was verified. A passing unrelated JWT or ACME test is not evidence for this token service."],
                [ref("server/service/service.go", "GetTokenHandler contract"), ref("server/service/checkin.go", "GetToken response path"),
                 ref("server/service/service_test.go", "GetToken handler tests")])
        return brief("Verify changed Apple protocol requirements",
            "Apple changed the protocol descriptions shown below after known editorial edits were filtered.",
            "These source changes require an assessment; no runtime incompatibility has been established.",
            ["Read each changed clause and trace the corresponding request/response path in the project.",
             "For each changed obligation, add a focused test or document why existing behavior satisfies it."],
            ["Every retained clause has a linked implementation/test or a documented support decision."],
            [ref("server/service/checkin.go", "Check-in dispatch and responses")])
    if key == "behavior-review:protocol:mdm:requesttype:ReturnToService" and any("ShouldRetryEnrollment" in e["path"] for e in evidence):
        def field(suffix, default):
            return next((evidence_values(e)[1] for e in evidence if e["path"].endswith("ShouldRetryEnrollment]." + suffix)), default)

        version, default = field("supportedOS.iOS.introduced", "the recorded version"), field("default", "the Apple default")
        summary = ("Apple added optional boolean `ShouldRetryEnrollment` to the ReturnToService response, available from iOS " + version +
                   " with default `" + default + "`. When true, the device retries enrollment after erasure if the first attempt fails.")
        impact = "The field's behavior through the server response path has not been verified on the unmodified candidate."
        if context.get("returnToServiceResponse"):
            impact = ("The assessed project's typed response already exposes this field." if context.get("retryFieldPresent") else
                      "The assessed project's typed response does not expose this option, so its typed handler cannot opt into the new behavior.")
        if context.get("returnToServiceHandler"):
            impact += " The existing ReturnToService handler supplies the response; no regression in current Return to Service behavior has been demonstrated."
        value = brief("Verify enrollment retry in iOS " + version + " ReturnToService responses", summary, impact,
            ["After the parser blockers clear, generate the seed preview and confirm the response type exposes `ShouldRetryEnrollment`; keep generated files generator-owned.",
             "Add service tests proving a handler's true, false and omitted values reach the plist response correctly.",
             "Verify generated availability for the recorded iOS boundary and unsupported platforms; rerun the bootstrap-token preservation tests and document the handler option."],
            ["The generated field and service serialization tests cover true, false and omission, retaining the documented default.",
             "Availability and existing bootstrap-token tests pass. The handler documentation explains how deployment policy selects the option."],
            [ref("devicemanagement/schema/checkin/types.gen.go", "Current typed ReturnToService response"),
             ref("server/service/service.go", "ReturnToServiceHandler contract"), ref("server/service/checkin.go", "ReturnToService response handling"),
             ref("server/service/returntoservice_test.go", "Existing ReturnToService tests")], candidate=True)
        value["changes"] = [{"subject": "ReturnToService.ShouldRetryEnrollment", "before": "Not declared in the baseline schema",
                            "after": "Optional boolean; default " + default + "; iOS " + version + "; visionOS unavailable"}]
        return value
    if key.startswith("upstream-input:area:"):
        metrics = "openapi/content-cache/metrics_report.json" in files
        return brief("Decide support for Apple's content-cache metrics schema" if metrics else "Decide support for the new Apple " + key.rsplit(":", 1)[-1] + " input area",
            "Apple added `openapi/content-cache/metrics_report.json`, outside the current YAML generation inputs." if metrics else "Apple added the input files listed below outside the monitor's known input areas.",
            "The scanner has identified an input outside its current coverage. It has not established that this repository must implement a receiver or that the server is malfunctioning.",
            ["Read the linked schema and identify its producer, intended consumer and protocol purpose.",
             "Decide whether this project's library or reference server should consume it. Record inclusion or exclusion and the rationale.",
             "If included, link bounded implementation work with contract tests and documentation; if excluded, document the support boundary."],
            ["The issue records an explicit support decision and rationale, with follow-up work linked for any accepted implementation."],
            [ref("internal/schemagen/model.go", "Current schema input coverage"), ref("docs/architecture.md", "Library and server responsibilities")],
            classification="Support decision")
    failure = item["kind"] == "failure"
    return brief(item["title"], "The " + item["stage"] + " stage reported the evidence below." if failure else "Apple changed the schema objects listed below.",
        "This check failed for the recorded snapshot; investigate the specific evidence before attributing a runtime regression." if failure else "A compatibility assessment is required; a runtime failure has not been established.",
        [item["action"], "Record the result against the linked source and project commits."],
        ["The relevant check passes on a later completed assessment." if failure else "Each change has a linked test or a documented support decision."],
        classification="Confirmed check failure" if failure else "Compatibility verification")
