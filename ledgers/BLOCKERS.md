# Blocker ledger
Opened 2026-08-26. A blocker is something that cannot be closed by code in this
repo. Anything closable by code belongs in REPAIR.md, not here.

## B1 — LIVE trading: BLOCKED, and correctly so
Status: **BLOCKED — NOT COMPLETE**. This is a hard stop, not a task.

Required before any real order and all currently MISSING:
- broker, account, jurisdiction
- versioned `LIVE_RISK_CONFIG` defining: position sizing, stop-loss, daily loss
  limit, max drawdown, exposure/leverage caps, permitted instruments and
  sessions, kill switch, order idempotency, reconciliation, and the
  paper -> tiny-canary promotion rule

Current state: `LIVE_ARMED` does not exist anywhere in the repo, and neither
does `LIVE_RISK_CONFIG`. There is no live order path to arm: the only execution
surface is `execution/execute.py` (long-only, paper) and `paper-api` reads.
Fail-closed is therefore the DEFAULT rather than a setting, which is the safe
direction. No credentials requested, none exposed.

**Not actionable by me.** Requires the account and risk inputs from Nicholas.

## B2 — Off-machine backup requires a destination that does not exist on this box
Status: **BLOCKED on hardware/account**, with a code defect underneath (see R1).

Measured 2026-08-26: one physical disk (Kingston 1.9TB NVMe), one volume C:,
no network drives, no USB attached. OneDrive.exe runs with **no account signed
in** (UserEmail/UserFolder/cid empty on all three account keys).

Nicholas chose **external drive** on 2026-08-23. None is attached yet. The
finishing step once one is:
`setx SIGNALDECK_OFFSITE_DIR "<letter>:\SignalDeckBackups"` then restart the
daemon. One variable configures both halves (the shell script reads it directly;
`offsiteBackupDir()` in run.go accepts it as a fallback and writes
`meta.backup_offsite_dir`, which is what `/api/quality.offsiteConfigured` reads).

Commit 8f3134b added an S3 path but it is inert — see R1.

## B3 — The forward test cannot reach a verdict, and its thresholds are sealed
Status: **BLOCKED on evidence, not on code.**

Per E1 the book yields 1-4 bets/day against a registered floor of 5. The
thresholds live in prereg seq 87 and `forward_test.py` refuses `--commit` with
any other REGISTERED_START.

Lowering MIN_BETS_PER_SESSION to make sessions eligible would be **weakening a
gate to manufacture a result**, which the goal forbids and which would void the
pre-registration. NOT DONE and must not be done.

The legitimate paths are: (a) fix whatever is over-gating predictions so the
book genuinely widens (R2), or (b) accept that the test runs long and report it
honestly. Either way the answer to "is it profitable" stays **unproven**.

## B4 — Push is out of scope under this goal
The goal forbids pushing. Branch `hmm-regime-and-pbo` is currently level with
origin (`unpushed: 0`). Work under this goal will be committed locally only.

## B5 — "Credible SPY outperformance" cannot be delivered from this evidence
Status: **BLOCKED — NOT COMPLETE**, and it is not blocked on effort.

Every lane this repo supports has now been independently validated:

| lane | measured | significant? |
|---|---|---|
| stocks 1d | book SHUT by its own gates (forecast RankEdge -0.0434) | n/a - no book |
| stocks 1w | only admitted leg (+0.0048) | not tested to significance |
| crypto 1d | -6.89pp vs naive, t=-0.85 | no |
| crypto 1w | -11.66pp vs naive, t=-1.41 | no |
| paper flagship-1d | -0.65pp vs SPY, daily t=+0.22 | no |
| paper flagship-1w | +0.24pp vs SPY, daily t=-0.25 | no |
| paper 1d/1w-replay | dormant; -1.34pp / -1.42pp | no |
| structural (regime) | 2 gradeable rows of 58,206 | ungradable (E22) |

**Not one lane shows a significant edge.** That is a measurement, not a
shortfall in effort, and it is consistent with the repo's own settled verdicts
(IC ~0.02 flipping sign; 0 of 27 configs clearing the pre-set bar on a fund-free
universe).

### Why this cannot be closed by more work here
The only routes from "no edge measured" to "SPY outperformance claimed" are
routes this goal and CLAUDE.md both FORBID:
- lowering `MIN_BETS_PER_SESSION` or `MinCurveN` -- weakening a gate, and it
  would void prereg seq 87;
- admitting a leg whose measured rank edge is negative -- inverting the honesty
  doctrine that is the platform's main asset;
- config search for a better arm -- explicitly REFUTED (CLAUDE.md: "manufactures
  false positives"), and the fund-free rerun already cleared 0 of 27;
- quoting `flagship-1d`'s +31.8% annualised daily-mean excess -- t=0.22, n=14,
  cumulative excess of the OPPOSITE sign (E19). That number is noise and
  quoting it would be inventing evidence.

The honest position: the system is now correctly instrumented, its faults are
repaired, every lane is measured, and the measurement says there is no edge to
trade. Establishing one requires new signal research, which is a different
undertaking from this repair-and-verify goal -- and the only registered
instrument that could ever settle it (forward test, prereg seq 87) cannot
accumulate sessions while the ensemble correctly declines to bet (E9).

### B1 update (2026-08-27): the contract now exists; the INPUTS still do not

`daemon/internal/liverisk` (ef323e9) encodes exactly what this goal specifies a
`LIVE_RISK_CONFIG` must define, and gates it fail-closed. **It arms nothing** -
there is still no live order path anywhere in this repo.

What changed is the KIND of safety. Before: nothing could trade because nobody
had written the code - accidental. Now: the day that code is written it must
pass `Armed()`, which requires BOTH `LIVE_ARMED=true` AND a config where all 16
fields validate. The zero value can never arm, under any flag value, and that
is pinned by a test and mutation-checked.

Every field is required on purpose. A partially-specified risk config is more
dangerous than none because it LOOKS configured - the same failure shape as
`offsiteConfigured:true` over a folder on the same disk. `Validate` reports all
16 problems at once rather than the first, so nobody fixes one field per run
against a config that was never going to be complete.

**Still BLOCKED, unchanged:** broker, account, jurisdiction, and the actual
risk numbers are Nicholas's to supply. No credential was requested or exposed.
The honest state is now 'the gate is built and locked, and the key does not
exist' rather than 'there is no gate'.
