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

if git grep -nE 'aleksclark/ultracore/admin(http)?|/gen/go/admin' -- 'cmd/cored' 'http' 'sdk' 'clients/ts' 2>/dev/null; then
  echo "admin isolation: consumer packages import admin" >&2
  fail=1
fi

if git grep -nE 'admin\.v1|AdminRead' -- 'proto/core' 2>/dev/null; then
  echo "admin isolation: admin mentioned in core protos" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "admin isolation: ok"
