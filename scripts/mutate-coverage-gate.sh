#!/usr/bin/env bash
# Prove the coverage-evidence gate rejects false claims.
#
# The gate exists to stop a merge whose evidence does not exist, does not run,
# does not assert the capability, or bypasses the shipped UI. This script
# mutates the matrix and the CI configuration in each of those ways, asserts
# verification fails, and restores the tree.
#
# Usage: scripts/mutate-coverage-gate.sh
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)

fail() { echo "COVERAGE MUTATION GATE FAILED: $*" >&2; exit 1; }
note() { echo "== $*"; }

snapshot=$(mktemp -d)
tracked=(
  "e2e/coverage.json"
  ".github/workflows/ci.yml"
  "ui/desktop/tests/desktop_e2e.rs"
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

# 1. A reference to a test file that does not exist.
note "mutation 1: nonexistent evidence file"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["fabricated_capability"] = {
    "web": {"file": "does-not-exist.spec.ts", "test": "imaginary", "asserts": ["nothing"]},
    "rust": {"file": "does-not-exist.rs", "test": "imaginary", "asserts": ["nothing"]},
}
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a reference to a nonexistent test file"

# 2. A real file, but a test name it does not declare.
note "mutation 2: undeclared test name"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["incremental_streaming"]["rust"]["test"] = "test_that_was_never_written"
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "a test name the referenced file does not declare"

# 3. A real, declared test that does not assert the claimed capability.
note "mutation 3: capability assertion absent from the test body"
python3 - <<'PY'
import json, pathlib
p = pathlib.Path("e2e/coverage.json")
d = json.loads(p.read_text())
d["env_usage_metering"]["rust"]["asserts"] = ["asserts a behavior this test never checks"]
p.write_text(json.dumps(d, indent=2) + "\n")
PY
expect_rejected "an assertion the referenced test does not contain"

# 4. Evidence that no required CI job executes.
note "mutation 4: evidence not executed by required CI"
python3 - <<'PY'
import pathlib, re
p = pathlib.Path(".github/workflows/ci.yml")
text = p.read_text()
# Narrow the functional job so it no longer runs the desktop suites.
mutated = re.sub(
    r"go test \./e2e/ -count=1 -v -timeout \d+m",
    "go test ./e2e/ -count=1 -run 'TestA01' -v -timeout 20m",
    text,
)
assert mutated != text, "ci.yml shape changed; update the mutation script"
p.write_text(mutated)
PY
expect_rejected "evidence that required CI never executes"

# 5. Desktop evidence that bypasses the rendered GPUI application.
note "mutation 5: desktop evidence that bypasses GPUI rendering"
python3 - <<'PY'
import pathlib
p = pathlib.Path("ui/desktop/tests/desktop_e2e.rs")
text = p.read_text()
# Replace the rendered-frame assertions in one test with a direct-state check,
# the classic "reducer-only evidence" substitution.
target = 'await_rendered(cx, "row:you: presence check", FRAME_ATTEMPTS);'
assert target in text, "desktop_e2e.rs shape changed; update the mutation script"
p.write_text(text.replace(target, "assert!(!participants.is_empty());"))
PY
expect_rejected "desktop evidence that never inspects a rendered GPUI frame"

note "every false coverage claim was rejected and the restored tree passes"
