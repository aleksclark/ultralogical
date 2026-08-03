#!/usr/bin/env bash
# Prove the coverage-evidence gate rejects false claims.
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
}

note "mutation 1: nonexistent evidence file"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["capabilities"]["fabricated_capability"] = {
    "rpcs": ["core.v1.SessionService/GetSession"],
    "go": {"file": "does-not-exist_test.go", "test": "TestImaginary", "asserts": ["nothing"]},
}
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a reference to a nonexistent test file"

note "mutation 2: undeclared test name"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["capabilities"]["incremental_streaming"]["go"]["test"] = "TestThatWasNeverWritten"
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a test name the referenced file does not declare"

note "mutation 3: capability assertion absent from the test body"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["capabilities"]["flat_allowlist_denial"]["go"]["asserts"] = ["asserts a behavior this test never checks"]
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "an assertion the referenced test does not contain"

note "mutation 4: evidence not executed by required CI"
python3 - <<'PY'
import pathlib, re
p = pathlib.Path(".github/workflows/ci.yml")
text = p.read_text()
mutated = re.sub(
    r"go test \./e2e/ -count=1 -v -timeout \d+m",
    "go test ./e2e/ -count=1 -run 'TestA01' -v -timeout 20m",
    text,
)
assert mutated != text, "ci.yml shape changed; update the mutation script"
p.write_text(mutated)
PY
expect_rejected "evidence that required CI never executes"

note "mutation 5: a covered capability deleted from the matrix"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
del d["capabilities"]["session_memory"]
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a capability deleted from the matrix, leaving its RPCs unaccounted for"

note "mutation 6: a new RPC with neither coverage nor a deferral"
python3 - <<'PY'
import pathlib
p = pathlib.Path("proto/core/v1/session.proto")
text = p.read_text()
target = "  rpc DeleteMemory(DeleteMemoryRequest) returns (DeleteMemoryResponse);"
assert target in text, "session.proto shape changed; update the mutation script"
p.write_text(text.replace(target, target + "\n  rpc PurgeSession(GetSessionRequest) returns (GetSessionResponse);"))
PY
expect_rejected "a published RPC with neither coverage nor an explicit deferral"

note "every false coverage claim was rejected and the restored tree passes"
