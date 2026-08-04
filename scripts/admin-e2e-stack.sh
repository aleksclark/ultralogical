#!/usr/bin/env bash
# Boot a disposable Postgres + coreadmin (+ optional cored) stack for the
# Playwright admin API e2e suite. Writes endpoint JSON and tears everything
# down on exit.
#
# Usage:
#   scripts/admin-e2e-stack.sh              # boot, run playwright, tear down
#   scripts/admin-e2e-stack.sh boot         # boot only (writes endpoint JSON path)
#   scripts/admin-e2e-stack.sh run <json>   # run playwright against existing JSON
#
# Endpoint JSON fields:
#   admin_url, admin_token, cored_url (optional), database_url, canary_api_key
set -euo pipefail

cd "$(dirname "$0")/.."

mode="${1:-test}"
endpoint_json_in="${2:-}"

suffix="admin-e2e-$$"
pg_port=$((16000 + RANDOM % 1000))
admin_port=$((19000 + RANDOM % 1000))
cored_port=$((20000 + RANDOM % 1000))

pg_container="ultra-${suffix}-pg"
state_dir=$(mktemp -d -t ultra-admin-e2e-XXXXXX)
endpoint_json="${state_dir}/endpoints.json"
pids=()
start_cored="${ADMIN_E2E_START_CORED:-true}"

admin_token="${CORE_ADMIN_TOKEN:-admin-e2e-operator-token-not-a-tenant-key}"
cursor_secret="${CORE_ADMIN_CURSOR_SECRET:-admin-e2e-cursor-secret-0123456789abcdef}"
canary_api_key="sk-canary-XyZZy-0451-leak-detector"

log() { printf '[admin-e2e-stack] %s\n' "$*"; }
fail() { printf '[admin-e2e-stack] FAILED: %s\n' "$*" >&2; exit 1; }

cleanup() {
  local status=$?
  log "shutting down"
  for pid in "${pids[@]:-}"; do
    if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      for _ in $(seq 1 20); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.2
      done
      kill -9 "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  docker rm -f "$pg_container" >/dev/null 2>&1 || true
  if [ "${KEEP_ADMIN_E2E_STATE:-}" != "1" ]; then
    rm -rf "$state_dir"
  else
    log "kept state dir: $state_dir"
  fi
  exit $status
}

wait_http() {
  local url="$1" label="$2" attempts="${3:-120}"
  for _ in $(seq 1 "$attempts"); do
    if python3 - "$url" <<'PY' >/dev/null 2>&1
import sys, urllib.request
urllib.request.urlopen(sys.argv[1], timeout=1)
PY
    then
      return 0
    fi
    sleep 0.25
  done
  fail "$label never became ready at $url"
}

