#!/usr/bin/env bash
# Verifies relative markdown links from docs/ and README.md point at existing paths.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
fail=0
while IFS= read -r -d '' file; do
  dir="$(dirname "$file")"
  while IFS= read -r link; do
    [[ -z "$link" ]] && continue
    [[ "$link" == http:* || "$link" == https:* || "$link" == mailto:* || "$link" == \#* ]] && continue
    target="${link%%#*}"
    [[ -z "$target" ]] && continue
    if [[ ! -e "$dir/$target" ]]; then
      echo "$file: missing link target $link (resolved $dir/$target)" >&2
      fail=1
    fi
  done < <(grep -oE '\]\([^)]+\)' "$file" | sed -E 's/^\]\(|\)$//g' | grep -Ev '^(https?|mailto|#)' || true)
done < <(find docs README.md AGENTS.md -name '*.md' -print0 2>/dev/null)

exit "$fail"
