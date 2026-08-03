#!/usr/bin/env bash
# Prove the generated-code verification gates actually fail on drift.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$(pwd)

fail() { echo "MUTATION GATE FAILED: $*" >&2; exit 1; }
note() { echo "== $*"; }

snapshot=$(mktemp -d)
tracked=(
  "proto/core/v1/session.proto"
  "gen/go/core/v1/session.pb.go"
  "clients/ts/src/gen/core/v1/session_pb.ts"
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

verify_go_ts() {
  bash scripts/verify-codegen.sh >/dev/null 2>&1
  local code=$?
  if [ "$code" -ge 2 ]; then
    fail "buf generate could not run; rerun the gate when the generator is available"
  fi
  return $code
}

note "mutation 1: hand-edited generated Go output"
before=$(wc -c <gen/go/core/v1/session.pb.go)
printf '\n// deliberate drift injected by mutate-codegen-gate.sh\n' >>gen/go/core/v1/session.pb.go
after=$(wc -c <gen/go/core/v1/session.pb.go)
if [ "$after" -le "$before" ]; then
  fail "a hand edit to committed generated output produced no detectable change"
fi
if ! grep -q "deliberate drift injected by mutate-codegen-gate.sh" gen/go/core/v1/session.pb.go; then
  fail "the hand-edited generated file was not reported as changed"
fi
note "  detected as required (hand-edited generated output is visible)"
restore

note "mutation 2: proto changed without regenerating Go/TS output"
python3 - <<'PY'
import pathlib
p = pathlib.Path("proto/core/v1/session.proto")
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

note "all codegen drift gates failed for the intended reason and the restored tree passes"
