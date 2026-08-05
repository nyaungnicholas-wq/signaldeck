# P4C — Kill switch: truth correction

**Date:** 2026-08-04 · **Phase:** P4C · **Resolves:** FC5

## The false claim

`INSTITUTIONAL_GAP.md`, in the table headed *"Already built (verified, not
claimed)"*, carried this row:

```md
| **Kill switch + pre-trade risk limits** | `stock-trader/trader/risk_gate.py` — fail-closed, file-based kill switch |
```

Three things were wrong with it at once:

1. **It cited a different repository.** `stock-trader/` is a separate project.
   No file in it can halt this daemon, has any import relationship to it, or is
   reachable from any code path here.
2. **It was under a heading asserting verification.** "Verified, not claimed"
   was applied to the whole table as a property, which is precisely how a row
   that was never verified travelled undetected.
3. **It conflated two different controls.** Pre-trade risk limits
   (`internal/riskgate`) genuinely existed here. Bundling them with a kill
   switch that did not made the true half vouch for the false half.

## The correction

The citation is gone from every document in this repository. A real, in-repo
kill switch now exists, is wired, and is tested.

```md
| **Kill switch** | `daemon/internal/killswitch` — fail-closed, file-based (`ops/HALT`), read before every order, proven by halt simulation. Governs the SIMULATED book; this repository has no live order path to halt |
| **Pre-trade risk limits** | `daemon/internal/riskgate` — `Admit` gates the book, `Evaluate` gates and sizes the candidate, BEFORE the EV go/no-go |
```

## An intervening false claim, also corrected

Between the original defect and this correction, the P0/P6 governance pass
measured the repository and recorded:

> `daemon/internal/killswitch/killswitch.go` exists, is well-specified, and has
> **zero importers and no test file**. Nothing calls it. It cannot halt anything
> in its current state.

**That was true when measured and is false now.** The package was mid-flight
during this phase. Rather than silently rewrite the finding, every occurrence
has been struck through and amended in place with a date and a pointer here, so
the governance record shows what was measured *and* what changed it:

| Document | Location | Action |
|---|---|---|
| `proofs/P6_GOVERNANCE_CLEANUP.md` | §1 BUILT BUT NOT IN FORCE | struck, amended, reclassified BUILT AND IN FORCE |
| `proofs/P6_GOVERNANCE_CLEANUP.md` | §1 NOT BUILT | amended: "no claim in this corpus may rest on it, and none now does" |
| `proofs/P6_GOVERNANCE_CLEANUP.md` | FC5 row | **RESOLVED 2026-08-04**, paper book only |
| `proofs/P6_GOVERNANCE_CLEANUP.md` | carry-forward list | struck, closed |
| `INSTITUTIONAL_GAP.md` | freeze banner, defect (1) | closed |
| `INSTITUTIONAL_GAP.md` | BUILT AND IN FORCE | kill switch added, scoped "paper book only" |
| `INSTITUTIONAL_GAP.md` | BUILT BUT NOT IN FORCE | struck, moved |
| `INSTITUTIONAL_GAP.md` | "The honest summary" | "a kill switch nothing calls is a design document" struck and amended |
| `STRATEGY_DECK.md` | §9 | rewritten; FC5 marked resolved |

## What the kill switch actually is

`daemon/internal/killswitch` — 1 file, 1 test file.

- **File-based.** Default `ops/HALT`, overridable with `SIGNALDECK_KILL_SWITCH`.
  `echo "reason" > ops/HALT` halts; `rm ops/HALT` resumes on the next pass with
  no restart. The file's contents become the audited reason.
- **Fail-closed.** Three outcomes are possible and only one permits trading:
  file definitively absent → RUNNING; file present → HALTED; **the check could
  not be completed at all → HALTED.** A permission error, an unmounted volume or
  a malformed path is the absence of evidence, not evidence of absence.
- **Checked before every order**, read fresh rather than cached for the pass, so
  a halt tripped mid-run stops the next entry rather than the next restart.
- **Entries refuse; exits do not.** A halt that traps the book inside the
  position it was tripped by is a larger risk than the one it controls.
- **Every refusal is ledgered** to `ev_decisions` with reason
  `kill-switch-halted` and the full `killswitch.State` snapshotted.

## Evidence

**Importers** — the row that said "zero importers":

```bash
cd daemon && grep -rln "internal/killswitch" --include="*.go" . | grep -v "^./internal/killswitch"
```

```
./internal/ev/ev.go                              (doc reference)
./internal/pipeline/paper.go                     (checked before every order)
./internal/pipeline/paperev.go                   (refusal ledger snapshot)
./internal/pipeline/riskgate_admission_test.go   (halt simulation)
```

**Halt simulation** — the row that said "no test file":

```bash
cd daemon && go test ./internal/killswitch/ ./internal/pipeline/ -run "KillSwitch" -count=1 -v
```

```
--- PASS: TestCheckPathTruthTable
--- PASS: TestUnreadablePathFailsClosed
--- PASS: TestEnvHalt
--- PASS: TestEnvCannotClearAFileHalt
--- PASS: TestPathOverride
--- PASS: TestHaltMidRunStopsTheNextOrder
--- PASS: TestKillSwitch_HaltsEntriesAndLedgersTheRefusal
--- PASS: TestKillSwitch_UnparseableHaltInstructionHaltsTheWorker
--- PASS: TestKillSwitch_ExitsStillExecuteWhileHalted
ok  github.com/nyaungnicholas-wq/signaldeck/internal/killswitch
ok  github.com/nyaungnicholas-wq/signaldeck/internal/pipeline
```

`TestKillSwitch_HaltsEntriesAndLedgersTheRefusal` is the end-to-end halt
simulation. It runs the **real** worker against a **real** store, using a seeded
candidate that provably fills when unhalted (the identical seed used by
`TestEVGate_LedgersBuyAndExit`), so a non-fill can only be the halt. It asserts:
no position, no trade, a ledgered `DO_NOTHING` with reason
`kill-switch-halted`, `"halted":true` in the snapshot, and **resumption** after
the file is removed.

**No remaining false citation:**

```bash
grep -rn "risk_gate.py" --include="*.md" . | grep -v ".claude/worktrees"
```

Four hits remain, and every one *names it as not this platform's control*:
`INSTITUTIONAL_GAP.md` ×2 (the freeze banner recording the defect, and the NOT
BUILT row), `proofs/P6_GOVERNANCE_CLEANUP.md` ×1 (NOT BUILT row),
`STRATEGY_DECK.md` ×1 (explicit disclaimer). **Zero documents claim it as this
platform's control.**

Stale copies exist under `.claude/worktrees/` — agent scratch directories, not
part of the repository's published surface.

## Done-when

| Criterion | Status |
|---|---|
| No document claims another repo's control as this platform's control | ✅ verified by grep; all four surviving mentions are disclaimers |
| Kill switch status is built and tested here, **or** explicitly absent | ✅ **built and tested here** — 9 passing tests including an end-to-end halt simulation |

## The limit of this correction, stated plainly

The switch halts **a simulation**. There is no broker order path in this
repository, so what it protects is a paper book's integrity, not capital. It
also **stops new risk without liquidating** — the `−12%` "flatten and halt" rung
in `RISK_POLICY.md` §3 remains SPEC ONLY, because flattening is a different and
more dangerous operation than halting, and a halt control that cannot flatten
should not claim it can.

Fixing the citation did not make this an institutional control. It made the
document true.
