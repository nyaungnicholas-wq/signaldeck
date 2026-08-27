# Evidence ledger — autonomous trading goal
Opened 2026-08-26. Every line is a measurement with the command that produced it.
Nothing here is inferred from a prior report.

## E1 — The forward test cannot complete at current volume
`tools/forward_test.py --verdict` -> `0 session(s) computed`, three trading days
after the window opened (REGISTERED_START 2026-08-23).

Registered thresholds (prereg seq 87, immutable): MIN_BETS_PER_SESSION = 5,
MIN_SESSIONS = 60.

Actual gradable book rows per day (`confluence_outcomes`, UTC buckets):

| date | rows |
|---|---|
| 2026-08-20 | 259 |
| 2026-08-21 | 62 |
| 2026-08-22 | 4 |
| 2026-08-23 | 2 |
| 2026-08-24 | 2 |
| 2026-08-25 | 3 |
| 2026-08-26 | 2 |
| 2026-08-27 | 1 |

After the registered population screens (direction=1, entry_px>=20,
|fwd_return|<=0.30, market='stocks') only **3 rows** survive across the whole
post-registration window. At 1-4 bets/day against a floor of 5, **no session can
ever be eligible**. The instrument is structurally unable to reach a verdict.

## E2 — The collapse is upstream gating, not a missing worker
All relevant workers run and report ok:

| worker | latest detail |
|---|---|
| forecast-trainer | `trained 630 forecasts over 329 symbols` |
| confluence-scorer | `assessed 329 symbol(s), 50 setup(s), **0 new event(s)**` |
| composite-scorer | `gated: only **5** symbol(s) with fresh ungated predictions (<30)` |
| confluence-resolver | `resolved 0 confluence outcome(s)` |

`predictions` holds 1,131-7,090 rows/day over 329-2,947 symbols, so raw
prediction volume is healthy. Only ~5 survive gating. The starvation is a
GATE, not an outage.

## E3 — forecast-monitor measures a different universe than the trainer
`forecast-monitor` (degraded): `only 0 of 2567 symbols received a forecast`,
while `forecast-trainer` (ok) reports `trained 630 forecasts over 329 symbols`
in the same period. The monitor counts against the swept-wide universe; the
trainer works the pruned one. One of the two numbers is answering the wrong
question. Unresolved — see R2.

## E4 — Models are gated on their own out-of-sample evidence
`alphax_models`: 1d has oos_lift **-0.0089**, auc 0.5128, `gated=1`.
1w has oos_lift 0.0092, auc 0.4911.
`symbol_models` rows on the wide universe read
`Still learning (0/30 own-outcome days, 0 graded rows) - using the equal-weight prior`.
`expectancy-trainer` (degraded): `anti-predictive and benched fleet-wide
(1d AUC 0.4482, 1w AUC 0.4775)`. `gbm-trainer` (degraded): `no model leg cleared
its OOS edge bar`.

This is the system refusing to trade models that fail their own bar. Correct
behaviour, and a likely honest contributor to E1/E2.

## E5 — The collapse predates this session's commits
Confluence fell 259 -> 62 -> 4 across 2026-08-20/21/22. The 2026-08-23 commit
series (ccb083a..b5ea150) lands after it. Not caused by that work.

## E6 — Offsite backup: the S3 commit did not take effect
Commit `8f3134b backup: an S3 offsite destination, and one writer for the key
that names it` is in HEAD, yet:
- `meta.backup_offsite_dir` = `''` (empty)
- `meta.backup_last_offsite` = 1786492388 = **2026-08-11**, now 15 days stale
- `logs/backup-offline.log` every night through 2026-08-26:
  `offsite NOT OFFSITE: C:\Users\Nicholas_N\OneDrive/SignalDeckBackups is on the
  same volume as the database`

The job still resolves to the OneDrive default. It is also re-accumulating the
~9.5GB of same-volume copies deleted on 2026-08-23.

## E7 — The staleness alarm shipped 2026-08-23 works in production
`logs/backup-offline.log`:
```
2026-08-24T18:41:51 Last off-machine backup is 13 days old (>= 3 days threshold).
2026-08-25T18:58:12 Last off-machine backup is 14 days old (>= 3 days threshold).
2026-08-26T18:46:08 Last off-machine backup is 15 days old (>= 3 days threshold).
```
Fires daily, rate-limited, non-fatal. Verified.

## E8 — Worker errors standing in the last 24h
`ai-analyst` error x7, `sentiment-tagger` error x24, `universe-poller` error x1.
Numerous `orphaned` runs across the fleet. Triage pending — see R3.

## E9 — DECISIVE: the empty book is correct abstention, not a defect
Read from `daemon/internal/ensemble/ensemble.go`, not inferred.

`admitsLeg(c, leg, lift)`: when a leg carries a ranking grade, that grade alone
decides — `if e, ok := c.RankEdge[leg]; ok { return e > 0 }`. Otherwise it falls
through to `admits(lift, RequireMeasuredLegs)`, which in production mode
(`RequireMeasuredLegs=true`) requires a MEASURED positive lift; absence of
evidence does not admit.

Measured on the latest fresh prediction per symbol:

| horizon | n_used>=1 | of fresh |
|---|---|---|
| 1d | **2** | 2665 |
| 1w | 635 | 2946 |

A live 1d component blob explains the 2/2665:

| leg | measured value | admitted |
|---|---|---|
| forecast | RankEdge **-0.0434** | no |
| pressure | PressureLift **-0.348** | no |
| expectancy | lift `None`, strict mode | no |
| sentiment | lift `None`, strict mode | no |
| gbm, meanrev | opt-in, lift `None` | no |

Every 1d leg is either measured anti-predictive or never measured, so nothing
blends and `n_used=0`. The 1w blob differs in exactly one place:
`RankEdge={'forecast': +0.0048, 'pressure': -1}` -> the forecast leg is admitted
-> `n_used=1`.

The source states the doctrine explicitly: *"a MEASURED anti-predictive leg is
DROPPED, never down-weighted (honesty doctrine)."*

**Conclusion.** The full chain
`1d legs all fail their bars -> n_used=0 on 2663/2665 -> composite-scorer gated
(5 < MinCurveN 30) -> confluence 0 new events -> forward test 0 sessions`
is the system refusing to trade signals that failed their own out-of-sample
bars. It is working as designed. Widening it would be weakening a gate, which
this goal forbids and which the repo's doctrine forbids.

**R2 is therefore reclassified NOT-A-DEFECT / WONTFIX.**

The corollary is the answer to the goal's central question: SignalDeck is not
profitable and is not tradeable today, and the reason is not that it is broken —
it is that its own gates measured its signals and shut the book.

## E10 — The only lane carrying admitted evidence is 1w
1w is the sole horizon with a positively-graded leg (forecast, RankEdge +0.0048,
635 symbols blending). Per the goal's "validate/promote each lane
independently", 1w is the only lane with anything to validate. Note the edge is
tiny and `alphax_models` 1w reports auc 0.4911 with oos_lift 0.0092 — consistent
with the settled verdict that IC ~0.02 flips sign across sub-periods.
Chasing a better 1d leg via config search is a REFUTED program (CLAUDE.md) and
is not attempted.
