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

Schema per capability:

  "capability": {
    "web":  {"file": "...", "test": "...", "asserts": ["..."]},
    "rust": {"file": "...", "test": "...", "asserts": ["..."]}
  }
"""

import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1]
data = json.loads((root / "e2e/coverage.json").read_text())
errors: list[str] = []
seen: dict[str, dict[tuple[str, str], list[str]]] = {"web": {}, "rust": {}}

WEB_DIR = root / "ui/web/e2e"
RUST_DIR = root / "ui/desktop/tests"
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


for capability, evidence in data.items():
    if not isinstance(evidence, dict):
        errors.append(f"{capability}: evidence must be an object")
        continue
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
print(f"{len(data)} capabilities have validated, CI-executed web and GPUI evidence")
