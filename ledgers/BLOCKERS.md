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
