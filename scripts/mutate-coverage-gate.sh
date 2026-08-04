#!/usr/bin/env bash
# Prove the coverage-evidence gate rejects false claims (v2 schema).
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)

fail() { echo "COVERAGE MUTATION GATE FAILED: $*" >&2; exit 1; }
note() { echo "== $*"; }

snapshot=$(mktemp -d)
tracked=(
  "e2e/coverage.json"
  ".github/workflows/ci.yml"
  "proto/core/v1/session.proto"
)
for f in "${tracked[@]}"; do
  mkdir -p "$snapshot/$(dirname "$f")"
  cp "$f" "$snapshot/$f"
done

restore() {
  for f in "${tracked[@]}"; do
    cp "$snapshot/$f" "$root/$f"
  done
}
cleanup() { restore; rm -rf "$snapshot"; }
trap cleanup EXIT

verify() { python3 scripts/verify-coverage.py >/dev/null 2>&1; }

note "baseline: the real matrix passes verification"
verify || fail "baseline coverage verification is already failing"

expect_rejected() {
  local reason="$1"
  if verify; then
    fail "coverage verification accepted $reason"
  fi
  note "  rejected as required: $reason"
  restore
  verify || fail "restored tree does not pass coverage verification"
  note "  the restored tree passes"
}

note "mutation 1: nonexistent evidence file"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["capabilities"]["fabricated_capability"] = {
    "rpcs": ["core.v1.SessionService/GetSession"],
    "go_functional": {"file": "does-not-exist_test.go", "test": "TestImaginary", "asserts": ["nothing"]},
    "go_sdk": {"file": "does-not-exist_test.go", "test": "TestImaginary", "asserts": ["nothing"]},
    "ts_sdk": {"file": "missing.test.ts", "test": "nope", "asserts": ["x"]},
}
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a reference to a nonexistent test file"

note "mutation 2: undeclared test name"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["capabilities"]["incremental_streaming"]["go_functional"]["test"] = "TestThatWasNeverWritten"
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a test name the referenced file does not declare"

note "mutation 3: capability assertion absent from the test body"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["capabilities"]["flat_allowlist_denial"]["go_functional"]["asserts"] = ["asserts a behavior this test never checks"]
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "an assertion the referenced test does not contain"

note "mutation 4: evidence not executed by required CI"
python3 - <<'PY'
import pathlib
ci = pathlib.Path(".github/workflows/ci.yml")
text = ci.read_text()
# Force every go test ./e2e invocation to a filter that matches nothing.
text2 = text.replace("go test ./e2e/", "go test ./e2e/ -run 'TestDoesNotMatchAnything' ")
if text2 == text:
    raise SystemExit("could not find go test ./e2e/ in ci.yml")
ci.write_text(text2)
PY
expect_rejected "evidence that required CI never executes"

note "mutation 5: capability deleted from the matrix"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
# Delete a capability that uniquely owns some RPCs so they become unaccounted.
del d["capabilities"]["periodic_prompts"]
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a capability deleted from the matrix, leaving its RPCs unaccounted for"

note "mutation 6: published RPC with neither coverage nor deferral"
python3 - <<'PY'
from pathlib import Path
# Add a throwaway RPC to a proto so the verifier sees an uncovered published RPC.
p = Path("proto/core/v1/session.proto")
text = p.read_text()
if "rpc MutationProbe" not in text:
    text = text.replace(
        "rpc DeleteMemory(DeleteMemoryRequest) returns (DeleteMemoryResponse);",
        "rpc DeleteMemory(DeleteMemoryRequest) returns (DeleteMemoryResponse);\n  rpc MutationProbe(GetSessionRequest) returns (GetSessionResponse);",
    )
    p.write_text(text)
PY
expect_rejected "a published RPC with neither coverage nor an explicit deferral"

note "all coverage mutations rejected as required"
note "the restored tree passes"
