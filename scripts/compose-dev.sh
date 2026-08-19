#!/usr/bin/env bash
# ultracore Stacklane compose lifecycle (additive; does not replace task dev).
# Always uses: docker compose -p "ultracore-<instance>" -f "$ROOT/docker-compose.stacklane.yml"
set -euo pipefail

# Neutralize accidental ambient Compose controls before assigning locals.
# STACKLANE_INSTANCE remains a documented operator input and is derived below.
unset COMPOSE_FILE COMPOSE_PROFILES COMPOSE_PROJECT_NAME

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="${ROOT}/docker-compose.stacklane.yml"
PROJECT_SLUG="ultracore"

die() { echo "error: $*" >&2; exit 1; }
info() { echo "ultracore-compose: $*" >&2; }

# sanitize_instance: lowercase, non [a-z0-9-] → -, collapse dashes, trim, max 48, fallback dev
sanitize_instance() {
  local s="${1:-}"
  s="$(printf '%s' "$s" | tr '[:upper:]' '[:lower:]')"
  s="$(printf '%s' "$s" | sed -E 's/[^a-z0-9-]+/-/g; s/-+/-/g; s/^-+//; s/-+$//')"
  if [[ ${#s} -gt 48 ]]; then
    s="${s:0:48}"
    s="$(printf '%s' "$s" | sed -E 's/-+$//')"
  fi
  if [[ -z "$s" ]]; then
    s="dev"
  fi
  printf '%s' "$s"
}

derive_instance() {
  if [[ -n "${STACKLANE_INSTANCE:-}" ]]; then
    sanitize_instance "$STACKLANE_INSTANCE"
    return
  fi
  local wt
  wt="$(basename "$ROOT")"
  if [[ -n "$wt" && "$wt" != "." && "$wt" != "/" && "$wt" != "$PROJECT_SLUG" && "$wt" != "ultralogical" ]]; then
    sanitize_instance "$wt"
    return
  fi
  local branch=""
  if command -v git >/dev/null 2>&1; then
    branch="$(git -C "$ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  fi
  if [[ -n "$branch" && "$branch" != "HEAD" ]]; then
    sanitize_instance "$branch"
    return
  fi
  sanitize_instance "dev"
}

# valid_base_domain prints a normalized DNS suffix. Stacklane FQDNs must
# never use .local (mDNS), including when an operator supplies an override.
valid_base_domain() {
  local domain="${1,,}"
  [[ ${#domain} -le 127 ]] || return 1
  [[ "$domain" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$ ]] || return 1
  [[ "$domain" != "local" && "$domain" != *.local ]] || return 1
  printf '%s' "$domain"
}

detect_base_domain() {
  if [[ -n "${STACKLANE_BASE_DOMAIN:-}" ]]; then
    valid_base_domain "$STACKLANE_BASE_DOMAIN" || die "STACKLANE_BASE_DOMAIN must be a DNS suffix other than .local"
    return
  fi
  if ! command -v stacklane >/dev/null 2>&1; then
    printf 'test'
    return
  fi
  local detected=""
  detected="$(
    timeout 3s stacklane status -o json 2>/dev/null \
      | python3 -c 'import json,sys
try:
    data=json.load(sys.stdin)
except Exception:
    sys.exit(0)
val=data.get("base_domain") if isinstance(data, dict) else ""
if isinstance(val, str):
    print(val)
' || true
  )"
  if [[ -n "$detected" ]] && valid_base_domain "$detected"; then
    return
  fi
  printf 'test'
}

require_docker() {
  command -v docker >/dev/null 2>&1 || die "docker not found"
  docker compose version >/dev/null 2>&1 || die "docker compose not available"
  [[ -f "$COMPOSE_FILE" ]] || die "missing $COMPOSE_FILE"
  command -v python3 >/dev/null 2>&1 || die "python3 required for compose check"
}

compose() {
  docker compose -p "$COMPOSE_PROJECT" --project-directory "$ROOT" -f "$COMPOSE_FILE" "$@"
}

export_stack_env() {
  INSTANCE="$(derive_instance)"
  COMPOSE_PROJECT="${PROJECT_SLUG}-${INSTANCE}"
  STACKLANE_BASE_DOMAIN="$(detect_base_domain)"
  export STACKLANE_INSTANCE="$INSTANCE"
  export STACKLANE_BASE_DOMAIN
  export COMPOSE_PROJECT_NAME="$COMPOSE_PROJECT"
}

host_port_for() {
  local svc="$1"
  local target="$2"
  local mapping
  mapping="$(compose port "$svc" "$target" 2>/dev/null || true)"
  if [[ -z "$mapping" ]]; then
    printf ''
    return
  fi
  printf '%s' "${mapping##*:}"
}

stacklane_status_line() {
  if ! command -v stacklane >/dev/null 2>&1; then
    printf 'stacklane: BLOCKED (daemon/cli absent — direct loopback ports still work)\n'
    return
  fi
  if timeout 3s stacklane status >/dev/null 2>&1; then
    local api_fqdn="api.${INSTANCE}.${PROJECT_SLUG}.${STACKLANE_BASE_DOMAIN}"
    if timeout 3s stacklane resolve "$api_fqdn" >/dev/null 2>&1; then
      printf 'stacklane: OK\n'
    else
      printf 'stacklane: degraded (daemon up; %s not resolved yet)\n' "$api_fqdn"
    fi
  else
    printf 'stacklane: BLOCKED (daemon not reachable)\n'
  fi
}

print_endpoints() {
  local api_hp worker_hp pg_hp base
  api_hp="$(host_port_for cored 8080)"
  worker_hp="$(host_port_for coreworker 8081)"
  pg_hp="$(host_port_for postgres 5432)"
  base="${STACKLANE_BASE_DOMAIN}"

  echo "api.${INSTANCE}.${PROJECT_SLUG}.${base}:8080  (via Stacklane VIP)"
  echo "worker.${INSTANCE}.${PROJECT_SLUG}.${base}:8081  (via Stacklane VIP)"
  echo "postgres.${INSTANCE}.${PROJECT_SLUG}.${base}:5432  (via Stacklane VIP)"
  if [[ -n "$api_hp" ]]; then
    echo "direct api:      http://127.0.0.1:${api_hp}/"
    echo "direct health:   http://127.0.0.1:${api_hp}/healthz"
    echo "direct ready:    http://127.0.0.1:${api_hp}/readyz"
  else
    echo "direct api:      (not published — stack down?)"
  fi
  if [[ -n "$worker_hp" ]]; then
    echo "direct worker:   http://127.0.0.1:${worker_hp}/readyz"
  else
    echo "direct worker:   (not published — stack down?)"
  fi
  if [[ -n "$pg_hp" ]]; then
    echo "direct postgres: 127.0.0.1:${pg_hp}"
  else
    echo "direct postgres: (not published — stack down?)"
  fi
  stacklane_status_line
  echo "instance: ${INSTANCE}"
  echo "compose project: ${COMPOSE_PROJECT}"
  echo "stacklane base_domain: ${base}"
}

wait_healthy() {
  local timeout_s="${1:-420}"
  local start now elapsed
  start="$(date +%s)"
  info "waiting for postgres/cored/coreworker healthy (timeout ${timeout_s}s)…"
  while true; do
    now="$(date +%s)"
    elapsed=$((now - start))
    if (( elapsed > timeout_s )); then
      compose ps || true
      die "services not healthy within ${timeout_s}s"
    fi
    local pg_h api_h worker_h
    pg_h="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${COMPOSE_PROJECT}-postgres-1" 2>/dev/null || echo missing)"
    api_h="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${COMPOSE_PROJECT}-cored-1" 2>/dev/null || echo missing)"
    worker_h="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${COMPOSE_PROJECT}-coreworker-1" 2>/dev/null || echo missing)"
    if [[ "$pg_h" == "healthy" && "$api_h" == "healthy" && "$worker_h" == "healthy" ]]; then
      info "postgres/cored/coreworker healthy"
      return 0
    fi
    sleep 2
  done
}

# Fail-closed render + contract assertions. Never prints rendered JSON or Compose stderr.
cmd_check() {
  require_docker
  export_stack_env
  command -v python3 >/dev/null 2>&1 || die "python3 required for compose check"

  (
  COMPOSE_CHECK_UMASK="$(umask)"
  umask 077
  tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/ultracore-compose-check.XXXXXX")"
  chmod 700 "$tmpdir"
  cleanup_check() {
    rm -rf "${tmpdir:-}"
    umask "${COMPOSE_CHECK_UMASK:-022}"
  }
  trap cleanup_check EXIT INT TERM

  local out errf
  out="${tmpdir}/compose.config.json"
  errf="${tmpdir}/compose.config.err"
  touch "$out" "$errf"
  chmod 600 "$out" "$errf"

  info "rendering compose config for instance=${INSTANCE} project=${COMPOSE_PROJECT}"
  if ! timeout 60s docker compose -p "$COMPOSE_PROJECT" --project-directory "$ROOT" -f "$COMPOSE_FILE" \
      config --format json >"$out" 2>"$errf"; then
    echo "FAIL: compose-config-render" >&2
    exit 1
  fi

  if ! python3 - "$out" "$INSTANCE" "$COMPOSE_PROJECT" "$PROJECT_SLUG" <<'PY'
import json, sys, re

path, expect_instance, expect_project, project_slug = sys.argv[1:5]
try:
    with open(path, "r", encoding="utf-8") as f:
        cfg = json.load(f)
except Exception:
    print("FAIL: compose-config-parse", file=sys.stderr)
    sys.exit(1)

errors = []

def err(rule):
    errors.append(rule)

services = cfg.get("services") or {}
for name in ("postgres", "cored", "coreworker"):
    if name not in services:
        err(f"missing-service-{name}")

name = cfg.get("name") or ""
if name and name != expect_project:
    err("compose-project-identity")

volumes_top = cfg.get("volumes") or {}
vol_keys = set(volumes_top.keys())
named_required = (
    "ultracore_pgdata",
    "ultracore_go_mod_cache",
    "ultracore_go_build_cache",
    "ultracore_cored_air_tmp",
    "ultracore_coreworker_air_tmp",
)
for req in named_required:
    if req not in vol_keys and not any(req in k for k in vol_keys):
        err(f"named-volume-{req}")

def labels_map(sc):
    labels = sc.get("labels") or {}
    if isinstance(labels, list):
        out = {}
        for item in labels:
            if isinstance(item, str) and "=" in item:
                k, v = item.split("=", 1)
                out[k] = v
        return out
    if isinstance(labels, dict):
        return {str(k): str(v) for k, v in labels.items()}
    return {}

def volume_entries(sc):
    return sc.get("volumes") or []

def has_bind(sc, target):
    for v in volume_entries(sc):
        if isinstance(v, dict):
            tgt = v.get("target") or v.get("destination") or ""
            typ = (v.get("type") or "").lower()
            if typ == "bind" and tgt.rstrip("/") == target.rstrip("/"):
                return True
        elif isinstance(v, str) and f":{target}" in v:
            return True
    return False

def has_named(sc, name_part):
    for v in volume_entries(sc):
        if isinstance(v, dict):
            src = str(v.get("source") or "")
            if name_part in src:
                return True
        elif isinstance(v, str) and name_part in v:
            return True
    return False

def env_map(sc):
    env = sc.get("environment") or {}
    if isinstance(env, list):
        out = {}
        for e in env:
            if isinstance(e, str):
                if "=" in e:
                    k, v = e.split("=", 1)
                    out[k] = v
                else:
                    out[e] = ""
        return out
    if isinstance(env, dict):
        return {str(k): "" if v is None else str(v) for k, v in env.items()}
    return {}

def check_isolation(svc_name, sc):
    if (sc.get("network_mode") or "") == "host":
        err(f"no-host-network-{svc_name}")
    if str(sc.get("pid") or "").strip().lower() == "host":
        err(f"no-host-pid-{svc_name}")
    priv = sc.get("privileged")
    if priv is True or (isinstance(priv, str) and priv.strip().lower() in ("true", "1", "yes", "on")):
        err(f"no-privileged-{svc_name}")

def check_ports(svc_name, sc, expect_target):
    ports = sc.get("ports") or []
    if not ports:
        err(f"publish-required-{svc_name}")
        return
    for p in ports:
        if not isinstance(p, dict):
            err(f"publish-object-{svc_name}")
            continue
        hip = p.get("host_ip")
        if hip != "127.0.0.1":
            err(f"publish-loopback-{svc_name}")
        published = p.get("published")
        if published not in (None, "", 0, "0"):
            err(f"publish-ephemeral-{svc_name}")
        target = p.get("target")
        try:
            if int(target) != int(expect_target):
                err(f"publish-target-port-{svc_name}")
        except (TypeError, ValueError):
            err(f"publish-target-port-{svc_name}")

def check_labels(svc_name, sc, endpoint, public_port, target_port=None):
    labels = labels_map(sc)
    enable = str(labels.get("stacklane.enable", ""))
    if enable not in ("true", "1"):
        err(f"label-enable-{svc_name}")
    if str(labels.get("stacklane.project", "")) != project_slug:
        err(f"label-project-{svc_name}")
    if str(labels.get("stacklane.instance", "")) != expect_instance:
        err(f"label-instance-{svc_name}")
    if str(labels.get("stacklane.endpoint", "")) != endpoint:
        err(f"label-endpoint-{svc_name}")
    if str(labels.get("stacklane.port", "")) != str(public_port):
        err(f"label-port-{svc_name}")
    got_target = labels.get("stacklane.target_port")
    if target_port is None:
        if got_target not in (None, "", str(public_port)):
            err(f"label-target-port-{svc_name}")
    elif str(got_target) != str(target_port):
        err(f"label-target-port-{svc_name}")
    proto = labels.get("stacklane.protocol")
    if proto not in (None, "", "tcp"):
        err(f"label-protocol-{svc_name}")
    return labels

specs = (
    ("postgres", 5432, "postgres", None),
    ("cored", 8080, "api", None),
    ("coreworker", 8081, "worker", None),
)
all_labels = {}
all_env = {}
for svc_name, target, endpoint, target_port in specs:
    sc = services.get(svc_name) or {}
    check_isolation(svc_name, sc)
    check_ports(svc_name, sc, target)
    all_labels[svc_name] = check_labels(svc_name, sc, endpoint, target, target_port)
    hc = sc.get("healthcheck") or {}
    test = hc.get("test") or []
    test_s = test if isinstance(test, str) else " ".join(str(x) for x in test)
    if svc_name == "postgres":
        if "pg_isready" not in test_s:
            err("healthcheck-postgres")
    else:
        if "/readyz" not in test_s or str(target) not in test_s:
            err(f"healthcheck-{svc_name}")
    all_env[svc_name] = env_map(sc)

cored = services.get("cored") or {}
worker = services.get("coreworker") or {}
if not has_bind(cored, "/src"):
    err("source-bind-mount-cored")
if not has_bind(worker, "/src"):
    err("source-bind-mount-coreworker")
if not has_named(cored, "ultracore_go_mod_cache"):
    err("named-volume-mount-cored-mod")
if not has_named(cored, "ultracore_go_build_cache"):
    err("named-volume-mount-cored-build")
if not has_named(cored, "ultracore_cored_air_tmp"):
    err("named-volume-mount-cored-tmp")
if not has_named(worker, "ultracore_coreworker_air_tmp"):
    err("named-volume-mount-coreworker-tmp")
if not has_named(services.get("postgres") or {}, "ultracore_pgdata"):
    err("named-volume-mount-postgres")

cored_env = all_env.get("cored") or {}
worker_env = all_env.get("coreworker") or {}
db = cored_env.get("DATABASE_URL", "")
if "postgres:5432" not in db or "127.0.0.1" in db or "localhost" in db:
    err("internal-dns-database-url")
if worker_env.get("DATABASE_URL", "") != db:
    err("internal-dns-worker-database-url")
if cored_env.get("CORE_ADDR") != ":8080":
    err("cored-listen-addr")
if worker_env.get("CORE_ADDR") != ":8081":
    err("worker-listen-addr")

blob = json.dumps({"labels": all_labels, "env": all_env, "name": name})
if re.search(r"\.local\b", blob):
    err("no-local-domain")

if errors:
    for e in errors:
        print(f"FAIL: {e}", file=sys.stderr)
    sys.exit(1)
print(f"ok: rendered-contract instance={expect_instance} project={expect_project}", file=sys.stderr)
sys.exit(0)
PY
  then
    echo "FAIL: rendered-contract" >&2
    exit 1
  fi

  if ! python3 - "$COMPOSE_FILE" "$tmpdir" "$ROOT" <<'PY'
import json, os, pathlib, subprocess, sys

base_path = pathlib.Path(sys.argv[1])
tmpdir = pathlib.Path(sys.argv[2])
root = pathlib.Path(sys.argv[3])
src = base_path.read_text()

def write_mut(name, text):
    p = tmpdir / f"mut-{name}.yml"
    p.write_text(text)
    return p

mutations = [
    ("wildcard-host", src.replace('"127.0.0.1::8080"', '"0.0.0.0::8080"', 1), "publish-loopback-cored"),
    ("fixed-host-port", src.replace('"127.0.0.1::8080"', '"127.0.0.1:8080:8080"', 1), "publish-ephemeral-cored"),
    ("missing-enable-label", src.replace('\n      stacklane.enable: "true"\n', "\n", 1), "label-enable"),
]

failures = 0
for name, text, _expect in mutations:
    mut_path = write_mut(name, text)
    out = tmpdir / f"mut-{name}.json"
    errf = tmpdir / f"mut-{name}.err"
    env = os.environ.copy()
    env["STACKLANE_INSTANCE"] = "mutprobe"
    env["STACKLANE_BASE_DOMAIN"] = env.get("STACKLANE_BASE_DOMAIN", "test")
    env["COMPOSE_PROJECT_NAME"] = "ultracore-mutprobe"
    try:
        proc = subprocess.run(
            [
                "docker", "compose", "-p", "ultracore-mutprobe",
                "--project-directory", str(root),
                "-f", str(mut_path),
                "config", "--format", "json",
            ],
            check=False,
            env=env,
            stdout=out.open("w"),
            stderr=errf.open("w"),
            timeout=60,
        )
    except Exception:
        print(f"ok: mutation-{name}-config-rejected", file=sys.stderr)
        continue
    if proc.returncode != 0:
        print(f"ok: mutation-{name}-config-rejected", file=sys.stderr)
        continue
    try:
        cfg = json.loads(out.read_text())
    except Exception:
        print(f"ok: mutation-{name}-invalid-json", file=sys.stderr)
        continue
    services = cfg.get("services") or {}
    cored = services.get("cored") or {}
    postgres = services.get("postgres") or {}
    bad = False
    if name == "wildcard-host":
        for p in cored.get("ports") or []:
            if isinstance(p, dict) and p.get("host_ip") != "127.0.0.1":
                bad = True
    elif name == "fixed-host-port":
        for p in cored.get("ports") or []:
            if isinstance(p, dict) and p.get("published") not in (None, "", 0, "0"):
                bad = True
    elif name == "missing-enable-label":
        labels = postgres.get("labels") or {}
        if isinstance(labels, list):
            kv = {}
            for item in labels:
                if isinstance(item, str) and "=" in item:
                    k, v = item.split("=", 1)
                    kv[k] = v
            labels = kv
        if str(labels.get("stacklane.enable", "")) not in ("true", "1"):
            bad = True
    if not bad:
        print(f"FAIL: mutation-{name}", file=sys.stderr)
        failures += 1
    else:
        print(f"ok: mutation-{name}", file=sys.stderr)

if failures:
    sys.exit(2)
sys.exit(0)
PY
  then
    echo "FAIL: mutation-probes" >&2
    exit 1
  fi

  info "ok: check"
  )
}

cmd_up() {
  require_docker
  export_stack_env
  cmd_check
  info "building images (project=${COMPOSE_PROJECT} instance=${INSTANCE})…"
  compose build
  info "starting stack…"
  compose up -d --remove-orphans
  wait_healthy 420
  print_endpoints
}

cmd_status() {
  require_docker
  export_stack_env
  compose ps
  echo
  print_endpoints
}

cmd_logs() {
  require_docker
  export_stack_env
  # Ctrl-C stops following only; it does not tear the stack down.
  if [[ $# -eq 0 ]]; then
    compose logs -f
  else
    compose logs "$@"
  fi
}

cmd_down() {
  require_docker
  export_stack_env
  info "stopping stack (volumes preserved; never uses -v)…"
  compose down --remove-orphans
}

cmd_destroy() {
  export_stack_env
  local expect="${COMPOSE_PROJECT}-destroy"
  if [[ "${CONFIRM:-}" != "$expect" ]]; then
    die "refusing destroy: set CONFIRM=${expect} to remove volumes for project ${COMPOSE_PROJECT}"
  fi
  require_docker
  info "destroying stack AND volumes for ${COMPOSE_PROJECT}…"
  compose down -v --remove-orphans
}

cmd_endpoints() {
  require_docker
  export_stack_env
  print_endpoints
}

usage() {
  cat <<'EOF'
Usage: scripts/compose-dev.sh <command>

Commands:
  check       Fail-closed Stacklane/compose contract validation
  up          check + build + up -d + wait healthy + print endpoints
  status      compose ps + endpoint table
  endpoints   print FQDNs + direct loopback mappings
  logs        follow compose logs (Ctrl-C leaves the stack running)
  down        compose down (never -v; volumes preserved)
  destroy     compose down -v (requires CONFIRM=<compose-project>-destroy)

Environment:
  STACKLANE_INSTANCE     override instance slug (else worktree dirname / branch / dev)
  STACKLANE_BASE_DOMAIN  FQDN base (default: host daemon base_domain, else test)
  CONFIRM                required for destroy; must equal ultracore-<instance>-destroy

Notes:
  - Host fallback is unchanged: `task dev` / scripts/dev-stack.sh.
  - Legacy `docker compose up` still uses docker-compose.yml (fixed 5432/8080/8081).
  - This Stacklane path uses docker-compose.stacklane.yml + ephemeral 127.0.0.1 publishes.
  - Stacklane daemon is optional; direct 127.0.0.1 ephemeral ports always work.
  - Compose project is always ultracore-<instance> via `docker compose -p`.
EOF
}

main() {
  local cmd="${1:-}"
  shift || true
  case "$cmd" in
    check) cmd_check "$@" ;;
    up) cmd_up "$@" ;;
    status) cmd_status "$@" ;;
    endpoints) cmd_endpoints "$@" ;;
    logs) cmd_logs "$@" ;;
    down) cmd_down "$@" ;;
    destroy) cmd_destroy "$@" ;;
    -h|--help|help|"") usage; [[ -n "$cmd" ]] || exit 1 ;;
    *) die "unknown command: $cmd (try --help)" ;;
  esac
}

main "$@"
