#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
errors=0

# Check CLAUDE.md line count
claude_lines=$(wc -l < "$REPO_ROOT/CLAUDE.md")
if [ "$claude_lines" -gt 150 ]; then
  echo "ERROR: CLAUDE.md is $claude_lines lines (max 150)"
  errors=$((errors + 1))
else
  echo "OK: CLAUDE.md is $claude_lines lines"
fi

# Check relative markdown links resolve to existing files
check_links() {
  local file="$1"
  local dir
  dir=$(dirname "$file")

  grep -oP '\[.*?\]\(\K[^)]+' "$file" 2>/dev/null | while read -r link; do
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
      return 1
    fi
  done
}

echo ""
echo "Checking markdown cross-links..."
find "$REPO_ROOT" -name '*.md' -not -path '*/node_modules/*' -not -path '*/.go/*' -not -path '*/.claude/*' | sort | while read -r mdfile; do
  rel="${mdfile#$REPO_ROOT/}"
  if ! check_links "$mdfile"; then
    errors=$((errors + 1))
  fi
done

if [ "$errors" -gt 0 ]; then
  echo ""
  echo "FAILED: $errors doc issue(s) found"
  exit 1
fi

echo ""
echo "All doc checks passed"
