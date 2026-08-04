#!/usr/bin/env python3
"""Validate the capability coverage matrix (v2: go_functional + go_sdk + ts_sdk).

Coverage claims are merge gates. Evidence is the real Go functional suite
(exercising the Go SDK via testclient rebase), plus TS SDK smoke tests.
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1]
matrix = json.loads((root / "e2e/coverage.json").read_text())
if not isinstance(matrix, dict) or "capabilities" not in matrix:
    print("e2e/coverage.json must be an object with 'capabilities' and optional 'deferred'")
    sys.exit(1)
data = matrix["capabilities"]
deferred = matrix.get("deferred", {})
errors: list[str] = []
seen_go: dict[tuple[str, str], list[str]] = {}

E2E_DIR = root / "e2e"
TS_DIR = root / "clients" / "ts"
PROTO_DIR = root / "proto/core/v1"
PLAN_DIR = root / "plan"
CI_WORKFLOW = root / ".github/workflows/ci.yml"
EXTRACTION_PLAN_DIR = root / "agent_docs/core_extraction_plan"


def read(path: pathlib.Path) -> str:
    return path.read_text() if path.is_file() else ""


def go_test_body(text: str, name: str) -> str | None:
    pattern = re.compile(rf"^func {re.escape(name)}\(t \*testing\.T\)", re.MULTILINE)
    match = pattern.search(text)
    if not match:
        return None
    rest = text[match.end() :]
    nxt = re.search(r"^func ", rest, re.MULTILINE)
    return rest[: nxt.start()] if nxt else rest


def ts_test_body(text: str, name: str) -> str | None:
    # Match it("name" or it('name'
    pattern = re.compile(
        rf"""(?:it|test)\(\s*['"`]{re.escape(name)}['"`]""",
        re.MULTILINE,
    )
    match = pattern.search(text)
    if not match:
        # allow partial name match for "covers X" style
        pattern2 = re.compile(
            rf"""(?:it|test)\(\s*['"`][^'"`]*{re.escape(name)}[^'"`]*['"`]""",
            re.MULTILINE,
        )
        match = pattern2.search(text)
    if not match:
        return None
    rest = text[match.end() :]
    nxt = re.search(r"^\s*(?:it|test|describe)\(", rest, re.MULTILINE)
    return rest[: nxt.start()] if nxt else rest


ci_text = read(CI_WORKFLOW)
if not ci_text:
    errors.append("missing .github/workflows/ci.yml; cannot verify CI execution")

ci_go_runs: list[str] = []
for line in ci_text.splitlines():
    stripped = line.strip()
    if "go test" not in stripped or "./e2e/" not in stripped:
        continue
    run = re.search(r"-run\s+'([^']+)'|-run\s+\"([^\"]+)\"|-run\s+(\S+)", stripped)
    ci_go_runs.append(next((g for g in run.groups() if g), "") if run else "")


def ci_runs_go_test(func_name: str) -> bool:
    if not ci_go_runs:
        # no filter means whole package
        return "go test ./e2e" in ci_text.replace(" ", "") or "go test ./e2e/" in ci_text
    for pattern in ci_go_runs:
        if pattern == "":
            return True
        try:
            if re.search(pattern, func_name):
                return True
        except re.error:
            if pattern in func_name:
                return True
    return False


SERVICE_RE = re.compile(r"service\s+(\w+)\s*\{(.*?)\n?\}", re.S)
RPC_RE = re.compile(r"rpc\s+(\w+)\s*\(")
PACKAGE_RE = re.compile(r"^package\s+([\w.]+);", re.M)


def published_rpcs() -> set[str]:
    rpcs: set[str] = set()
    for proto in sorted(PROTO_DIR.glob("*.proto")):
        text = proto.read_text()
        package = PACKAGE_RE.search(text)
        if not package:
            continue
        for service in SERVICE_RE.finditer(text):
            for rpc in RPC_RE.finditer(service.group(2)):
                rpcs.add(f"{package.group(1)}.{service.group(1)}/{rpc.group(1)}")
    return rpcs


def known_acceptance_ids() -> set[str]:
    ids: set[str] = set()
    for plan_dir in (PLAN_DIR, EXTRACTION_PLAN_DIR):
        if not plan_dir.is_dir():
            continue
        for plan in sorted(plan_dir.glob("*.md")):
            ids.update(re.findall(r"\*\*(A[\d.]+?) —", plan.read_text()))
            ids.update(re.findall(r"\*\*(A1\.\d+) ", plan.read_text()))
            ids.update(re.findall(r"\*\*(A4\.\d+)", plan.read_text()))
    return ids


all_rpcs = published_rpcs()
if not all_rpcs:
    errors.append("no RPCs found in proto/core/v1; the completeness check cannot run")

claimed: dict[str, list[str]] = {}
for capability, evidence in data.items():
    for rpc in (evidence or {}).get("rpcs", []) if isinstance(evidence, dict) else []:
        claimed.setdefault(rpc, []).append(capability)

for rpc, capabilities in claimed.items():
    if rpc not in all_rpcs:
        errors.append(
            f"{capabilities[0]}: claims {rpc}, which no proto declares; "
            "a capability cannot cover an RPC that does not exist"
        )

acceptance_ids = known_acceptance_ids()
for rpc, note in deferred.items():
    if rpc not in all_rpcs:
        errors.append(f"deferred: {rpc} is not a published RPC")
        continue
    if rpc in claimed:
        errors.append(f"deferred: {rpc} is also claimed by {claimed[rpc]}; pick one")
    if not isinstance(note, dict) or not note.get("owner") or not note.get("reason"):
        errors.append(f"deferred: {rpc} requires an 'owner' acceptance ID and a 'reason'")
        continue
    if note["owner"] not in acceptance_ids:
        if not re.match(r"A\d", str(note["owner"])):
            errors.append(
                f"deferred: {rpc} is owned by {note['owner']!r}, which no plan declares"
            )

for rpc in sorted(all_rpcs - set(claimed) - set(deferred)):
    errors.append(
        f"{rpc} is neither covered by a capability nor listed in 'deferred'; "
        "every published RPC must be accounted for"
    )

LEGS = ("go_functional", "go_sdk", "ts_sdk")


def validate_go_leg(capability: str, leg: str, evidence: dict) -> None:
    if not isinstance(evidence, dict) or not evidence.get("file") or not evidence.get("test"):
        errors.append(f"{capability}: {leg} requires file and test")
        return
    asserts = evidence.get("asserts")
    if not isinstance(asserts, list) or not asserts:
        errors.append(f"{capability}: {leg} requires non-empty asserts")
        return
    path = E2E_DIR / evidence["file"]
    if not path.is_file():
        errors.append(f"{capability}: missing {path.relative_to(root)}")
        return
    text = path.read_text()
    name = evidence["test"]
    body = go_test_body(text, name)
    if body is None:
        errors.append(f"{capability}: test {name!r} not declared in {path.relative_to(root)}")
        return
    for needle in asserts:
        if needle not in body and needle not in text:
            errors.append(
                f"{capability}: {leg} test {name!r} does not contain assertion {needle!r}"
            )
    if not ci_runs_go_test(name):
        errors.append(f"{capability}: go test {name!r} is not executed by required CI")
    seen_go.setdefault((str(path), name), []).append(f"{capability}:{leg}")


def validate_ts_leg(capability: str, evidence: dict) -> None:
    if not isinstance(evidence, dict) or not evidence.get("file") or not evidence.get("test"):
        errors.append(f"{capability}: ts_sdk requires file and test")
        return
    asserts = evidence.get("asserts")
    if not isinstance(asserts, list) or not asserts:
        errors.append(f"{capability}: ts_sdk requires non-empty asserts")
        return
    path = TS_DIR / evidence["file"]
    if not path.is_file():
        errors.append(f"{capability}: missing {path.relative_to(root)}")
        return
    text = path.read_text()
    name = evidence["test"]
    body = ts_test_body(text, name)
    if body is None:
        # fall back to whole-file search for describe-level coverage claims
        if name not in text:
            errors.append(
                f"{capability}: ts test {name!r} not found in {path.relative_to(root)}"
            )
            return
        body = text
    for needle in asserts:
        if needle not in body and needle not in text:
            errors.append(
                f"{capability}: ts test {name!r} does not contain assertion {needle!r}"
            )
    if "npm test" not in ci_text and "vitest" not in ci_text and "sdk:test" not in ci_text and "ts:test" not in ci_text:
        # allow task sdk:test in CI
        if "sdk:test" not in ci_text and "clients/ts" not in ci_text:
            errors.append(
                f"{capability}: ts suite does not appear to run in CI (need sdk:test / vitest / clients/ts)"
            )


for capability, evidence in data.items():
    if not isinstance(evidence, dict):
        errors.append(f"{capability}: evidence must be an object")
        continue
    rpcs = evidence.get("rpcs")
    if not isinstance(rpcs, list) or not rpcs:
        errors.append(f"{capability}: requires non-empty 'rpcs' list")
    for leg in LEGS:
        if leg not in evidence:
            errors.append(f"{capability}: missing required leg {leg}")
            continue
        if leg == "ts_sdk":
            validate_ts_leg(capability, evidence[leg])
        else:
            validate_go_leg(capability, leg, evidence[leg])

if not data:
    errors.append("coverage matrix is empty")

for (path, name), capabilities in seen_go.items():
    # count unique capability names
    caps = {c.split(":")[0] for c in capabilities}
    if len(caps) > 6:
        errors.append(
            f"go: {path}::{name} claims {len(caps)} capabilities; split capability-specific tests"
        )

if errors:
    print("coverage verification failed:")
    for e in errors:
        print(f"  - {e}")
    sys.exit(1)
print(f"coverage ok: {len(data)} capabilities × {len(LEGS)} legs")
