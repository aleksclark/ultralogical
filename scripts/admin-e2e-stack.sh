#!/usr/bin/env bash
# Boot a disposable Postgres + coreadmin (+ optional cored) stack for the
# Playwright admin API and admin SPA e2e suites. Writes endpoint JSON and tears
# everything down on exit.
#
# Usage:
#   scripts/admin-e2e-stack.sh              # boot, run admin-e2e API tests, tear down
#   scripts/admin-e2e-stack.sh test         # same
#   scripts/admin-e2e-stack.sh spa          # boot, serve SPA via vite, run admin-web e2e
#   scripts/admin-e2e-stack.sh boot         # boot only (writes endpoint JSON path)
#   scripts/admin-e2e-stack.sh run <json>   # run admin-e2e API tests against existing JSON
#   scripts/admin-e2e-stack.sh run-spa <json>  # run admin-web SPA tests against existing JSON
#
# Endpoint JSON fields:
#   admin_url, admin_token, cored_url (optional), database_url, canary_api_key, spa_url (spa mode)
#
# SPA path: Vite dev/preview proxies /admin.v1.* to coreadmin (see admin-web/vite.config.ts).
set -euo pipefail

cd "$(dirname "$0")/.."

mode="${1:-test}"
endpoint_json_in="${2:-}"

suffix="admin-e2e-$$"
pg_port=$((16000 + RANDOM % 1000))
admin_port=$((19000 + RANDOM % 1000))
cored_port=$((20000 + RANDOM % 1000))
spa_port=$((21000 + RANDOM % 1000))

pg_container="ultra-${suffix}-pg"
state_dir=$(mktemp -d -t ultra-admin-e2e-XXXXXX)
endpoint_json="${state_dir}/endpoints.json"
pids=()
start_cored="${ADMIN_E2E_START_CORED:-true}"

admin_token="${CORE_ADMIN_TOKEN:-admin-e2e-admin-token-not-a-tenant-key}"
viewer_token="${CORE_ADMIN_VIEWER_TOKEN:-admin-e2e-viewer-token}"
operator_token="${CORE_ADMIN_OPERATOR_TOKEN:-admin-e2e-operator-token}"
security_token="${CORE_ADMIN_SECURITY_TOKEN:-admin-e2e-security-token}"
cursor_secret="${CORE_ADMIN_CURSOR_SECRET:-admin-e2e-cursor-secret-0123456789abcdef}"
canary_api_key="sk-canary-XyZZy-0451-leak-detector"
# Multi-role map for E7 Playwright; admin_token remains the default SPA/login token.
admin_tokens_json=$(python3 - "$admin_token" "$viewer_token" "$operator_token" "$security_token" <<'PY'
import json, sys
admin, viewer, operator, security = sys.argv[1:]
print(json.dumps({
    admin: {"role": "admin", "name": "e2e-admin", "id": "e2e-admin"},
    viewer: {"role": "viewer", "name": "e2e-viewer", "id": "e2e-viewer"},
    operator: {"role": "operator", "name": "e2e-operator", "id": "e2e-operator"},
    security: {"role": "security", "name": "e2e-security", "id": "e2e-security"},
}))
PY
)

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
  CORE_ADMIN_TOKENS="$admin_tokens_json" \
  CORE_ADMIN_CURSOR_SECRET="$cursor_secret" \
  CORE_ADMIN_REVEAL_ENABLED="${CORE_ADMIN_REVEAL_ENABLED:-false}" \
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

  python3 - "$endpoint_json" "$admin_url" "$admin_token" "$viewer_token" "$operator_token" "$security_token" "$cored_url" "$database_url" "$canary_api_key" <<'PY'
import json, sys
(path, admin_url, token, viewer, operator, security, cored_url, database_url, canary) = sys.argv[1:]
payload = {
    "admin_url": admin_url,
    "admin_token": token,
    "viewer_token": viewer,
    "operator_token": operator,
    "security_token": security,
    "cored_url": cored_url or None,
    "database_url": database_url,
    "canary_api_key": canary,
    "spa_url": None,
    "reveal_enabled": False,
}
with open(path, "w", encoding="utf-8") as f:
    json.dump(payload, f, indent=2)
    f.write("\n")
print(path)
PY

  log "endpoints written to $endpoint_json"
}

