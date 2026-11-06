#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
errors=0

# Check AGENTS.md line count. The file is hand-written, so the limit applies to
# the whole file.
agents_lines=$(wc -l < "$REPO_ROOT/AGENTS.md")
agents_scope="file"
if [ "$agents_lines" -gt 300 ]; then
  echo "ERROR: AGENTS.md $agents_scope is $agents_lines lines (max 300)"
  errors=$((errors + 1))
else
  echo "OK: AGENTS.md $agents_scope is $agents_lines lines"
fi

# Check relative markdown links resolve to existing files. Reports every broken
# link in the file and returns non-zero if any were found. Reads grep output via
# process substitution (not a pipe) so the loop runs in this shell — a piped
# `while` would run in a subshell and swallow the failure flag.
check_links() {
  local file="$1"
  local dir rc=0 link target
  dir=$(dirname "$file")

  while IFS= read -r link; do
    # Skip external URLs, anchors, and mailto
    case "$link" in
      http://*|https://*|mailto:*|\#*) continue ;;
    esac

    # Strip anchor from link
    target="${link%%#*}"
    [ -z "$target" ] && continue

    # Resolve relative to the file's directory
    if [ ! -e "$dir/$target" ]; then
      echo "BROKEN LINK: $file -> $link (resolved: $dir/$target)"
      rc=1
    fi
  done < <(grep -oP '\[.*?\]\(\K[^)]+' "$file" 2>/dev/null || true)

  return "$rc"
}

echo ""
echo "Checking markdown cross-links..."
# Process substitution (not a pipe) keeps the loop in this shell so `errors`
# increments survive — otherwise broken links are printed but never fail the run.
while read -r mdfile; do
  if ! check_links "$mdfile"; then
    errors=$((errors + 1))
  fi
done < <(find "$REPO_ROOT" -name '*.md' -not -path '*/node_modules/*' -not -path '*/vendor/*' -not -path '*/.go/*' -not -path '*/.claude/*' | sort)

if [ "$errors" -gt 0 ]; then
  echo ""
  echo "FAILED: $errors doc issue(s) found"
  exit 1
fi

echo ""
echo "All doc checks passed"
