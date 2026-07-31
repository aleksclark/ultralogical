#!/usr/bin/env bash
# Prove the generated-code verification gates actually fail on drift.
#
# For each of Go, TypeScript, and Rust output, this script introduces a
# deliberate mismatch between the checked-in protos and the generated output,
# asserts the corresponding verification command fails, then restores the tree
# and asserts the normal gate passes. It never leaves the tree mutated: the
# restore runs from an EXIT trap.
#
# The generator uses rate-limited remote plugins, so the script invokes it as
# few times as possible: once for the drift mutation and once for the restored
# tree. The other mutations are detectable without regenerating.
#
# Usage: scripts/mutate-codegen-gate.sh
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)

fail() { echo "MUTATION GATE FAILED: $*" >&2; exit 1; }
note() { echo "== $*"; }

# Snapshot every file the mutations touch so restore is exact.
snapshot=$(mktemp -d)
tracked=(
  "proto/ultra/v1/session.proto"
  "gen/go/ultra/v1/session.pb.go"
  "clients/ts/src/gen/ultra/v1/session_pb.ts"
  "clients/rust/build.rs"
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

# verify_go_ts returns 0 when generated output matches the protos and 1 when it
# drifted. It aborts the gate when the generator itself cannot run, because a
# tooling outage is not a drift verdict in either direction.
verify_go_ts() {
  bash scripts/verify-codegen.sh >/dev/null 2>&1
  local code=$?
  if [ "$code" -ge 2 ]; then
    fail "buf generate could not run; rerun the gate when the generator is available"
  fi
  return $code
}

# The rust gate is cheap, so its baseline is checked up front. The Go/TS
# baseline is established by the restored-tree check after mutation 2, which
# keeps the number of rate-limited generator calls to two.
note "baseline: rust codegen covers every proto"
python3 scripts/verify-codegen-rust.py >/dev/null || fail "baseline rust codegen gate is already failing"

# 1. Hand-edited generated output. Generated code is committed, so an edit that
#    regeneration would erase shows up as an unexpected change to a generated
#    path. Detected without spending a generator call.
note "mutation 1: hand-edited generated Go output"
baseline_generated=$(git status --porcelain gen clients/ts/src/gen)
printf '\n// deliberate drift injected by mutate-codegen-gate.sh\n' >>gen/go/ultra/v1/session.pb.go
mutated_generated=$(git status --porcelain gen clients/ts/src/gen)
if [ "$mutated_generated" = "$baseline_generated" ]; then
  fail "a hand edit to committed generated output produced no detectable change"
fi
if ! printf '%s' "$mutated_generated" | grep -q "gen/go/ultra/v1/session.pb.go"; then
  fail "the hand-edited generated file was not reported as changed"
fi
note "  detected as required (hand-edited generated output is visible)"
restore

# 2. Proto changed but generated output not regenerated: the committed output
#    no longer corresponds to the proto, and regeneration reveals it.
note "mutation 2: proto changed without regenerating Go/TS output"
python3 - <<'PY'
import pathlib
p = pathlib.Path("proto/ultra/v1/session.proto")
text = p.read_text()
marker = "message CreateSessionRequest {"
assert marker in text, "session.proto shape changed; update the mutation script"
p.write_text(text.replace(marker, marker + "\n  string drift_probe = 99;", 1))
PY
if verify_go_ts; then
  fail "codegen gate passed with a proto change that was never regenerated"
fi
note "  gate failed as required (Go/TS drift detected)"
restore
verify_go_ts || fail "restored tree does not pass the Go/TS codegen gate"
note "  restored tree passes the Go/TS codegen gate"

# 3. Rust output drift: a proto the Rust build never compiles disappears from
#    the Rust client surface.
note "mutation 3: proto dropped from the Rust build"
python3 - <<'PY'
import pathlib
p = pathlib.Path("clients/rust/build.rs")
text = p.read_text()
target = '        "../../proto/ultra/v1/session.proto",\n'
assert target in text, "build.rs shape changed; update the mutation script"
p.write_text(text.replace(target, "", 1))
PY
if python3 scripts/verify-codegen-rust.py >/dev/null 2>&1; then
  fail "rust codegen gate passed with session.proto dropped from the build"
fi
note "  gate failed as required (Rust drift detected)"
restore
python3 scripts/verify-codegen-rust.py >/dev/null || fail "restored tree does not pass the rust codegen gate"

note "all codegen drift gates failed for the intended reason and the restored tree passes"
