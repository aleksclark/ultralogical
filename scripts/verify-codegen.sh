#!/usr/bin/env bash
# Verify committed generated code matches the checked-in protos.
#
# The check regenerates into a snapshot comparison rather than diffing against
# git's index, so it is correct both in CI (where the working tree is clean) and
# in a working tree with uncommitted generated changes. Either way the question
# is the same: does generate reproduce exactly what is on disk?
#
# Exit codes:
#   0  generated output matches the protos
#   1  generated output drifted from the protos
#   2  the generator itself could not run (for example remote-plugin rate
#      limiting); the caller must not read this as a drift verdict
set -uo pipefail

cd "$(dirname "$0")/.."

targets=(gen clients/ts/src/gen clients/admin-ts/src/gen)
snapshot=$(mktemp -d)
trap 'rm -rf "$snapshot"' EXIT

for target in "${targets[@]}"; do
  mkdir -p "$snapshot/$(dirname "$target")"
  if [ -e "$target" ]; then
    cp -r "$target" "$snapshot/$target"
  else
    mkdir -p "$snapshot/$target"
  fi
done

restore_snapshot() {
  for target in "${targets[@]}"; do
    rm -rf "$target"
    if [ -e "$snapshot/$target" ]; then
      cp -r "$snapshot/$target" "$target"
    fi
  done
}

# buf's remote plugins are rate limited; a throttled run is a tooling failure,
# not evidence about drift, so wait it out before giving up with code 2.
# CORE_CODEGEN_RETRIES tunes patience for constrained environments.
generate_output=""
generated=0
retries=${CORE_CODEGEN_RETRIES:-8}
for attempt in $(seq 1 "$retries"); do
  generate_output=$(bash scripts/generate.sh 2>&1)
  if [ $? -eq 0 ]; then
    generated=1
    break
  fi
  if ! printf '%s' "$generate_output" | grep -qi "resource_exhausted\|rate limit\|too many requests"; then
    break
  fi
  echo "buf generate throttled (attempt $attempt/$retries); waiting" >&2
  sleep $((attempt * 30))
done

if [ "$generated" != 1 ]; then
  printf '%s\n' "$generate_output" >&2
  echo "buf generate could not run; codegen drift is undetermined" >&2
  restore_snapshot
  exit 2
fi

status=0
for target in "${targets[@]}"; do
  if ! diff -ru "$snapshot/$target" "$target"; then
    echo "generated output in $target does not match the protos; run 'task generate' and commit the result" >&2
    status=1
  fi
done

# Hard isolation: admin must never appear under the public TS client tree.
if [ -d clients/ts/src/gen/admin ]; then
  echo "admin symbols leaked into clients/ts/src/gen/admin" >&2
  status=1
fi

exit $status
