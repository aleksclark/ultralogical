#!/usr/bin/env bash
# Lifecycle contract tests for scripts/compose-dev.sh (no long-running stack).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${ROOT}/scripts/compose-dev.sh"
COMPOSE="${ROOT}/docker-compose.stacklane.yml"

fail() { echo "FAIL: $*" >&2; exit 1; }
ok() { echo "ok: $*" >&2; }

[[ -x "$SCRIPT" ]] || fail "compose-dev.sh must be executable"
[[ -f "$COMPOSE" ]] || fail "missing docker-compose.stacklane.yml"
[[ -f "${ROOT}/Dockerfile.dev" ]] || fail "missing Dockerfile.dev"
[[ -f "${ROOT}/.air.cored.toml" ]] || fail "missing .air.cored.toml"
[[ -f "${ROOT}/.air.coreworker.toml" ]] || fail "missing .air.coreworker.toml"
[[ -f "${ROOT}/docker-compose.yml" ]] || fail "legacy docker-compose.yml must remain"

help_out="$("$SCRIPT" --help)"
for verb in check up status endpoints logs down destroy; do
  printf '%s\n' "$help_out" | grep -q "$verb" || fail "help missing $verb"
done
ok "help verbs"

if STACKLANE_BASE_DOMAIN=example.local "$SCRIPT" endpoints >/dev/null 2>&1; then
  fail "Stacklane lifecycle must reject .local base domains"
fi
ok "lifecycle rejects .local base domain"

if "$SCRIPT" destroy >/tmp/ultracore-compose-destroy.err 2>&1; then
  fail "destroy without CONFIRM must fail"
fi
grep -q 'refusing destroy' /tmp/ultracore-compose-destroy.err || fail "destroy refusal message"
ok "destroy refused without CONFIRM"

if CONFIRM=wrong-destroy "$SCRIPT" destroy >/tmp/ultracore-compose-destroy2.err 2>&1; then
  fail "destroy with wrong CONFIRM must fail"
fi
grep -q 'refusing destroy' /tmp/ultracore-compose-destroy2.err || fail "wrong CONFIRM must refuse"
ok "destroy refused with wrong CONFIRM"

if awk '/^cmd_down\(\)/,/^}/ {print}' "$SCRIPT" | grep -vE '^[[:space:]]*#' | grep -qE 'down[[:space:]].*-v|[[:space:]]-v([[:space:]]|$)'; then
  fail "cmd_down must not pass -v"
fi
ok "down never uses -v"

if ! awk '/^cmd_destroy\(\)/,/^}/ {print}' "$SCRIPT" | grep -vE '^[[:space:]]*#' | grep -q -- 'down -v'; then
  fail "cmd_destroy must use down -v"
fi
ok "destroy uses down -v after CONFIRM"

if ! grep -q 'docker compose -p "$COMPOSE_PROJECT"' "$SCRIPT"; then
  fail "lifecycle must pass docker compose -p"
fi
ok "compose -p present"

if grep -qE 'sk-|ghp_|xox[baprs]-' "$COMPOSE"; then
  fail "stacklane compose must not interpolate provider tokens"
fi
ok "dev compose has no provider tokens"

if grep -qE '127\.0\.0\.1:[0-9]+:(5432|8080|8081)' "$COMPOSE"; then
  fail "stacklane compose must not use fixed host ports"
fi
if grep -q '0.0.0.0:' "$COMPOSE"; then
  fail "stacklane compose must not wildcard-publish"
fi
if grep -q 'network_mode:[[:space:]]*host' "$COMPOSE"; then
  fail "stacklane compose must not use host network"
fi
for pub in '127.0.0.1::5432' '127.0.0.1::8080' '127.0.0.1::8081'; do
  grep -q "$pub" "$COMPOSE" || fail "stacklane compose must publish $pub"
done
ok "dev publish form"

# Legacy path must keep its documented fixed binds.
grep -q '"5432:5432"' "${ROOT}/docker-compose.yml" || fail "legacy compose lost 5432 bind"
grep -q '"8080:8080"' "${ROOT}/docker-compose.yml" || fail "legacy compose lost 8080 bind"
grep -q '"8081:8081"' "${ROOT}/docker-compose.yml" || fail "legacy compose lost 8081 bind"
ok "legacy compose fixed binds preserved"

# Host path remains the existing script.
grep -q 'bash scripts/dev-stack.sh' "${ROOT}/Taskfile.yml" || fail "task dev must still call scripts/dev-stack.sh"
ok "host task dev preserved"

echo "ok: compose-dev lifecycle tests"
