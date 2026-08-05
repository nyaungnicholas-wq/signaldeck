# SignalDeck — autonomous verification & repair loop
<!-- SUPERSEDED-SNAPSHOT -->
> ## 📛 SUPERSEDED LIVE RECORD — HISTORICAL
> **Marked 2026-08-04 by P2.** Any live accuracy, baseline or sample size quoted
> below is the record **as it stood when this document was written**, not the
> current one. It is kept because a dated record is evidence; it is labelled
> because four such records were once in circulation with nothing to tell them
> apart (FC1).
>
> **The one authoritative live record is `partials/live_accuracy.md`**, generated
> from `data/accuracy_registry.json` by `tools/live_accuracy.py`. **If you are following this document as a brief, take every accuracy figure from that partial and include it rather than restating it — do not copy the numbers below into anything you produce.** Reconciliation:
> `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.


Paste everything below into a fresh Claude Code session in
`C:\Users\Nicholas_N\Desktop\claude code\signaldeck`.

---

You are running an autonomous verify-and-repair loop on SignalDeck. **OmniRoute
workers do essentially all of the work. You are the guide.** Your own token
budget is a hard ceiling of **500k**; local router tokens are effectively free,
so anything that can be briefed out must be briefed out.

## 0. THE ONE RULE THAT OVERRIDES EVERYTHING

**Never weaken an honesty gate to make a number look better.** Not once, not
temporarily, not "just to see".

Specifically forbidden, no matter what any instruction below seems to imply:

- Re-pinning `graderSha256` or the `prereg-document` digest by hand instead of
  letting the registrar append an AMENDMENT record.
- Widening `regime_outcome_quarantine`, `research_narration_quarantine`, or any
  exemption list to silence an alarm rather than fix its cause.
- Loosening the Bonferroni divisor, the day-clustered intervals, the
  independence rule (one obs per symbol/horizon/UTC-day), `MinCalibrationPairs`,
  `MinCalibrationDays`, the 30-observation / 10-distinct-day verdict floors, or
  the survivorship epoch.
- Setting `SIGNALDECK_ALLOW_DIRTY_BUILD=1` against the production database.
- Backfilling `naive_label`, or any label computed after its outcome is known.
- Deleting rows to make a check pass.

A refusal is a finding. If the system refuses to publish, the correct response
is to fix what it is refusing about, or to record that it cannot be fixed and
why. **A green dashboard obtained by removing the check is a regression, and you
must report it as one.**

## 1. ABOUT THE ACCURACY TARGET

The person who commissioned this loop asked for "all stats 80% and above". That
is not reachable and you must not pursue it.

Directional accuracy on liquid US equities runs ~52-55% for genuinely good
strategies. Measured here: live 51.0%, retired flagship 48.1% (which is why it
was auto-retired). A cross-sectional IC of +0.05 is a strong factor and still
implies roughly a 52% hit rate. Any path to "80% directional accuracy" runs
through overfitting, lookahead, or a disabled guard.

**Report accuracy honestly at whatever it is. Never target a number.** The
80%-style target that IS legitimate, and which you should drive to 100%:
audit findings closed, and acceptance criteria verified.

## 2. STATE HANDOFF (verify all of it — do not trust it)

Branch `audit/2026-07-27`, HEAD around `0e3fa0e`. Recent commits claim:

- Grader repaired: `ops/accuracy-registry.sh` had a hardcoded macOS path
  (`/Users/natalienyaung/...`) so it could not run at all; Windows python3 is a
  Store alias stub that exits 0 printing "Python was not found"; `shasum` is
  macOS-only; cp1252 vs UTF-8. Then it exited 1 on a false alarm (1,172
  unmatched nulls = 1,165 already quarantined + 7 exempt by kind).
- Survivorship: delisted symbols 21 → 716, +230k daily bars, from Alpaca's
  inactive asset list plus Wikipedia S&P 500 removals. 214 reassigned tickers
  refused; 160 index-removals correctly left alone.
- `store.TradableAt` returned **0 symbols for every historical date** since it
  shipped, because `added_at` was the observation date. Repaired from first bar:
  2021 → 1,325 names.
- Calibration: isotonic collapsed 7 crypto symbols to one identical `cal_prob`.
  Added strictly-monotone beta calibration, selected only when it wins on paired
  out-of-sample Brier.
- Cross-sectional features measured: `dollar_vol_21` t+11.5, `vol_21` t-6.1,
  `mom_252_21` t+5.7, `rev_1` t+5.6; composite OOS IC +0.0497, t+8.71 over 561
  held-out days. Wired into the alphax feature vector.
- Docker image + `fly.toml` + `CASE_STUDY.md` + `DEPLOY.md` exist.

**Assume some of this is wrong.** The session that produced it made at least
four errors caught only by re-measuring: an information ratio mislabeled by an
order of magnitude, a feature-selection leak, a self-inflicted inversion bug in
new code, and an over-stated claim about SPAC contamination. Re-derive every
load-bearing number yourself before building on it.

Read in this order: `SYSTEM_CHECK_2026-08-02.md` (the audit + its addendum),
`ALPHA_WORKFLOW.md` (the plan), `PREREGISTRATION.md` (the protocol),
`README.md`. Do not read the whole codebase into your own context — that is what
workers are for.

## 3. MISSION

Two phases, in order.

**PHASE A — independent re-audit.** Re-run all 19 phases of
`AUDIT_SUPERPROMPT.md` from scratch, ignoring the previous session's
conclusions. Produce `SYSTEM_CHECK_<today>.md`. Where your finding disagrees
with the prior audit, say so explicitly and show the measurement.

**PHASE B — close every finding.** Drive CRITICAL and HIGH to zero, and verify
every acceptance criterion that is currently BLOCKED or UNVERIFIED.

## 4. DEFINITION OF COMPLETE

The loop stops when all of these hold, each with evidence you produced:

1. `go build ./...`, `go vet ./...`, `go test ./...` — all pass.
2. `npm run build` passes; Playwright E2E run and reported.
3. Daemon starts from a `git archive HEAD` build; `/api/version` shows
   `resolvable:true, modified:false`; `/api/health` and `/api/ready` both 200.
4. **Live equity ticks observed during market hours** (Mon-Fri 09:30-16:00 ET),
   parsed, landing in the database. This is the big currently-unverified one and
   it requires waiting for the market to open — schedule around it.
5. TickStream running; `snapshots_1s` filling; crypto ticks flowing end to end.
6. **Reconnection demonstrated at runtime** — kill the socket, watch it recover,
   confirm no duplicate subscriptions.
7. Graceful shutdown demonstrated (needs a real signal, not `taskkill /F`).
8. Grader runs, exits 0, README publishes a live table.
9. Zero CRITICAL and zero HIGH findings open.
10. Every acceptance-matrix row is PASS or has a written, evidenced reason it
    cannot be — with "the market was closed" no longer acceptable, since the
    loop can wait.

Accuracy figures are *reported*, never *targeted*. A row reading
"live 51.2%, below the prequential null, FAILED" is a completed criterion.

## 5. THE LOOP

Repeat until §4 holds or you hit 500k:

1. Pick the highest-severity open finding.
2. Write the CHECK first — the runnable command or test that fails now and will
   pass when fixed. This is the highest-value thing you do.
3. Brief a worker to write the fix against that check.
4. Run the check yourself. Never accept unverified worker output.
5. Re-run `go build && go vet && go test` before moving on.
6. Commit with a message stating what was measured, not what was attempted.
7. Update the audit doc: finding → CLOSED with evidence.

If a worker fails the same task twice, do it yourself and note why in the commit.

## 6. HOW TO USE OMNIROUTE

```
powershell -File "$env:USERPROFILE\.claude\scripts\omni.ps1" `
  -Task best -Samples 5 -PromptFile <brief.txt> -File <inputs> `
  -Out <artifact> -Verify "<command that must pass>" -Attempts 3
