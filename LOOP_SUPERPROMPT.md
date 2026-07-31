# LOOP SUPERPROMPT — 24 hours of continuous, verified self-improvement

Paste this whole file as a single prompt. It is self-contained: assume the agent
receiving it knows nothing about this project, this conversation, or this machine.

---

You are running an unattended 24-hour improvement loop on the SignalDeck
repository. Work continuously until 24 hours have elapsed from your first
action. Do not stop early because things look finished — when the gates are
green there is always a backlog item, and when the backlog empties you write new
items grounded in evidence.

## Repository and environment — verified facts, do not re-litigate

- Repo root: `C:\Users\Nicholas_N\Desktop\claude code\signaldeck` (Windows 11).
- Git: branch `audit/2026-07-27`, remote `origin` = `nyaungnicholas-wq/signaldeck`
  (**private**). Never push to `main`. Never force-push. Never rewrite history.
- Go **1.26.5** is installed and is the only Go on the machine. `daemon/go.mod`
  requires `go 1.25.0`. `GOTOOLCHAIN=auto`, so any older toolchain a go.mod asks
  for is downloaded automatically. **Do not install, downgrade, or change Go.**
- Python 3.12.10 as `python` (there is no `python3` on PATH — the Windows Store
  alias stub answers to that name and silently exits 0).
- Node 24.18.0, npm 11.16.0. The web app is Next.js 16.2.10 in `web/`.
- Shell is PowerShell. `bash` exists at `C:\Program Files\Git\bin\bash.exe` and is
  required for the `ops/*.sh` scripts.
- `data/signaldeck.db` is a live 2.4 GB SQLite database. It is gitignored.

## Prime directive — this repository refuses dishonest results

SignalDeck exists to measure whether its own predictions are any good and to
publish the answer even when the answer is no. Its checks are adversarial **by
design**: they refuse to grade an unregistered grader, refuse to publish a
registry beside an unverifiable research claim, refuse to start an
unattributable build. `PREREGISTRATION.md` makes a hash chain authoritative over
prose.

Therefore, for every single change you make:

1. **Fix the root cause, never the symptom.** Before editing a function, find
   every caller. One guard in the shared path beats a guard in each caller.
2. **Never weaken, delete, skip, loosen, or special-case a check to make it
   pass.** Not a threshold, not an assertion, not a refusal message, not a test.
   If a check is failing, the code is wrong or the claim is wrong — fix that.
   Softening a check to reach green is the single worst thing you can do here
   and it destroys the entire point of the project.
3. **A change is only done when a command proves it.** Model confidence is not
   evidence. Every change must be gated on a command that exited non-zero before
   the change and exits zero after it.
4. **If you cannot fix something honestly, leave it failing and write it up.**
   A documented red gate is worth more than a green one you faked.

## The cycle

Repeat until 24 hours have elapsed. Never exit the loop for any other reason.

### Step 1 — sync
```
cd "C:\Users\Nicholas_N\Desktop\claude code\signaldeck"
git pull --rebase --autostash
```
On conflict, resolve in favour of `origin`, then continue. Never abandon the loop.

### Step 2 — run every gate, record which are red
```
cd daemon;  go build ./...
cd daemon;  go vet ./...
cd daemon;  go test ./...
cd tools;   python -m unittest test_accuracy_registry test_audit_register test_deployment_drift test_schema_contract_check test_research_liveness
cd web;     npx tsc --noEmit
cd web;     npx eslint . --max-warnings 0
cd web;     npx next build
& "C:\Program Files\Git\bin\bash.exe" -lc "cd '/c/users/nicholas_n/desktop/claude code/signaldeck' && bash ops/pre-publish-scan.sh"
```
A gate that crashes, hangs, or errors counts as **red**, not as skipped.

### Step 3 — pick exactly one piece of work
- **Any gate red** → the first red gate is the work. Nothing else matters.
- **All green** → take the first unchecked `## [ ]` item from
  `ops/IMPROVE_BACKLOG.md`. Each carries a `verify:` command that decides it.
- **Backlog empty** → find new work by evidence, not by taste. Read
  `logs/`, the `dq_events` and `worker_runs` tables, `ops/pre-publish-scan.sh`
  output, and `audits/`. Append new items with real `verify:` commands.

### Step 4 — make the smallest correct change
Match surrounding style. No new dependencies. No speculative abstractions. No
refactors nobody asked for.

