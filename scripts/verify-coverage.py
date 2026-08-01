#!/usr/bin/env python3
"""Validate the cross-client capability coverage matrix.

Coverage claims are merge gates, so this script rejects the ways a claim can be
false rather than merely malformed:

1. The referenced file must exist and declare the named test.
2. Each capability must name at least one behavioral assertion, and that
   assertion text must actually appear in the referenced test body. A row that
   points at a real test which never asserts the capability is rejected.
3. The referenced test must be executed by required CI, traced from the CI
   workflow through the Go functional suite that launches the client runner.
4. Desktop evidence must drive the rendered GPUI application, not tonic or a
   headless state core alone.
5. One named scenario may back at most three capabilities, so an omnibus smoke
   test cannot stand in for the product.
6. The matrix must account for the whole published API surface. Every RPC in
   the protos is either claimed by a capability or explicitly deferred to a
   named future acceptance ID. Silently dropping a row is the same failure as
   fabricating one, so both are rejected here.

Schema:

  {
    "capabilities": {
      "capability": {
        "rpcs": ["ultra.v1.Service/Method"],
        "web":  {"file": "...", "test": "...", "asserts": ["..."]},
        "rust": {"file": "...", "test": "...", "asserts": ["..."]}
      }
    },
    "deferred": {
      "ultra.v1.Service/Method": {"owner": "A9.8", "reason": "..."}
    }
  }
"""

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
seen: dict[str, dict[tuple[str, str], list[str]]] = {"web": {}, "rust": {}}

WEB_DIR = root / "ui/web/e2e"
RUST_DIR = root / "ui/desktop/tests"
PROTO_DIR = root / "proto/ultra/v1"
PLAN_DIR = root / "plan"
CI_WORKFLOW = root / ".github/workflows/ci.yml"

# GPUI application-path markers. Rust evidence must open the shipped window and
# inspect what it rendered; a tonic-only or state-only test is not UI evidence.
GPUI_REQUIRED = ("gpui::test", "open_app(")
GPUI_RENDER_REQUIRED = ("await_rendered(", "debug_bounds(")


def read(path: pathlib.Path) -> str:
    return path.read_text() if path.is_file() else ""


def web_test_body(text: str, name: str) -> str | None:
    """Extract one Playwright test body by name."""
    pattern = re.compile(
        rf'test\s*\(\s*["\']{re.escape(name)}["\']\s*,\s*async\s*\(', re.MULTILINE
    )
    match = pattern.search(text)
    if not match:
        return None
    rest = text[match.end() :]
    # The body ends at the next top-level test declaration, or at end of file.
    nxt = re.search(r"^test\s*\(", rest, re.MULTILINE)
    return rest[: nxt.start()] if nxt else rest


def rust_test_body(text: str, name: str) -> str | None:
    """Extract one Rust test body by function name."""
    pattern = re.compile(rf"(?:async\s+)?fn\s+{re.escape(name)}\s*\(", re.MULTILINE)
    match = pattern.search(text)
    if not match:
        return None
    rest = text[match.end() :]
    nxt = re.search(r"^#\[gpui::test\]|^#\[tokio::test\]|^#\[test\]", rest, re.MULTILINE)
    return rest[: nxt.start()] if nxt else rest


# ---------------------------------------------------------------------------
# CI execution: which client test files does required CI actually run?
# ---------------------------------------------------------------------------

ci_text = read(CI_WORKFLOW)
if not ci_text:
    errors.append("missing .github/workflows/ci.yml; cannot verify CI execution")

# Go test invocations in CI, with the -run filter when present.
ci_go_runs: list[str] = []
for line in ci_text.splitlines():
    stripped = line.strip()
    if "go test" not in stripped or "./e2e/" not in stripped:
        continue
    run = re.search(r"-run\s+'([^']+)'|-run\s+\"([^\"]+)\"|-run\s+(\S+)", stripped)
    ci_go_runs.append(next((g for g in run.groups() if g), "") if run else "")

# A Go functional test "covers" a client file when CI runs that Go test and the
# Go test launches the runner for that file's suite.
e2e_dir = root / "e2e"
go_sources = {path: read(path) for path in sorted(e2e_dir.glob("*_test.go"))}


def ci_runs_go_test(func_name: str) -> bool:
    for pattern in ci_go_runs:
        if pattern == "":
            return True  # unfiltered `go test ./e2e/` runs everything
        try:
            if re.search(pattern, func_name):
                return True
        except re.error:
            if pattern in func_name:
                return True
    return False