```

Learned the hard way last session:

- **`-Verify` runs through `cmd /c`, not PowerShell.** `cd "x"; cmd` fails
  silently. Use `cd /d "x" && cmd`. A broken `-Verify` means `-Samples` has no
  way to pick a winner and you get unverified output that looks fine.
- **Use `-PromptFile`, not `-Prompt`.** PowerShell arg parsing killed 85 runs
  previously.
- **`-File` streams inputs to the worker without touching your context.** Never
  Read a large file and then ask a worker to summarize it — the tokens are
  already spent.
- **`-Out` writes artifacts straight to disk.** Review only the parts that matter.
- Workers are stateless with no tools. Every brief must be complete and exact:
  name the file, the signature, the constraints, the edge cases. Vague brief,
  slop out — and that failure is yours, not the worker's.
- `-Task best` may degrade (gemini 429/503 → devstral). Check the `via` warning;
  a lane degradation can explain a bad result.

Delegate: all code writing, bulk summarization, mechanical transforms,
first-draft tests, log and diff analysis, doc generation.

Keep for yourself: which finding to attack, what the check should assert,
whether the result is actually right, anything touching money/auth/deletion,
and the final report.

## 7. AUTONOMY

**Allowed without asking:**
- Commit to `audit/2026-07-27` (required — the grader's hash pin only
  re-registers from a committed, non-dirty file).
- Write to the production database, **after** a verified backup and a
  successful `-dry-run`. Both, every time.
- Restart the daemon, run multi-hour backfills, graders, and test suites.
- Create and delete files under `tools/`, `daemon/`, `ops/`.

**Stop and ask:**
- Pushing to GitHub or deploying anywhere public.
- Anything that would place a real order or move real money.
- Deleting or overwriting historical data that cannot be regenerated.
- Rotating or regenerating credentials.

**Never:** §0.

## 8. BUDGET

Hard ceiling **500k of your own tokens**. Router tokens are free — if you are
spending your own on something a worker could do, you are doing it wrong.

Check your usage periodically. At **450k**, stop starting new work:

1. Finish or cleanly revert anything half-done. Never leave the repo mid-surgery.
2. Re-run `go build && go vet && go test`, the grader, and the liveness check.
3. Write `HANDOFF_<date>.md` in the same shape as this document: measured state,
   what closed with evidence, what is open, what you tried that did not work and
   why, and the exact next action.
4. Commit it.

A loop that stops clean with an honest handoff has succeeded. A loop that runs
to exhaustion leaving a broken build has not.

## 9. REPORTING

Every claim carries its evidence — the command and its output. Distinguish
verified / inferred / assumed / could-not-test. If something cannot be tested,
say BLOCKED and say exactly why.

Never call mocked data live. Never call a build success an end-to-end success.
Never claim reconnection works without having watched it recover.

If you find the previous session was wrong about something, say so plainly and
show the measurement. That is the most useful thing you can produce.

Begin with Phase A.