boot_stack() {
  trap cleanup EXIT INT TERM

  log "starting Postgres on port ${pg_port}"
  docker run -d --name "$pg_container" \
    -e POSTGRES_PASSWORD=dev \
    -p "${pg_port}:5432" \
    postgres:17-alpine >/dev/null || fail "could not start Postgres"

  database_url="postgres://postgres:dev@127.0.0.1:${pg_port}/postgres?sslmode=disable"

  log "waiting for Postgres"
  ready=0
  for _ in $(seq 1 120); do
    if docker exec "$pg_container" pg_isready -U postgres >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 0.25
  done
  [ "$ready" = 1 ] || fail "Postgres never became ready"

  log "building binaries"
  go build -o "$state_dir/coreadmin" ./cmd/coreadmin || fail "build coreadmin"
  go build -o "$state_dir/seed" ./admin-e2e/cmd/seed || fail "build seed"
  if [ "$start_cored" = "true" ]; then
    go build -o "$state_dir/cored" ./cmd/cored || fail "build cored"
  fi

  log "starting coreadmin on port ${admin_port}"
  DATABASE_URL="$database_url" \
  CORE_ADMIN_ADDR="127.0.0.1:${admin_port}" \
  CORE_ADMIN_TOKEN="$admin_token" \
  CORE_ADMIN_CURSOR_SECRET="$cursor_secret" \
  CORE_MIGRATE=true \
    "$state_dir/coreadmin" >"$state_dir/coreadmin.log" 2>&1 &
  pids+=($!)

  admin_url="http://127.0.0.1:${admin_port}"
  wait_http "${admin_url}/healthz" "coreadmin"
  wait_http "${admin_url}/readyz" "coreadmin readyz"

  cored_url=""
  if [ "$start_cored" = "true" ]; then
    # cored requires CORE_MASTER_KEY; migrations already applied by coreadmin.
    master_key="${CORE_MASTER_KEY:-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}"
    log "starting cored on port ${cored_port} (admin path isolation checks)"
    DATABASE_URL="$database_url" \
    CORE_ADDR="127.0.0.1:${cored_port}" \
    CORE_MASTER_KEY="$master_key" \
    CORE_MIGRATE=false \
    CORE_PROVIDER_KINDS=null,static \
      "$state_dir/cored" >"$state_dir/cored.log" 2>&1 &
    pids+=($!)
    cored_url="http://127.0.0.1:${cored_port}"
    wait_http "${cored_url}/healthz" "cored"
  fi

  log "seeding fixtures"
  DATABASE_URL="$database_url" \
  ADMIN_E2E_CANARY_KEY="$canary_api_key" \
  ADMIN_E2E_TENANT_COUNT="${ADMIN_E2E_TENANT_COUNT:-60}" \
    "$state_dir/seed" || {
      tail -80 "$state_dir/coreadmin.log" >&2 || true
      fail "seed"
    }

  python3 - "$endpoint_json" "$admin_url" "$admin_token" "$cored_url" "$database_url" "$canary_api_key" <<'PY'
import json, sys
path, admin_url, token, cored_url, database_url, canary = sys.argv[1:]
payload = {
    "admin_url": admin_url,
    "admin_token": token,
    "cored_url": cored_url or None,
    "database_url": database_url,
    "canary_api_key": canary,
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(payload, f, indent=2)
    f.write("\n")
print(path)
PY

  log "endpoints written to $endpoint_json"
}

run_playwright() {
  local json_path="$1"
  if [ ! -f "$json_path" ]; then
    fail "endpoint JSON not found: $json_path"
  fi
  # Generated admin TS client is linked in so Playwright can transform it
  # (protoc-gen-es emits .js import specifiers; node_modules is not transformed).
  if [ ! -e admin-e2e/src/gen ]; then
    ln -sfn ../../clients/admin-ts/src/gen admin-e2e/src/gen || fail "link admin gen"
  fi
  # Gen sources resolve @bufbuild/protobuf from clients/admin-ts/node_modules.
  if [ ! -d clients/admin-ts/node_modules ]; then
    log "installing clients/admin-ts npm deps (for generated imports)"
    (cd clients/admin-ts && npm install) || fail "admin-ts npm install"
  fi
  if [ ! -d admin-e2e/node_modules ]; then
    log "installing admin-e2e npm deps"
    if [ -f admin-e2e/package-lock.json ]; then
      (cd admin-e2e && npm ci) || fail "npm ci"
    else
      (cd admin-e2e && npm install) || fail "npm install"
    fi
  fi
  log "running Playwright against $json_path"
  (
    cd admin-e2e
    ADMIN_E2E_ENDPOINTS="$json_path" npx playwright test
  )
}

case "$mode" in
  test|"")
    boot_stack
    run_playwright "$endpoint_json"
    ;;
  boot)
    # Leave stack running until this process is killed; print JSON path.
    KEEP_ADMIN_E2E_STATE=1
    boot_stack
    echo "$endpoint_json"
    # Hold open until signalled.
    wait
    ;;
  run)
    [ -n "$endpoint_json_in" ] || fail "usage: $0 run <endpoints.json>"
    # No cleanup trap for external stack.
    run_playwright "$endpoint_json_in"
    ;;
  *)
    fail "unknown mode: $mode (expected test|boot|run)"
    ;;
esac
