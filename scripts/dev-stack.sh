#!/usr/bin/env bash
# One-command local development stack: Postgres, a local model endpoint,
# ultrad, the worker, and the web application, plus seeded org, user, provider,
# and credential records so the stack is usable rather than merely running.
#
# Usage:
#   scripts/dev-stack.sh          # boot the stack and stay in the foreground
#   scripts/dev-stack.sh smoke    # boot, run a noninteractive smoke, tear down
#
# Every resource the script owns is named with a run-scoped prefix and removed
# on exit, so neither mode can leak a process or a container.
set -uo pipefail

cd "$(dirname "$0")/.."
mode="${1:-up}"

: "${ULTRA_BEZALEL_IMAGE:=ultralogical/bezalel:phase2-test}"
: "${ULTRA_DEV_TOKEN:=dev-token}"
: "${ULTRA_DEV_EMAIL:=dev@example.com}"

if [ "$mode" = "smoke" ]; then
  # The smoke run must not collide with a developer's long-lived stack.
  suffix="smoke-$$"
  pg_port=$((15400 + RANDOM % 300))
  api_port=$((18100 + RANDOM % 300))
  model_port=$((18500 + RANDOM % 300))
  web_port=$((18900 + RANDOM % 300))
else
  suffix="dev"
  pg_port="${ULTRA_DEV_PG_PORT:-5499}"
  api_port="${ULTRA_DEV_API_PORT:-8080}"
  model_port="${ULTRA_DEV_MODEL_PORT:-8091}"
  web_port="${ULTRA_DEV_WEB_PORT:-5173}"
fi

pg_container="ultra-${suffix}-pg"
state_dir=$(mktemp -d)
pids=()

log() { printf '[dev-stack] %s\n' "$*"; }
fail() { printf '[dev-stack] FAILED: %s\n' "$*" >&2; exit 1; }

cleanup() {
  local status=$?
  log "shutting down"
  for pid in "${pids[@]:-}"; do
    if [ -n "${pid:-}" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null
      # Children get a grace period, then a hard stop, so nothing survives.
      for _ in $(seq 1 20); do
        kill -0 "$pid" 2>/dev/null || break
        sleep 0.3
      done
      kill -9 "$pid" 2>/dev/null
      wait "$pid" 2>/dev/null
    fi
  done
  # The smoke run owns its Postgres container and any environment containers it
  # provisioned; a developer stack keeps its database between runs.
  if [ "$mode" = "smoke" ]; then
    local envs
    envs=$(docker ps -aq --filter "label=ultralogical.env_id" 2>/dev/null)
    if [ -n "$envs" ]; then
      docker rm -f $envs >/dev/null 2>&1
    fi
    docker rm -f "$pg_container" >/dev/null 2>&1
  fi
  rm -rf "$state_dir"
  exit $status
}
trap cleanup EXIT INT TERM

master_key="${ULTRA_MASTER_KEY:-}"
if [ -z "$master_key" ]; then
  master_key=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
  log "generated an ephemeral ULTRA_MASTER_KEY for this run"
fi

log "starting Postgres on port ${pg_port}"
if ! docker start "$pg_container" >/dev/null 2>&1; then
  docker run -d --name "$pg_container" \
    -e POSTGRES_PASSWORD=dev \
    -p "${pg_port}:5432" \
    postgres:17-alpine >/dev/null || fail "could not start Postgres"
fi

database_url="postgres://postgres:dev@127.0.0.1:${pg_port}/postgres?sslmode=disable"

log "waiting for Postgres"
for _ in $(seq 1 120); do
  if docker exec "$pg_container" pg_isready -U postgres >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
[ "${ready:-0}" = 1 ] || fail "Postgres never became ready"

log "building binaries"
go build -o "$state_dir/ultrad" ./cmd/ultrad || fail "build ultrad"
go build -o "$state_dir/worker" ./cmd/worker || fail "build worker"
go build -o "$state_dir/devstack" ./cmd/devstack || fail "build devstack"

model_url="http://127.0.0.1:${model_port}/v1"
log "starting the local model endpoint on port ${model_port}"
ULTRA_MODEL_ADDR="127.0.0.1:${model_port}" "$state_dir/devstack" model \
  >"$state_dir/model.log" 2>&1 &
pids+=($!)

log "seeding org, user, provider, and credential"
DATABASE_URL="$database_url" \
ULTRA_MASTER_KEY="$master_key" \
ULTRA_DEV_EMAIL="$ULTRA_DEV_EMAIL" \
ULTRA_MODEL_URL="$model_url" \
  "$state_dir/devstack" seed >"$state_dir/seed.json" 2>"$state_dir/seed.err" \
  || { cat "$state_dir/seed.err" >&2; fail "seed"; }
org_id=$(python3 -c "import json;print(json.load(open('$state_dir/seed.json'))['org_id'])")
log "seeded org $org_id"

log "starting ultrad on port ${api_port}"
DATABASE_URL="$database_url" \
ULTRA_ADDR="127.0.0.1:${api_port}" \
ULTRA_DEV_TOKENS="${ULTRA_DEV_TOKEN}=${ULTRA_DEV_EMAIL}" \
ULTRA_MASTER_KEY="$master_key" \
ULTRA_DEFAULT_MODEL=devstack \
  "$state_dir/ultrad" >"$state_dir/ultrad.log" 2>&1 &
pids+=($!)

log "starting worker"
DATABASE_URL="$database_url" \
ULTRA_MASTER_KEY="$master_key" \
ULTRA_BEZALEL_IMAGE="$ULTRA_BEZALEL_IMAGE" \
ULTRA_RECONCILE_INTERVAL=2s \
  "$state_dir/worker" >"$state_dir/worker.log" 2>&1 &
pids+=($!)

api="http://127.0.0.1:${api_port}"
log "waiting for ultrad at $api"
for _ in $(seq 1 240); do
  if python3 - "$api" <<'PY' >/dev/null 2>&1
import sys, urllib.request
urllib.request.urlopen(sys.argv[1] + "/healthz", timeout=1)
PY
  then
    healthy=1
    break
  fi
  sleep 0.5
done
if [ "${healthy:-0}" != 1 ]; then
  tail -50 "$state_dir/ultrad.log" >&2
  fail "ultrad never became healthy"
fi

if [ "$mode" = "smoke" ]; then
  log "running the noninteractive smoke"
  if ! ULTRA_SMOKE_API="$api" \
       ULTRA_SMOKE_TOKEN="$ULTRA_DEV_TOKEN" \
       ULTRA_SMOKE_ORG="$org_id" \
       "$state_dir/devstack" smoke; then
    echo "--- ultrad log ---" >&2
    tail -50 "$state_dir/ultrad.log" >&2
    echo "--- worker log ---" >&2
    tail -50 "$state_dir/worker.log" >&2
    fail "smoke failed"
  fi
  log "smoke passed"
  exit 0
fi

log "starting the web application on port ${web_port}"
if [ -d ui/web/node_modules ]; then
  (cd ui/web && VITE_ULTRAD_URL="$api" exec npm run dev -- --host 127.0.0.1 --port "$web_port" \
    >"$state_dir/web.log" 2>&1) &
  pids+=($!)
  log "web application: http://127.0.0.1:${web_port}"
else
  log "ui/web/node_modules missing; run 'npm ci' in ui/web to include the web app"
fi

cat <<EOF
[dev-stack] stack is up
  API:      $api
  token:    $ULTRA_DEV_TOKEN
  org:      $org_id
  model:    $model_url
  Postgres: $database_url
  logs:     $state_dir
Press Ctrl-C to stop everything.
EOF

wait
