#!/usr/bin/env python3
"""Verify the Rust client's generated code cannot drift from the protos.

Rust output is produced by clients/rust/build.rs at compile time rather than
committed, so its drift risk differs from Go and TypeScript: a proto file the
build never compiles silently disappears from the Rust API surface, and a
generated tree left over from an older proto set keeps compiling.

Two checks:

1. Every checked-in proto file is compiled by clients/rust/build.rs (and every
   file build.rs names still exists).
2. If a generated ultra.v1.rs is present (i.e. the crate has been built), it
   contains a Rust item for every proto message and service.

Usage: verify-codegen-rust.py [--require-generated]
"""

import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parents[1]
build_rs = root / "clients/rust/build.rs"
proto_dir = root / "proto/ultra/v1"
require_generated = "--require-generated" in sys.argv[1:]

errors = []

text = build_rs.read_text()
listed = set(re.findall(r'"\.\./\.\./proto/(ultra/v1/[A-Za-z0-9_]+\.proto)"', text))
present = {f"ultra/v1/{p.name}" for p in sorted(proto_dir.glob("*.proto"))}

for missing in sorted(present - listed):
    errors.append(f"{missing} is checked in but never compiled by clients/rust/build.rs")
for stale in sorted(listed - present):
    errors.append(f"{stale} is compiled by clients/rust/build.rs but no longer exists")


def snake(name: str) -> str:
    return re.sub(r"(?<!^)(?=[A-Z])", "_", name).lower()


messages: set[str] = set()
services: set[str] = set()
for proto in sorted(proto_dir.glob("*.proto")):
    body = proto.read_text()
    messages.update(re.findall(r"^\s*message\s+([A-Za-z0-9_]+)", body, re.MULTILINE))
    services.update(re.findall(r"^\s*service\s+([A-Za-z0-9_]+)", body, re.MULTILINE))

generated = sorted(root.glob("**/build/*/out/ultra.v1.rs"), key=lambda p: p.stat().st_mtime)
if not generated:
    if require_generated:
        errors.append(
            "no generated ultra.v1.rs found; build the Rust client before verifying output"
        )
else:
    out = generated[-1].read_text()
    for message in sorted(messages):
        if not re.search(rf"\bstruct {re.escape(message)}\b", out):
            errors.append(f"generated Rust output has no struct for message {message}")
    for service in sorted(services):
        module = snake(service) + "_client"
        if module not in out:
            errors.append(f"generated Rust output has no client module for service {service}")

if errors:
    print("\n".join(errors))
    sys.exit(1)
print(
    f"rust codegen covers {len(present)} proto files, "
    f"{len(messages)} messages, {len(services)} services"
)
