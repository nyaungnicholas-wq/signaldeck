# Overnight handoff — 2026-08-06

Written ~02:25 local by the supervising session. Every fact in the "Verified"
block was checked directly against git / go / the running daemon at that time,
not taken from a session's report. Session-reported claims are labelled.

---

## Verified state (checked directly)

| Thing | Value |
|---|---|
| Branch | `accuracy-rootcause-fixes` |
| HEAD | `e1ae686` |
| Unpushed commits | **8** (sessions variously said 5 and 7 — both stale) |
| `go build ./...` | exit 0 |
| `go vet ./...` | exit 0 |
| Dirty files in tree | 81 |
| PR #8 | **OPEN, MERGEABLE, 6/6 checks SUCCESS** |
| Daemon | PID 73856, `127.0.0.1:8322`, revision `0499416`, `resolvable: true`, `modified: false` |

**The daemon is 8 commits behind HEAD.** The cross-section gate, breadth
reporting, the fleet-veto floor and the cost disclaimer are all committed but
**not live**. Nothing is pushed.

Note: the daemon serves on **8322**, not 8080.

---

## Do these in order

1. **Merge PR #8.** 6/6 CI green including the `-race` job; head SHA `a78ed92`
   matches the PR head exactly.
2. **Then deploy** per [`ops/MORNING_DEPLOY_2026-08-06.md`](MORNING_DEPLOY_2026-08-06.md).
   Order matters: `main` currently lacks `0499416`, so deploying from `main`
   before the merge rolls code backward against a forward schema. The runbook
   was corrected for this.
3. **Push the 8 commits** on `accuracy-rootcause-fixes` and restart the daemon,
   or the gate stays committed-but-dark.

---

## Decisions only you can make

- **Re-register the grader on the prereg chain.** `204bf12` added
  `breadth_block()` to `tools/accuracy_registry.py`; the grader is SHA-pinned at
  seq 48 and now refuses to run (`UNREGISTERED GRADER`, pins `6c2902a6…`, file
  hashes `b826f397…`). That is the control working correctly. Until
  re-registered the registry publishes without breadth, so
  `directional-ensemble (1d)` still reads "FAILED, effective_n=245.6" with no
  indication it rests on 11 near-unanimous market calls, 3 of them right.
  Verified verdict-neutral. Re-registration is a governance write that was
  scoped out of session autonomy.
- **`RequireMeasuredLegs`** — flipping it drops 1d emission to 30/322 symbols.
- **GitHub contribution count** 310 vs 353 — reconciling needs a PAT
  (credential creation + repo secret).
- **8 files staged-ready** from the quant-portfolio session (researchx,
  discover, meanret_test, researchloop, cmd/seldiff, pbo tooling). That session
  declined to commit them on a shared dirty tree; the exact `git add` line is in
  its transcript.

---

## Known gaps (session-reported, not independently verified)

- **Test gap:** the cross-section gate's ordering invariant (measure BEFORE the
  gated `continue`) is held by structure and comment, not by a test. A
  regression there silently restores the latch `9fa2b21` fixed.
- **Rollback path unexercised.** The `superseded_by` migration succeeded
  forward on live data, but the rollback has never been run. Worth exercising
  before the *next* schema change.
- `d1a9c28`'s subject carries a stray `@` from a PowerShell here-string. Not
  fixable — the reference-transaction hook refuses amends because the evidence
  ledger anchors commit SHAs. Body and files are intact.
- The self-locking-guard pattern appeared **three times** in one session,
  including in a fix for itself. Flagged as the thing to watch in this codebase.

---

## The headline finding

The published **44.27% "FAILED, retire=true"** grades a bug that was already
repaired. During 2026-07-27 → 08-04 the calibration map had collapsed — 329
symbols receiving 5–14 distinct probabilities — and the hard 0.5 threshold
turned that into one market call republished per symbol, then graded as ~330
independent forecasts. Accuracy was measuring the tape, not the model.

Threshold-free and day-clustered: **AUC 0.5226 ± 0.0154**. Under the codebase's
own `dayBet` framing the record is **3/10, CI [0.108, 0.603]** — contains
chance.

None of tonight's work creates an edge. It removes defects that made a
coin-flip signal look worse than a coin flip.
