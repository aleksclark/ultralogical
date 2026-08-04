#!/usr/bin/env bash
# Regenerate Go + public TS + private admin TS from protos.
# Admin symbols must never land in clients/ts (public @ultracore/client).
set -euo pipefail
cd "$(dirname "$0")/.."

buf generate --template buf.gen.yaml
buf generate --template buf.gen.core-ts.yaml --path proto/core
buf generate --template buf.gen.admin.yaml --path proto/admin

if [ -d clients/ts/src/gen/admin ]; then
  echo "generate: admin leaked into clients/ts/src/gen/admin" >&2
  exit 1
fi
if [ -d clients/admin-ts/src/gen/core ]; then
  echo "generate: core leaked into clients/admin-ts/src/gen/core" >&2
  exit 1
fi
