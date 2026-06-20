#!/usr/bin/env bash
#
# rebase-merge-branches.sh — merge a batch of per-issue feature branches
# into main in least-conflict-first order, pausing for manual conflict
# resolution. The merge step of the autonomous batch loop; see
# docs/developer/batch-loop.md.
#
#   * Discovers local `*/<key>-NNN-*` branches not yet in main (any issue
#     key: dil-, bry-, etc). Override the pattern with $BRANCH_GLOB.
#   * Orders them so branches whose changed files DON'T overlap other
#     pending branches go first (truly conflict-free); the ones sharing a
#     file go last, after main has settled.
#   * On a merge conflict the script PAUSES (leaves the rebase in
#     progress and exits 2). Resolve it — or ask the agent to — then:
#         git add <files> && git rebase --continue   # repeat til done
#     and RE-RUN this script: it fast-forwards main, deletes the
#     just-finished branch, and carries on with the rest. Idempotent.
#   * Nothing is pushed. `git push origin main` is left to you.
set -uo pipefail   # deliberately not -e; errors are handled explicitly

cd "$(git rev-parse --show-toplevel)"

# Branch discovery pattern: issue-key + number, e.g. ryanjamesdillon/bry-12-...
BRANCH_GLOB="${BRANCH_GLOB:-/[a-z][a-z]*-[0-9][0-9]*}"

# --- refuse if a rebase is mid-flight --------------------------------
if [ -d "$(git rev-parse --git-path rebase-merge)" ] || [ -d "$(git rev-parse --git-path rebase-apply)" ]; then
  echo "✗ A rebase is already in progress. Finish it (git rebase --continue / --abort), then re-run." >&2
  exit 1
fi

git switch main 2>/dev/null || git checkout main

# Allow intentional local-only files (tfvars, secrets, scratch); refuse
# anything else so a stray edit can't get folded into a fast-forward.
if git status --porcelain \
     | grep -vE 'terraform/terraform\.tfvars\.json|board-mailbox-passwords\.json|\.bak[0-9]*$|^\?\? .*\.md$' \
     | grep -q .; then
  echo "✗ Unexpected uncommitted changes — commit/stash first:" >&2
  git status --short >&2
  exit 1
fi

# --- discover pending branches (local issue branches not in main) ----
pending=()
while IFS= read -r b; do
  git merge-base --is-ancestor "$b" main 2>/dev/null || pending+=("$b")
done < <(git for-each-ref --format='%(refname:short)' refs/heads/ | grep -E "$BRANCH_GLOB")

if [ ${#pending[@]} -eq 0 ]; then
  echo "✓ Nothing to do — no pending issue branches."
  exit 0
fi

# --- order by least file overlap with the other pending branches ----
declare -A FILES
for b in "${pending[@]}"; do
  FILES[$b]="$(git diff --name-only "main..$b")"
done
shared="$(for b in "${pending[@]}"; do printf '%s\n' "${FILES[$b]}"; done | sort | uniq -d)"

rank="$(mktemp)"
for b in "${pending[@]}"; do
  overlap=$(comm -12 <(printf '%s\n' "${FILES[$b]}" | sort -u) <(printf '%s\n' "$shared" | sort -u) | grep -c . || true)
  total=$(printf '%s\n' "${FILES[$b]}" | grep -c . || true)
  printf '%d\t%d\t%s\n' "$overlap" "$total" "$b" >> "$rank"
done
ordered=()
while IFS= read -r b; do ordered+=("$b"); done < <(sort -n -k1,1 -k2,2 "$rank" | cut -f3)
rm -f "$rank"

echo "Merge order (least-overlap first):"
printf '  %s\n' "${ordered[@]}"
echo

# --- rebase → ff-merge → delete, one at a time ----------------------
for b in "${ordered[@]}"; do
  echo "▶ rebasing $b onto main…"
  if git rebase --autostash main "$b"; then
    git switch main
    git merge --ff-only "$b"
    git branch -d "$b"
    echo "✓ $b merged into main; branch deleted"
    echo
  else
    cat >&2 <<EOF

⏸  PAUSED — merge conflict rebasing $b.
   The rebase is in progress; conflict markers are in the working tree.
   1. Resolve the conflicts (or ask the agent to).
   2. git add <files> && git rebase --continue   # repeat until it finishes
   3. Re-run this script — it ff-merges main, deletes $b, and continues.
   To bail on this branch only:  git rebase --abort   (then re-run to skip it)
EOF
    exit 2
  fi
done

echo "✓ All pending branches merged into main and deleted. main is now:"
git log --oneline -10 | cat
echo
echo "Remember to push when ready:  git push origin main"
