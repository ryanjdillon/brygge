# Autonomous batch loop — processing a backlog fast

Audience: agents (and the humans driving them). Scope: how to take a set
of independent Linear issues and ship them one-per-branch in a single
self-paced session, then merge them all in least-conflict order. This is
the "process N issues without babysitting each one" workflow. For the
single-issue flow, use the normal per-change steps in
[AGENTS.md](../../AGENTS.md) instead.

## When to use it

- You have **several independent, codeable issues** to land in one go
  ("pick the 6 best backlog tasks and ship them", "burn down the
  Accounting project").
- Each issue is self-contained enough to live on its own branch and be
  verified independently.

**Don't** use it for a single issue, for tightly-coupled changes that
belong in one branch, or for work that needs a human decision mid-stream
(see [Block, don't guess](#block-dont-guess)).

## Shape

```
work-queue.md (ordered list)
      │
      ▼
/loop  ──▶ iteration: 1 issue ──▶ branch ──▶ implement ──▶ verify ──▶ commit ──▶ Linear comment ──▶ mark [x]/[blocked] ──▶ back to main
      │         (repeat, one issue per iteration, until all [x] or [blocked])
      ▼
scripts/rebase-merge-branches.sh  ──▶ least-conflict-first merge into main (pauses on conflict)
```

One issue per iteration keeps each turn's context small; the queue file
is the durable state so the loop survives compaction.

## 1. Pick and queue

Choose the issues, then write a queue file **outside the repo** (it's
session scratch, not a deliverable) at
`~/.claude/projects/<project>/work-queue.md`:

```markdown
# Autonomous work queue — <batch name> (created <date>)

Process strictly top-to-bottom, ONE issue per loop iteration. Read each
issue's spec fresh via the linear MCP (`get_issue`).

Status: `[ ]` pending · `[~]` in progress · `[x]` done (branch committed
+ verified) · `[blocked: reason]`.

- [ ] DIL-XXX — short title (one-line scope hint)
- [ ] DIL-YYY — …

## Notes for each iteration
- Branch off main (`git switch main` first). Never commit to main; never push; never close PRs.
- Protected files — never stage: terraform/terraform.tfvars.json(.bak*), board-mailbox-passwords.json, scripts/rebase-merge-branches.sh.
- Verify before committing. Backend: go build + go test (docker postgres on a spare port, `CREATE EXTENSION pgcrypto` BEFORE running — verify it took; go from the Nix flake). Frontend: vue-tsc + vite build + vitest (note the known pre-existing failure baseline). Regenerate api.d.ts (`just api-types`) if endpoints change.
- Commit messages: conventional, atomic, `(DIL-NNN)` in the subject, no AI co-author trailer.
- Update Linear: In Progress at start; on completion add a comment with what landed + branch name; mark the queue item [x] (or [blocked: reason]).
```

Ordering matters: put issues whose changes are likely to **collide** with
each other adjacent (or merge them into one), and put the riskiest last.

## 2. Run the loop

Fire the `/loop` skill in self-paced mode (no interval) with a prompt
that re-derives state from the queue every iteration, so it's resumable
across context compaction:

```
/loop Autonomous queue processing. Read <path>/work-queue.md and pick the FIRST item still `[ ]`.
Work ONLY that one issue this iteration; STOP (don't reschedule) once every item is `[x]` or `[blocked: …]`.
For the chosen issue:
1. Mark it `[~]`. Read the spec via linear `get_issue`. Set the issue In Progress.
2. `git switch main`, then branch `ryanjamesdillon/<key>-NNN-short-slug`.
3. Implement per the spec + the queue's Notes (protected files, verify, commit rules).
4. Verify (build + tests, regen api.d.ts if endpoints changed) BEFORE committing. Commit atomically. Do NOT merge/push/close PRs.
5. Add a Linear comment (what landed + branch name); mark the queue item `[x]` (or `[blocked: reason]`); return to `main`.
Keep ONE issue per iteration so context stays manageable; always return to `main` between iterations.
After the LAST item is resolved, run scripts/rebase-merge-branches.sh and resolve any conflicts it pauses on.
```

Each iteration is self-contained: branch → implement → **verify** →
commit → Linear comment → mark `[x]`/`[blocked]` → `git switch main`.
Leaving every issue on its own branch (no merge, no push) is what lets
the human batch-review at the end.

## 3. Merge

When the loop finishes, [`scripts/rebase-merge-branches.sh`](../../scripts/rebase-merge-branches.sh)
discovers the per-issue branches, orders them least-overlap-first,
rebases each onto main and fast-forwards, and deletes each branch on
success. On a conflict it **pauses** (exit 2) — resolve it, `git rebase
--continue`, then re-run the script; it's idempotent and picks up where
it left off. It never pushes. Set `BRANCH_GLOB` to override the
branch-name pattern.

## Block, don't guess

If an issue turns out to need a decision you can't make from the spec +
the code — a **security-critical** change (auth/session lifetime), an
**infra/ops** task (deploys, DNS, secrets — these are deferred to the
operator anyway), or a genuine **product** call — do **not** implement a
plausible-looking guess. Mark it `[blocked: reason]`, leave a
Linear comment with a concrete implementation plan + the specific
question, and move on. A reviewed plan is a better outcome than a
looks-done change that quietly weakens a security gate.

Likewise, when an issue is partly superseded by work that shipped after
it was filed, scope your change to the **genuine remaining gap** and say
so in the Linear comment — don't re-implement what already exists.

## Conventions this workflow must honor

- **Branch per issue**, off `main`; never commit to `main`, never push,
  never close PRs inside the loop. The merge script does the integration.
- **Verify before every commit** (build + tests; regen `api.d.ts` if
  endpoints change). A red build never gets committed.
- **Commit messages**: conventional + atomic + `(DIL-NNN)`, no AI
  co-author trailer.
- **Linear**: In Progress at start, completion comment + branch name at
  the end, queue marked `[x]`/`[blocked]`. The operator marks the issue
  Done after merge (or follow the auto-close policy in
  [AGENTS.md](../../AGENTS.md) if you're also doing the merge).
- **Protected files** never get staged (tfvars, mailbox passwords, local
  scratch).
