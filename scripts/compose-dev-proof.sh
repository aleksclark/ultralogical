#!/usr/bin/env bash
# Hot-reload + two-instance isolation proof for the Stacklane compose path.
# Uses unique slc-* probe instances only. Never inherits ambient STACKLANE_INSTANCE.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${ROOT}/scripts/compose-dev.sh"
HEALTH_SRC="${ROOT}/http/server.go"

fail() { echo "FAIL: $*" >&2; exit 1; }
info() { echo "compose-dev-proof: $*" >&2; }

umask 077
WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/ultracore-compose-proof.XXXXXX")"
chmod 700 "$WORKDIR"
HEALTH_BAK="${WORKDIR}/server.go.bak"
touch "$HEALTH_BAK"
chmod 600 "$HEALTH_BAK"

CREATED=()
RESTORE_NEEDED=0
ORIG_SUM=""

rand6() { python3 -c 'import secrets; print(secrets.token_hex(3))'; }

sanitize_probe() {
  local s="${1:-}"
  s="$(printf '%s' "$s" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9-]+/-/g; s/-+/-/g; s/^-+//; s/-+$//')"
  if [[ ${#s} -gt 48 ]]; then
    s="${s:0:48}"
    s="$(printf '%s' "$s" | sed -E 's/-+$//')"
  fi
  printf '%s' "${s:-dev}"
}

project_exists() {
  local project="$1"
  docker compose ls --format json 2>/dev/null \
    | python3 -c 'import json,sys
want=sys.argv[1]
try:
    data=json.load(sys.stdin)
except Exception:
    sys.exit(1)
names=[]
if isinstance(data, list):
    names=[str(x.get("Name") or "") for x in data]
print("yes" if want in names else "no")
' "$project"
}

project_resources_exist() {
  local project="$1"
  docker ps -aq --filter "label=com.docker.compose.project=${project}" | grep -q . && return 0
  docker network ls -q --filter "label=com.docker.compose.project=${project}" | grep -q . && return 0
  docker volume ls -q --filter "label=com.docker.compose.project=${project}" | grep -q . && return 0
  return 1
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ "$RESTORE_NEEDED" -eq 1 && -s "$HEALTH_BAK" ]]; then
    cp -f "$HEALTH_BAK" "$HEALTH_SRC" || true
    if [[ -n "$ORIG_SUM" ]]; then
      local now
      now="$(sha256sum "$HEALTH_SRC" | awk '{print $1}')"
      if [[ "$now" != "$ORIG_SUM" ]]; then
        echo "FAIL: source restore checksum mismatch" >&2
        status=1
      fi
    fi
  fi
  local proj
  for proj in "${CREATED[@]:-}"; do
    [[ -n "$proj" ]] || continue
    info "cleanup $proj"
    if ! CONFIRM="${proj}-destroy" STACKLANE_INSTANCE="${proj#ultracore-}" \
      "$SCRIPT" destroy >/dev/null 2>&1; then
      echo "FAIL: cleanup destroy failed for $proj" >&2
      status=1
      continue
    fi
    if project_resources_exist "$proj"; then
      echo "FAIL: cleanup left compose resources for $proj" >&2
      status=1
    fi
  done
  rm -rf "$WORKDIR"
  exit $status
}
trap cleanup EXIT INT TERM

unset STACKLANE_INSTANCE COMPOSE_FILE COMPOSE_PROFILES COMPOSE_PROJECT_NAME || true

[[ -x "$SCRIPT" ]] || fail "compose-dev.sh not executable"
[[ -f "$HEALTH_SRC" ]] || fail "missing $HEALTH_SRC"
command -v docker >/dev/null 2>&1 || fail "docker required"
command -v python3 >/dev/null 2>&1 || fail "python3 required"

stamp="$(date +%s)"
pid="$$"
token="slc$(rand6)"
INST_A="$(sanitize_probe "slc-a-${pid}-${token}")"
INST_B="$(sanitize_probe "slc-b-${pid}-${token}")"
PROJ_A="ultracore-${INST_A}"
PROJ_B="ultracore-${INST_B}"

if [[ "$(project_exists "$PROJ_A")" == "yes" ]]; then
  fail "refusing to reuse pre-existing project $PROJ_A"
fi
if [[ "$(project_exists "$PROJ_B")" == "yes" ]]; then
  fail "refusing to reuse pre-existing project $PROJ_B"
fi

up_probe() {
  local inst="$1"
  local proj="ultracore-${inst}"
  info "up $proj"
  if ! STACKLANE_INSTANCE="$inst" timeout 600s "$SCRIPT" up; then
    # The project was absent before this proof. Record ownership only when this
    # invocation actually created a labeled resource, so cleanup never destroys
    # a same-named ambient project but does reclaim a partial failed startup.
    if project_resources_exist "$proj"; then
      CREATED+=("$proj")
    fi
    STACKLANE_INSTANCE="$inst" "$SCRIPT" logs --no-color --tail 80 || true
    fail "up failed for $proj"
  fi
  CREATED+=("$proj")
}

direct_url() {
  local inst="$1"
  local svc="$2"
  local target="$3"
  local mapping
  mapping="$(
    docker compose -p "ultracore-${inst}" --project-directory "$ROOT" \
      -f "${ROOT}/docker-compose.stacklane.yml" port "$svc" "$target"
  )"
  printf 'http://127.0.0.1:%s' "${mapping##*:}"
}

