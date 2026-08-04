#!/usr/bin/env bash
# One-command local development stack: Postgres, a local model endpoint,
# cored and the worker, plus a seeded tenant, admin API key, provider,
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

: "${CORE_BEZALEL_IMAGE:=ultracore/bezalel:phase2-test}"

if [ "$mode" = "smoke" ]; then
  # The smoke run must not collide with a developer's long-lived stack.
  suffix="smoke-$$"
  pg_port=$((15400 + RANDOM % 300))
  api_port=$((18100 + RANDOM % 300))
  model_port=$((18500 + RANDOM % 300))
else
  suffix="dev"
  pg_port="${CORE_DEV_PG_PORT:-5499}"
  api_port="${CORE_DEV_API_PORT:-8080}"
  model_port="${CORE_DEV_MODEL_PORT:-8091}"
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
  # The smoke run owns its Postgres container and any resource containers it
  # provisioned; a developer stack keeps its database between runs.
  if [ "$mode" = "smoke" ]; then
    local resources
    resources=$(docker ps -aq --filter "label=ultracore.resource_id" 2>/dev/null)
    if [ -n "$resources" ]; then
      docker rm -f $resources >/dev/null 2>&1
    fi
    docker rm -f "$pg_container" >/dev/null 2>&1
  fi
  rm -rf "$state_dir"
  exit $status
}
trap cleanup EXIT INT TERM

master_key="${CORE_MASTER_KEY:-}"
if [ -z "$master_key" ]; then
  master_key=$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
  log "generated an ephemeral CORE_MASTER_KEY for this run"
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
go build -o "$state_dir/cored" ./cmd/cored || fail "build cored"
go build -o "$state_dir/worker" ./cmd/coreworker || fail "build worker"
go build -o "$state_dir/devstack" ./cmd/devstack || fail "build devstack"

model_url="http://127.0.0.1:${model_port}/v1"
log "starting the local model endpoint on port ${model_port}"
CORE_MODEL_ADDR="127.0.0.1:${model_port}" "$state_dir/devstack" model \
  >"$state_dir/model.log" 2>&1 &
pids+=($!)

log "seeding tenant, admin API key, provider, and credential"
DATABASE_URL="$database_url" \
CORE_MASTER_KEY="$master_key" \
CORE_MODEL_URL="$model_url" \
  "$state_dir/devstack" seed >"$state_dir/seed.json" 2>"$state_dir/seed.err" \
  || { cat "$state_dir/seed.err" >&2; fail "seed"; }
tenant_id=$(python3 -c "import json;print(json.load(open('$state_dir/seed.json'))['tenant_id'])")
api_key=$(python3 -c "import json;print(json.load(open('$state_dir/seed.json'))['api_key'])")
log "seeded tenant $tenant_id"

log "starting cored on port ${api_port}"
DATABASE_URL="$database_url" \
CORE_ADDR="127.0.0.1:${api_port}" \
CORE_MASTER_KEY="$master_key" \
CORE_DEFAULT_PROVIDER=openai \
CORE_DEFAULT_MODEL=devstack \
CORE_MIGRATE=false \
CORE_PROVIDER_KINDS=local_docker,null,static \
  "$state_dir/cored" >"$state_dir/cored.log" 2>&1 &
pids+=($!)

log "starting worker"
DATABASE_URL="$database_url" \
CORE_MASTER_KEY="$master_key" \
CORE_BEZALEL_IMAGE="$CORE_BEZALEL_IMAGE" \
CORE_RECONCILE_INTERVAL=2s \
CORE_PROVIDER_KINDS=local_docker,null,static \
  "$state_dir/worker" >"$state_dir/worker.log" 2>&1 &
pids+=($!)

api="http://127.0.0.1:${api_port}"
log "waiting for cored at $api"
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
  tail -50 "$state_dir/cored.log" >&2
  fail "cored never became healthy"
fi

if [ "$mode" = "smoke" ]; then
  log "running the noninteractive smoke"
  if ! CORE_SMOKE_API="$api" \
       CORE_SMOKE_TOKEN="$api_key" \
       CORE_SMOKE_TENANT="$tenant_id" \
       "$state_dir/devstack" smoke; then
    echo "--- cored log ---" >&2
    tail -50 "$state_dir/cored.log" >&2
    echo "--- worker log ---" >&2
    tail -50 "$state_dir/worker.log" >&2
    fail "smoke failed"
  fi
  log "smoke passed"
  exit 0
fi

cat <<EOF
[dev-stack] stack is up
  API:      $api
  token:    $api_key
  tenant:   $tenant_id
  model:    $model_url
  Postgres: $database_url
  logs:     $state_dir
Press Ctrl-C to stop everything.
EOF

wait
