#!/usr/bin/env node
/**
 * CI gates for admin-web:
 * 1. No @ultracore/client or core.v1 imports.
 * 2. List* RPC methods may only be invoked from src/data/collections.ts
 *    (the collection data layer).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, "..");
const srcRoot = path.join(root, "src");

const LIST_RPC_RE =
  /\.(listTenants|listAPIKeys|listSessions|listEvents|listRuns|listRunSteps|listResources|listProviders|listCredentials|listPeriodicPrompts|listMemory|listWaits|listJobs)\s*\(/g;

const FORBIDDEN_IMPORT_RE =
  /@ultracore\/client|from\s+['"][^'"]*core\/v1|gen\/core\/v1|@ultracore\/client/g;

function walk(dir, out = []) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    if (ent.name === "gen" || ent.name === "node_modules" || ent.name === "dist") continue;
    const p = path.join(dir, ent.name);
    if (ent.isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|js|jsx|mjs)$/.test(ent.name)) out.push(p);
  }
  return out;
}

let failed = false;
const files = walk(srcRoot);

for (const file of files) {
  const rel = path.relative(root, file);
  const text = fs.readFileSync(file, "utf8");

  if (FORBIDDEN_IMPORT_RE.test(text)) {
    console.error(`FAIL import gate: forbidden core client / core.v1 reference in ${rel}`);
    failed = true;
  }
  FORBIDDEN_IMPORT_RE.lastIndex = 0;

  // Direct list RPC calls outside data layer
  const isDataLayer =
    rel.replace(/\\/g, "/") === "src/data/collections.ts" ||
    rel.replace(/\\/g, "/").endsWith("/data/collections.ts");

  if (!isDataLayer) {
    const matches = text.match(LIST_RPC_RE);
    if (matches) {
      console.error(
        `FAIL list-RPC gate: ${rel} calls List* outside data layer: ${[...new Set(matches)].join(", ")}`,
      );
      failed = true;
    }
  }
  LIST_RPC_RE.lastIndex = 0;
}

// package.json must not depend on public client
const pkg = JSON.parse(fs.readFileSync(path.join(root, "package.json"), "utf8"));
const deps = { ...pkg.dependencies, ...pkg.devDependencies };
for (const name of Object.keys(deps)) {
  if (name === "@ultracore/client" || name.includes("core-client")) {
    console.error(`FAIL import gate: package.json depends on ${name}`);
    failed = true;
  }
}

if (failed) {
  console.error("admin-web import gates FAILED");
  process.exit(1);
}
console.log("admin-web import gates: ok");
