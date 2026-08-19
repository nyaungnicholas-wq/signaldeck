# SignalDeck — settled verdicts and pre-flight checks

Memory is the authority on research verdicts (~/.claude ... /memory/MEMORY.md, SignalDeck
cluster). Root superprompts are frozen history unless DOCS_INDEX.md says otherwise; anything
in quarantine/ is SUPERSEDED — do not execute it.

## Settled verdicts — do NOT re-run these programs
- IC ~0.02 and FLIPS SIGN per sub-period (t=1.73/0.80). 70% (or 80%) directional accuracy is
  UNREACHABLE from this signal; honest ceiling ~61.5%. Config search on it manufactures false
  positives. EIGHTY_PERCENT_SUPERPROMPT is quarantined for this reason.
- ACCURACY DIVERGES FROM PROFIT: the best-accuracy arm has the worst Sharpe. The real tradeable
  edge is alpha-Sharpe ~1.0 (2.71 holdout), h=10, k=10/leg, conviction-weighted, beta-neutral.
  Most "proper" risk controls HURT — inverse-vol is destructive (alpha lives in HIGH-vol names).
- dircall stays OUT of the CALL path (costs 3-11pp) and must NOT be inverted — the book is
  indistinguishable from chance, not anti-predictive. The -13/-23/-28pp directional number is
  an arithmetic IDENTITY (acc = 1 - null), not anti-skill.
- Alpha stacking REFUTED; HMM vol labeler beats the incumbent 1.59x (the Go HMM IS causal;
  the old Python replication was not — do not conflate them).

## Pre-flight checks before claiming ANYTHING works or shipped
- A fix is not live until worker_runs.revision matches git HEAD. Check it.
- Green gates are not a working product: 20+ real defects have shipped behind all-green gates.
  Open the web UI in a real browser before calling web work done.
- The interpreter is .venv/Scripts/python.exe — name it explicitly in commands and -Verify.
- accuracy_registry.py's sha256 is PINNED in the prereg chain and it REFUSES to run when edited.
  Ship guards beside it, never edit it. repro/ pins rot silently (a grader sat 9 re-registrations
  stale behind three green gates) — check pin freshness when touching repro/.
- TZ= is silently UTC under Git Bash — timezone-sensitive stats invert (measurement-inversion).
- deployment_drift needs a >15-min outage to clear; a running fleet never produces its "boot".
- NEVER raise the Go research-loop cadence (his standing order).

## Hygiene
- .claude/worktrees/ may hold stale session copies of this whole repo. Repo-wide grep/glob must
  exclude .claude/ — a hit in a worktree is a STALE file, and "fixing" one is the
  undeployed-fixes failure shape. Remove worktrees when a session ends.
- Long-running loops (score loop, research loop) stage through shared files — wait for a quiet
  window before git add on shared paths.
