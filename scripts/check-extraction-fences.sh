#!/usr/bin/env bash
# Fail when banned extraction terms reappear in live code.
#
# Each closed extraction phase may append banned terms to
# agent_docs/core_extraction_plan/fences/eN.txt (one regex fragment per line,
# blank lines and # comments ignored). Closing a phase is therefore the same
# act as making its deletions CI-enforced.
#
# Scanned surfaces: Go, proto, SQL, TypeScript. Docs, plan history, generated
# code under gen/, and the fences directory itself are excluded so historical
# phase write-ups can still name what was deleted.
#
# Exit 0 when no fences are active or no matches are found.
# Exit 1 when any banned term appears in a scanned path.
set -euo pipefail

cd "$(dirname "$0")/.."

fences_dir="agent_docs/core_extraction_plan/fences"
if [ ! -d "$fences_dir" ]; then
  echo "extraction fences: no fences directory; nothing to enforce"
  exit 0
fi

# Collect non-empty, non-comment lines from every eN.txt allowlist/ban file.
terms=()
while IFS= read -r -d '' file; do
  while IFS= read -r line || [ -n "$line" ]; do
    # Trim leading/trailing whitespace.
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    case "$line" in
      ''|\#*) continue ;;
    esac
    terms+=("$line")
  done <"$file"
done < <(find "$fences_dir" -maxdepth 1 -type f -name 'e*.txt' -print0 | sort -z)

if [ "${#terms[@]}" -eq 0 ]; then
  echo "extraction fences: no active terms (E0 baseline); ok"
  exit 0
fi

# Join terms into one alternation. Individual lines are already regex fragments.
banned=""
for t in "${terms[@]}"; do
  if [ -z "$banned" ]; then
    banned="$t"
  else
    banned="$banned|$t"
  fi
done

echo "extraction fences: scanning for: $banned"

# git grep exits 1 when there are no matches, 0 when there are. We invert that:
# matches are the failure mode.
set +e
matches=$(git grep -inE "$banned" -- \
  '*.go' '*.proto' '*.sql' '*.ts' '*.tsx' \
  ':!agent_docs' \
  ':!docs' \
  ':!plan' \
  ':!gen' \
  ':!**/node_modules/**' \
  2>/dev/null)
rc=$?
set -e

if [ "$rc" -eq 0 ] && [ -n "$matches" ]; then
  echo "extraction fence violated; banned terms still present in live code:" >&2
  echo "$matches" >&2
  exit 1
fi

if [ "$rc" -gt 1 ]; then
  echo "extraction fences: git grep failed with exit $rc" >&2
  exit 2
fi

echo "extraction fences: clean"
exit 0
