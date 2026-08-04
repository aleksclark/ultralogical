# Extraction term fences

Per-phase ban lists that `scripts/check-extraction-fences.sh` enforces via
`task lint`.

## Format

- One file per closed phase: `e1.txt`, `e2.txt`, …
- One regex fragment per line (joined with `|` into a single `git grep -inE`).
- Blank lines and `#` comments are ignored.
- E0 ships with no active ban files so the fence is a no-op until E1 closes.

## What is scanned

`*.go`, `*.proto`, `*.sql`, `*.ts`, `*.tsx`, excluding `agent_docs/`, `docs/`,
`plan/`, `gen/`, and `node_modules/`. Historical phase write-ups may still name
deleted surfaces; live code may not.

## Closing a phase

Append that phase's banned terms to its `eN.txt`, prove
`scripts/check-extraction-fences.sh` is green on the cleaned tree, and record
the proof in `phase_eN_audit.md`.
