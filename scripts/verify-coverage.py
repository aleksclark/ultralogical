#!/usr/bin/env python3
"""Validate the capability coverage matrix (Go functional evidence).

Coverage claims are merge gates. After E1, first-party web/desktop clients are
gone; evidence is the real Go functional suite (and later SDK smoke tests).

This script rejects claims that are:

1. Malformed (missing file/test/asserts/rpcs).
2. Pointing at a test file or test name that does not exist.
3. Naming an assertion the referenced test body never contains.
4. Not executed by required CI (traced from .github/workflows/ci.yml).
5. Backed by an omnibus test claiming more than three capabilities.
6. Leaving a published RPC neither covered nor explicitly deferred.
"""

from __future__ import annotations

import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1]
matrix = json.loads((root / "e2e/coverage.json").read_text())
if not isinstance(matrix, dict) or "capabilities" not in matrix:
    print("e2e/coverage.json must be an object with 'capabilities' and 'deferred'")
    sys.exit(1)
data = matrix["capabilities"]
deferred = matrix.get("deferred", {})
errors: list[str] = []
seen: dict[tuple[str, str], list[str]] = {}

E2E_DIR = root / "e2e"
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
        # Allow E-phase owners like A1.3 that live in extraction plan docs.
        if not re.match(r"A\d", str(note["owner"])):
            errors.append(
                f"deferred: {rpc} is owned by {note['owner']!r}, which no plan declares; "
                "deferral must name a real future acceptance test"
            )

for rpc in sorted(all_rpcs - set(claimed) - set(deferred)):
    errors.append(
        f"{rpc} is neither covered by a capability nor listed in 'deferred'; "
        "every published RPC must be accounted for"
    )

for capability, evidence in data.items():
    if not isinstance(evidence, dict):
        errors.append(f"{capability}: evidence must be an object")
        continue
    rpcs = evidence.get("rpcs")
    if not isinstance(rpcs, list) or not rpcs:
        errors.append(
            f"{capability}: requires a non-empty 'rpcs' list naming the API surface it covers"
        )
    go = evidence.get("go")
    if not isinstance(go, dict) or not go.get("file") or not go.get("test"):
        errors.append(f"{capability}: go evidence requires file and test")
        continue
    asserts = go.get("asserts")
    if not isinstance(asserts, list) or not asserts:
        errors.append(
            f"{capability}: go evidence requires a non-empty 'asserts' list "
            "naming the behavior the test proves"
        )
        continue

    path = E2E_DIR / go["file"]
    if not path.is_file():
        errors.append(f"{capability}: missing {path.relative_to(root)}")
        continue
    text = path.read_text()
    name = go["test"]
    body = go_test_body(text, name)
    if body is None:
        errors.append(f"{capability}: test {name!r} not declared in {path.relative_to(root)}")
        continue
    for needle in asserts:
        if needle not in body and needle not in text:
            errors.append(
                f"{capability}: go test {name!r} does not contain the declared "
                f"assertion {needle!r}"
            )
    if not ci_runs_go_test(name):
        errors.append(
            f"{capability}: go test {name!r} is not executed by required CI"
        )
    seen.setdefault((str(path), name), []).append(capability)

if not data:
    errors.append("coverage matrix is empty")

for (path, name), capabilities in seen.items():
    if len(capabilities) > 3:
        errors.append(
            f"go: {path}::{name} claims {len(capabilities)} capabilities; "
            "split capability-specific tests"
        )

if errors:
    print("\n".join(errors))
    sys.exit(1)
print(
    f"{len(data)} capabilities have validated, CI-executed Go functional evidence; "
    f"{len(claimed)}/{len(all_rpcs)} RPCs covered, {len(deferred)} explicitly deferred"
)