def go_tests_launching(client: str, filename: str) -> list[str]:
    """Go functional tests whose execution reaches this client test file."""
    launchers: list[str] = []
    for path, text in go_sources.items():
        for match in re.finditer(r"^func (Test\w+)\(t \*testing\.T\)", text, re.MULTILINE):
            name = match.group(1)
            body_start = match.end()
            nxt = re.search(r"^func ", text[body_start:], re.MULTILINE)
            body = text[body_start : body_start + (nxt.start() if nxt else len(text))]
            if client == "web":
                # Playwright runs every spec in ui/web/e2e, so any Go test that
                # invokes the Playwright runner covers the file.
                if "playwright" in body or "playwright" in text:
                    launchers.append(name)
            else:
                suite = pathlib.Path(filename).stem
                if f'"{suite}"' in body:
                    launchers.append(name)
    return launchers


# ---------------------------------------------------------------------------
# Completeness: every published RPC is claimed or explicitly deferred.
#
# The matrix used to be a whitelist, so a capability could disappear from the
# product's evidence simply by deleting its row. Anchoring it to the protos
# makes a deletion as loud as a fabrication.
# ---------------------------------------------------------------------------

SERVICE_RE = re.compile(r"service\s+(\w+)\s*\{(.*?)\n?\}", re.S)
RPC_RE = re.compile(r"rpc\s+(\w+)\s*\(")
PACKAGE_RE = re.compile(r"^package\s+([\w.]+);", re.M)


def published_rpcs() -> set[str]:
    """Every RPC declared in the checked-in protos."""
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
    """Acceptance IDs the phase plans actually declare."""
    ids: set[str] = set()
    for plan in sorted(PLAN_DIR.glob("*.md")):
        ids.update(re.findall(r"\*\*(A[\d.]+?) —", plan.read_text()))
    return ids


all_rpcs = published_rpcs()
if not all_rpcs:
    errors.append("no RPCs found in proto/ultra/v1; the completeness check cannot run")

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
    for client in ("web", "rust"):
        item = evidence.get(client)
        if not isinstance(item, dict) or not item.get("file") or not item.get("test"):
            errors.append(f"{capability}: {client} evidence requires file and test")
            continue
        asserts = item.get("asserts")
        if not isinstance(asserts, list) or not asserts:
            errors.append(
                f"{capability}: {client} evidence requires a non-empty 'asserts' list "
                "naming the behavior the test proves"
            )
            continue

        base = WEB_DIR if client == "web" else RUST_DIR
        path = base / item["file"]
        if not path.is_file():
            errors.append(f"{capability}: missing {path.relative_to(root)}")
            continue
        text = path.read_text()
        name = item["test"]

        body = web_test_body(text, name) if client == "web" else rust_test_body(text, name)
        if body is None:
            errors.append(f"{capability}: test {name!r} not declared in {path.relative_to(root)}")
            continue

        # The declared assertions must exist in the test body, so a row cannot
        # point at a real test that never checks the capability.
        for needle in asserts:
            if needle not in body:
                errors.append(
                    f"{capability}: {client} test {name!r} does not contain the declared "
                    f"assertion {needle!r}"
                )

        if client == "rust":
            for marker in GPUI_REQUIRED:
                if marker not in text:
                    errors.append(
                        f"{capability}: {path.relative_to(root)} must drive the rendered GPUI "
                        f"application (missing {marker!r}); tonic-only evidence is not accepted"
                    )
            if not any(marker in body for marker in GPUI_RENDER_REQUIRED):
                errors.append(
                    f"{capability}: rust test {name!r} never inspects the rendered GPUI frame; "
                    "reducer-only or direct-RPC evidence is not accepted"
                )

        launchers = go_tests_launching(client, item["file"])
        if not launchers:
            errors.append(
                f"{capability}: no Go functional test launches {path.relative_to(root)}; "
                "the referenced test would never run"
            )
        elif not any(ci_runs_go_test(launcher) for launcher in launchers):
            errors.append(
                f"{capability}: {path.relative_to(root)} is launched only by {launchers}, "
                "which required CI does not run"
            )

        seen[client].setdefault((str(path), name), []).append(capability)

if not data:
    errors.append("coverage matrix is empty")

# Omnibus evidence can cover related assertions, but mapping most of the
# product to one scenario is forbidden.
for client, references in seen.items():
    for (path, name), capabilities in references.items():
        if len(capabilities) > 3:
            errors.append(
                f"{client}: {path}::{name} claims {len(capabilities)} capabilities; "
                "split capability-specific tests"
            )

if errors:
    print("\n".join(errors))
    sys.exit(1)
print(
    f"{len(data)} capabilities have validated, CI-executed web and GPUI evidence; "
    f"{len(claimed)}/{len(all_rpcs)} RPCs covered, {len(deferred)} explicitly deferred"
)
