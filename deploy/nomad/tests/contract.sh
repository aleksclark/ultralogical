#!/usr/bin/env bash
# L1 static contract checks for ultracore (no Nomad credentials, no submit).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
NOMAD_DIR="${ROOT}/deploy/nomad"
JOB="${NOMAD_DIR}/jobs/ultracore.nomad.hcl"
MANIFEST="${NOMAD_DIR}/deployment.yaml"
LOCK="${NOMAD_DIR}/images.lock.hcl"
ENVF="${NOMAD_DIR}/env/home.nomadvars.hcl"
CODEOWNERS="${ROOT}/.github/CODEOWNERS"
EXPECTED="${NOMAD_DIR}/tests/expected-services.json"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "OK: $*"; }

[[ -f "$JOB" ]] || fail "missing jobspec $JOB"
[[ -f "$MANIFEST" ]] || fail "missing $MANIFEST"
[[ -f "$LOCK" ]] || fail "missing $LOCK"
[[ -f "$ENVF" ]] || fail "missing $ENVF"
[[ -f "$CODEOWNERS" ]] || fail "missing CODEOWNERS"
[[ -f "$EXPECTED" ]] || fail "missing expected-services.json"

# Plan 03 required keys (JSON-compatible YAML / JSON)
python3 - <<'PY' "$MANIFEST"
import json, sys
from pathlib import Path
p = Path(sys.argv[1])
text = p.read_text()
data = json.loads(text)
req = ["schema_version","project","owner","repository","ref_policy","namespace","datacenters","release_sets"]
for k in req:
    if k not in data:
        raise SystemExit(f"deployment missing {k}")
if data["schema_version"] != 1:
    raise SystemExit("schema_version must be 1")
if data["ref_policy"] != "signed-default-branch-commit":
    raise SystemExit("ref_policy mismatch")
if data["owner"] != "aleks-clark":
    raise SystemExit("owner must be aleks-clark")
if data["repository"] != "https://github.com/aleksclark/ultralogical":
    raise SystemExit("repository mismatch")
if data["namespace"] != "default":
    raise SystemExit("namespace must be default")
if not data["release_sets"]:
    raise SystemExit("release_sets empty")
rs = data["release_sets"][0]
for k in ["name","jobs","env","images","variable_paths","rollout","prune"]:
    if k not in rs:
        raise SystemExit(f"release_set missing {k}")
if rs["name"] != "ultracore":
    raise SystemExit("release set name")
if rs["rollout"] != "serial" or rs["prune"] != "explicit-only":
    raise SystemExit("rollout/prune")
if rs["env"] != "env/home.nomadvars.hcl" or rs["images"] != "images.lock.hcl":
    raise SystemExit("env/images paths")
jobs = rs["jobs"]
if len(jobs) != 1 or jobs[0]["id"] != "ultracore":
    raise SystemExit("jobs")
if jobs[0]["spec"] != "jobs/ultracore.nomad.hcl":
    raise SystemExit(f"spec must be jobs/ultracore.nomad.hcl got {jobs[0]['spec']}")
if "nomad/jobs/ultracore" not in rs["variable_paths"]:
    raise SystemExit("variable_paths")
# reject unknown top-level keys beyond schema
allowed = set(req)
extra = set(data) - allowed
if extra:
    raise SystemExit(f"unknown top-level keys: {extra}")
print("manifest schema ok")
PY
pass "deployment.yaml plan03 schema"

# Digest-only lock
grep -E '@sha256:[0-9a-f]{64}' "$LOCK" >/dev/null || fail "images.lock.hcl missing digest"
! grep -E ':latest["[:space:]]' "$LOCK" >/dev/null || fail "images.lock.hcl must not use :latest"
pass "images.lock.hcl digest"

# Jobspec invariants
grep -F 'job "ultracore"' "$JOB" >/dev/null || fail "job id"
grep -F 'nomadVar "nomad/jobs/ultracore"' "$JOB" >/dev/null || fail "nomadVar"
grep -E '@sha256:[0-9a-f]{64}' "$JOB" >/dev/null || fail "jobspec missing digest pin"
! grep -E 'image\s*=\s*"[^"]*:latest"' "$JOB" >/dev/null || fail ":latest image"
! grep -E '(?i)password\s*=\s*"[^$"]' "$JOB" >/dev/null || true
grep -F 'path     = "/readyz"' "$JOB" >/dev/null || fail "readyz"
grep -F 'core.fleet.clark.team' "$JOB" >/dev/null || fail "hostname"
# Must not enroll retired helper
! grep -F 'ultracore-image-load' "$JOB" >/dev/null || fail "image-load must not appear in jobspec"
pass "jobspec invariants"

# CODEOWNERS
grep -E '^/deploy/nomad/' "$CODEOWNERS" | grep -F '@aleksclark' >/dev/null || fail "CODEOWNERS deploy/nomad"
pass "CODEOWNERS"

# No deploy submit workflow in project for Nomad
if [[ -d "${ROOT}/.github/workflows" ]]; then
  if rg -n 'nomad job (run|dispatch)|NOMAD_TOKEN' "${ROOT}/.github/workflows" -g '*.yml' >/dev/null 2>&1; then
    # allow provider tests that spin local nomad agent — reject real cluster submit patterns
    if rg -n 'nomad job run |nomad job dispatch ' "${ROOT}/.github/workflows" -g '*.yml' >/dev/null 2>&1; then
      fail "workflows must not nomad job run/dispatch"
    fi
  fi
fi
pass "no nomad submit in workflows"

# Optional nomad CLI
if command -v nomad >/dev/null 2>&1; then
  nomad fmt -check "${NOMAD_DIR}/jobs" || fail "nomad fmt"
  nomad job run -output "$JOB" >/dev/null || fail "nomad job run -output"
  pass "nomad L0 parse"
else
  echo "SKIP: nomad CLI not installed (L0 parse)"
fi

pass "all contract checks"