http_body() {
  local url="$1"
  python3 - "$url" <<'PY'
import sys, urllib.request
url = sys.argv[1]
with urllib.request.urlopen(url, timeout=3) as r:
    sys.stdout.write(r.read().decode("utf-8", "replace"))
PY
}

wait_body() {
  local url="$1"
  local want="$2"
  local timeout_s="${3:-90}"
  local start now
  start="$(date +%s)"
  while true; do
    now="$(date +%s)"
    if (( now - start > timeout_s )); then
      return 1
    fi
    if body="$(http_body "$url" 2>/dev/null || true)"; then
      if [[ "$body" == *"$want"* ]]; then
        printf '%s' "$body"
        return 0
      fi
    fi
    sleep 1
  done
}

inspect_source_mount() {
  local cid="$1"
  docker inspect -f '{{range .Mounts}}{{if eq .Destination "/src"}}{{.Source}}{{end}}{{end}}' "$cid"
}

info "starting probe A=$INST_A"
up_probe "$INST_A"
info "starting probe B=$INST_B"
up_probe "$INST_B"

API_A="$(direct_url "$INST_A" cored 8080)/healthz"
API_B="$(direct_url "$INST_B" cored 8080)/healthz"
PORT_A="${API_A##*:}"
PORT_A="${PORT_A%%/*}"
PORT_B="${API_B##*:}"
PORT_B="${PORT_B%%/*}"
[[ "$PORT_A" != "$PORT_B" ]] || fail "ephemeral ports must differ (A=$PORT_A B=$PORT_B)"

label_a="$(docker inspect -f '{{index .Config.Labels "stacklane.instance"}}' "${PROJ_A}-cored-1")"
label_b="$(docker inspect -f '{{index .Config.Labels "stacklane.instance"}}' "${PROJ_B}-cored-1")"
[[ "$label_a" == "$INST_A" ]] || fail "label instance A want $INST_A got $label_a"
[[ "$label_b" == "$INST_B" ]] || fail "label instance B want $INST_B got $label_b"

body_a="$(http_body "$API_A")"
body_b="$(http_body "$API_B")"
[[ "$body_a" == "ok" ]] || fail "A healthz want ok got $body_a"
[[ "$body_b" == "ok" ]] || fail "B healthz want ok got $body_b"
info "both instances healthy on distinct ports A=$PORT_A B=$PORT_B"

cid_a="${PROJ_A}-cored-1"
src_mount="$(inspect_source_mount "$cid_a")"
resolved_src="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$src_mount")"
resolved_root="$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$ROOT")"
[[ "$resolved_src" == "$resolved_root" ]] || fail "source mount $resolved_src != worktree $resolved_root"
info "source mount maps to this worktree"

cp -f "$HEALTH_SRC" "$HEALTH_BAK"
ORIG_SUM="$(sha256sum "$HEALTH_BAK" | awk '{print $1}')"
RESTORE_NEEDED=1

nonce="slc-hr-${stamp}-${token}"
python3 - "$HEALTH_SRC" "$nonce" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
nonce = sys.argv[2]
text = path.read_text()
old = '_, _ = w.Write([]byte("ok"))'
new = f'_, _ = w.Write([]byte("{nonce}"))'
if old not in text:
    raise SystemExit("healthz write site not found")
path.write_text(text.replace(old, new, 1))
PY
info "mutated /healthz; waiting for Air reload"

if ! wait_body "$API_A" "$nonce" 120 >/dev/null; then
  STACKLANE_INSTANCE="$INST_A" "$SCRIPT" logs --no-color --tail 80 cored || true
  fail "A did not serve mutated healthz within 120s"
fi
info "A served mutated body $nonce"

# B shares the same bind mount (same worktree). Isolation is project/port/VIP,
# not source trees. Assert B also sees the mutation, then restore.
if ! wait_body "$API_B" "$nonce" 120 >/dev/null; then
  fail "B should observe the same bind-mounted mutation"
fi

cp -f "$HEALTH_BAK" "$HEALTH_SRC"
RESTORE_NEEDED=0
now_sum="$(sha256sum "$HEALTH_SRC" | awk '{print $1}')"
[[ "$now_sum" == "$ORIG_SUM" ]] || fail "restored bytes do not match backup"

if ! wait_body "$API_A" "ok" 120 >/dev/null; then
  fail "A did not restore baseline healthz"
fi
info "source restored; baseline healthz returned"

# Stop A; B must remain healthy.
info "stopping A; B must remain healthy"
STACKLANE_INSTANCE="$INST_A" "$SCRIPT" down
if ! wait_body "$API_B" "ok" 30 >/dev/null; then
  fail "B died after A was stopped"
fi
info "B remained healthy after A down"

if command -v stacklane >/dev/null 2>&1 && timeout 3s stacklane status >/dev/null 2>&1; then
  base="$(STACKLANE_INSTANCE="$INST_B" "$SCRIPT" endpoints 2>/dev/null | awk -F= '/stacklane base_domain/ {print $2; exit}' | tr -d ' ')"
  base="${base:-test}"
  for endpoint in api worker postgres; do
    fqdn="${endpoint}.${INST_B}.ultracore.${base}"
    if timeout 5s stacklane resolve "$fqdn" >/dev/null 2>&1; then
      info "stacklane resolve ok: $fqdn"
    else
      info "stacklane: degraded (resolve missed $fqdn; direct loopback still used)"
    fi
  done
else
  info "stacklane: BLOCKED (daemon/cli absent or down — direct loopback used)"
fi

echo "ok: compose-dev proof A=$INST_A B=$INST_B ports=${PORT_A}/${PORT_B}"
