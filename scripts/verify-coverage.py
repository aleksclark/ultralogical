#!/usr/bin/env python3
import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1]
data = json.loads((root / "e2e/coverage.json").read_text())
errors = []
seen = {"web": {}, "rust": {}}

if not data:
    errors.append("coverage matrix is empty")

for capability, evidence in data.items():
    if not isinstance(evidence, dict):
        errors.append(f"{capability}: evidence must be an object")
        continue
    for client in ("web", "rust"):
        item = evidence.get(client)
        if not isinstance(item, dict) or not item.get("file") or not item.get("test"):
            errors.append(f"{capability}: {client} evidence requires file and test")
            continue
        base = root / ("ui/web/e2e" if client == "web" else "ui/desktop/tests")
        path = base / item["file"]
        if not path.is_file():
            errors.append(f"{capability}: missing {path.relative_to(root)}")
            continue
        text = path.read_text()
        name = item["test"]
        # Require a declared test with the named capability evidence, not a
        # filename or arbitrary string in comments.
        patterns = (
            [rf'test\s*\(\s*["\']{re.escape(name)}["\']']
            if client == "web"
            else [rf'async\s+fn\s+{re.escape(name)}\s*\(', rf'fn\s+{re.escape(name)}\s*\(', rf'capability_test!\(\s*{re.escape(name)}\s*\)']
        )
        if not any(re.search(pattern, text) for pattern in patterns):
            errors.append(f"{capability}: test {name!r} not declared in {path.relative_to(root)}")
        seen[client].setdefault((str(path), name), []).append(capability)

# Omnibus evidence can cover related assertions, but mapping most of the
# product to one smoke test is forbidden. Three rows per named scenario is
# the maximum; broader coverage must be split into capability-specific tests.
for client, references in seen.items():
    for (path, name), capabilities in references.items():
        if len(capabilities) > 3:
            errors.append(f"{client}: {path}::{name} claims {len(capabilities)} capabilities; split capability-specific tests")

if errors:
    print("\n".join(errors))
    sys.exit(1)
print(f"{len(data)} capabilities have validated web and Rust test declarations")