ensure_admin_ts() {
  if [ ! -e admin-e2e/src/gen ]; then
    ln -sfn ../../clients/admin-ts/src/gen admin-e2e/src/gen || fail "link admin gen"
  fi
  if [ ! -e admin-web/src/gen ]; then
    ln -sfn ../../clients/admin-ts/src/gen admin-web/src/gen || fail "link admin-web gen"
  fi
  if [ ! -d clients/admin-ts/node_modules ]; then
    log "installing clients/admin-ts npm deps (for generated imports)"
    (cd clients/admin-ts && npm install) || fail "admin-ts npm install"
  fi
}

run_playwright() {
  local json_path="$1"
  if [ ! -f "$json_path" ]; then
    fail "endpoint JSON not found: $json_path"
  fi
  ensure_admin_ts
  if [ ! -d admin-e2e/node_modules ]; then
    log "installing admin-e2e npm deps"
    if [ -f admin-e2e/package-lock.json ]; then
      (cd admin-e2e && npm ci) || fail "npm ci"
    else
      (cd admin-e2e && npm install) || fail "npm install"
    fi
  fi
  log "running admin-e2e Playwright against $json_path"
  (
    cd admin-e2e
    ADMIN_E2E_ENDPOINTS="$json_path" npx playwright test
  )
}

start_spa() {
  local admin_url="$1"
  ensure_admin_ts
  if [ ! -d admin-web/node_modules ]; then
    log "installing admin-web npm deps"
    if [ -f admin-web/package-lock.json ]; then
      (cd admin-web && npm ci) || fail "admin-web npm ci"
    else
      (cd admin-web && npm install) || fail "admin-web npm install"
    fi
  fi
  # Import gates before serving.
  (cd admin-web && node scripts/check-import-gates.mjs) || fail "admin-web import gates"

  # Prefer preview of a production build for stable e2e; fall back to vite dev.
  log "building admin-web"
  (cd admin-web && npm run build) || fail "admin-web build"

  log "starting SPA preview on port ${spa_port} (proxy → ${admin_url})"
  (
    cd admin-web
    CORE_ADMIN_URL="$admin_url" npx vite preview --host 127.0.0.1 --port "$spa_port"
  ) >"$state_dir/spa.log" 2>&1 &
  pids+=($!)
  spa_url="http://127.0.0.1:${spa_port}"
  wait_http "$spa_url" "admin-web spa"

  python3 - "$endpoint_json" "$spa_url" <<'PY'
import json, sys
path, spa_url = sys.argv[1:]
with open(path, encoding="utf-8") as f:
    payload = json.load(f)
payload["spa_url"] = spa_url
with open(path, "w", encoding="utf-8") as f:
    json.dump(payload, f, indent=2)
    f.write("\n")
PY
  log "spa_url=$spa_url written to endpoints"
}

run_spa_playwright() {
  local json_path="$1"
  if [ ! -f "$json_path" ]; then
    fail "endpoint JSON not found: $json_path"
  fi
  ensure_admin_ts
  if [ ! -d admin-web/node_modules ]; then
    log "installing admin-web npm deps"
    if [ -f admin-web/package-lock.json ]; then
      (cd admin-web && npm ci) || fail "admin-web npm ci"
    else
      (cd admin-web && npm install) || fail "admin-web npm install"
    fi
  fi
  # Ensure browsers
  (cd admin-web && npx playwright install chromium) || fail "playwright install"

  spa_url=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("spa_url") or "")' "$json_path")
  [ -n "$spa_url" ] || fail "endpoints.json missing spa_url — boot with spa mode first"

  log "running admin-web Playwright against $spa_url"
  (
    cd admin-web
    ADMIN_E2E_ENDPOINTS="$json_path" \
    ADMIN_WEB_URL="$spa_url" \
      npx playwright test
  )
}

case "$mode" in
  test|"")
    boot_stack
    run_playwright "$endpoint_json"
    ;;
  spa)
    # API stack + SPA for admin-web e2e
    start_cored=false
    boot_stack
    admin_url=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["admin_url"])' "$endpoint_json")
    start_spa "$admin_url"
    run_spa_playwright "$endpoint_json"
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
  run-spa)
    [ -n "$endpoint_json_in" ] || fail "usage: $0 run-spa <endpoints.json>"
    run_spa_playwright "$endpoint_json_in"
    ;;
  *)
    fail "unknown mode: $mode (expected test|spa|boot|run|run-spa)"
    ;;
esac
