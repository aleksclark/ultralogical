#!/usr/bin/env bash
# Fail if admin API symbols leak into the consumer surface.
set -euo pipefail
cd "$(dirname "$0")/.."

fail=0

if [ -d clients/ts/src/gen/admin ]; then
  echo "admin isolation: clients/ts/src/gen/admin must not exist" >&2
  fail=1
fi

if grep -RIn --include='*.ts' --include='*.tsx' -E 'admin/v1|admin\.v1|AdminReadService' clients/ts/src 2>/dev/null | grep -v node_modules; then
  echo "admin isolation: admin symbols referenced from public TS client" >&2
  fail=1
fi

if git grep -nE 'aleksclark/ultracore/admin(http|/store|/query)?|/gen/go/admin' -- 'cmd/cored' 'http' 'sdk' 'clients/ts' 2>/dev/null; then
  echo "admin isolation: consumer packages import admin" >&2
  fail=1
fi

if git grep -nE 'admin\.v1|AdminRead' -- 'proto/core' 2>/dev/null; then
  echo "admin isolation: admin mentioned in core protos" >&2
  fail=1
fi

# Binary dependency fence: cored must not link admin packages (A5.1).
# AdminStore lives in admin/store so postgres (shared with cored) stays free of
# admin protos and query engine.
if command -v go >/dev/null 2>&1; then
  deps="$(go list -deps ./cmd/cored 2>/dev/null || true)"
  if printf '%s\n' "$deps" | grep -E 'ultracore/(admin(/|$)|gen/go/admin)' >/dev/null 2>&1; then
    echo "admin isolation: cored package deps include admin surface:" >&2
    printf '%s\n' "$deps" | grep -E 'ultracore/(admin(/|$)|gen/go/admin)' >&2 || true
    fail=1
  fi
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "admin isolation: ok"