### Step 5 — verify, then re-verify everything
Run the specific command that must now pass. If it does not pass, **revert your
change** (`git checkout -- .`) and record why it failed. Do not iterate more than
three times on one item; move on and leave a note.

If it passes, re-run **all** gates from Step 2. A fix that repairs its own gate
and breaks another is a net loss and must be reverted.

### Step 6 — commit and push
Only when every gate is green:
```
git add -A
git commit -m "<what changed and why, in plain language>"
git push origin audit/2026-07-27
```
Commit messages state what was broken, what the root cause was, and what command
proves the fix. No filler.

### Step 7 — journal
Append one line per cycle to `logs/loop-journal.md`: cycle number, timestamp,
which gate or item, what you changed, the verification result, and anything you
deliberately left undone.

## Known open problems — real, measured, already triaged

These are in `ops/IMPROVE_BACKLOG.md` with verification commands. Highest value first.

1. **Research-loop liveness.** `worker_runs` narrated 48-rule grid searches on
   2026-07-26 through 07-29, but `research_loop_judgments` and
   `research_loop_runs` hold no rows for those days. This blocks the entire
   accuracy registry from publishing. Either the writer drops judgments or the
   narration overstates what ran. Fix whichever is lying. Do not delete the check.
2. **16,022 of 19,058 `regime_outcomes` rows have a NULL `naive_label`**, so
   `regime-outcome-runner` refuses to run and **not one structural prediction has
   ever been graded** (`correct` and `resolved_at` are NULL on every row). Find
   the write path that produced label-less rows. Backfill only where the naive
   baseline is genuinely recomputable from stored bars; quarantine the rest.
   Never invent a label.
3. **Hardcoded project root.** `daemon/internal/config/config.go` lines 78, 125,
   158 and 166 build paths from `filepath.Join(home, "claude code", ...)`, but the
   repo lives under `$HOME/Desktop/claude code`. The daemon therefore loads **no
   `.env` at all**: the LLM key is ignored, Alpaca keys are never found, and the
   security toggles never apply. Resolve the root from the executable/cwd with a
   `SIGNALDECK_ROOT` override; keep the old path as last fallback so the macOS
   launchd deployment still works.
4. **CRLF breaks the grader digest.** `core.autocrlf=true` stores
   `tools/accuracy_registry.py` LF and checks it out CRLF, changing its SHA-256,
   so a fresh Windows clone gets a grader the prereg chain rejects. Add
   `.gitattributes` marking digest-pinned files `-text`.
5. **Stale pinned digest.** The chain's newest `grading-protocol` record pins the
   pre-encoding-fix grader hash. The registrar appends the amendment
   automatically on a clean build — make that path run rather than appending by hand.
6. **Pollers that report success while delivering nothing.** `congress_trades`
   has 0 rows while `congress-poller` returns `ok`; `edgar-fetcher` and
   `filings-poller` return `ok` with "skipped: no EDGAR client" every run. A
   source that never delivers must report degraded, not ok.

## The quality bar

Aim for the standard a serious quantitative trading firm would apply to its own
research:

- Every published number carries an interval, and the interval is computed on the
  **effective** sample size, not the raw row count. Correlated observations are
  not independent ones.
- Multiple looks at the same data are priced. Re-grading daily and publishing a
  family of rows both inflate false positives; both must widen the interval.
- A baseline is mandatory. "59% accurate" means nothing without the naive rule it
  is meant to beat.
- Negative results ship. A pre-registered failure is evidence; a quietly deleted
  one is fraud.
- Anything not reproducible from committed code plus committed data is not a
  result yet.

## Absolutely forbidden

- Weakening or removing any test, assertion, threshold, guard or refusal.
- Editing `data/`, any `.env`, or anything under `quarantine/`.
- Force-pushing, rewriting history, or pushing to `main`.
- Committing secrets, API keys, or raw provider bars/quotes/news bodies.
- Running the daemon with `SIGNALDECK_ALLOW_DIRTY_BUILD=1` against
  `data/signaldeck.db` — a dirty build writes rows that can never be graded.
- Installing, downgrading or switching the Go toolchain.
- Claiming a fix without the command output that proves it.

## Resilience — this loop does not stop

A failed command, an unreachable network, a git conflict, a hung build, an
unparseable file: log it, revert anything half-applied, and start the next cycle.
The only thing that ends this loop is the 24-hour deadline.

## Report at the end

Write `logs/loop-report.md`: cycles completed, commits made, gates fixed, backlog
items closed with the command that proves each, items attempted and abandoned
with the reason, and the honest list of what is still broken.
