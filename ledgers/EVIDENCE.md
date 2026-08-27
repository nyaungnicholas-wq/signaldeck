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

## E11 — Execution safety is STRUCTURAL, not a setting (fail-closed holds)
Searched 2026-08-27:
- No `execution/` directory exists in this repo and git has no history for one.
- The only Alpaca trading host anywhere in `daemon/` or `tools/` is
  `defaultBasePaper = "https://paper-api.alpaca.markets"`. A grep for
  `api.alpaca.markets` excluding the data and paper hosts returns NOTHING.
- `LIVE_ARMED` and `LIVE_RISK_CONFIG` do not exist as identifiers anywhere.

So there is no live order path to arm or disarm. Fail-closed is the shape of the
system rather than a flag someone could flip, which is the strongest form of the
guarantee the goal asks for. No credentials were requested or exposed at any
point in this work.

## E12 — The statistical machinery the goal demands is largely PRESENT
Audited by locating implementations, not mentions:

| requirement | status | where |
|---|---|---|
| PBO | PRESENT, via CSCV | `tools/pbo_ledger.py` (`cscv`, `drop_mirrors`, `wilson_lower_bound`), `tools/test_pbo.py` |
| Deflated Sharpe | PRESENT | `research/dirfix/holdout_port.py` -> `search.deflated_threshold(..., TRIALS)` |
| Multiple-testing correction | PRESENT | Bonferroni in `tools/accuracy_registry.py`, `tools/alpha/xscore.py`; `tools/effective_trials.py` |
| Embargo / purge | PRESENT | `tools/controls_evidence.py`, `daemon/internal/alphax/alphax.go` |
| Walk-forward | PRESENT | `daemon/internal/metalabel/metalabel.go`, `research/dirfix/` |
| Sealed holdouts | PRESENT | `research/dirfix/holdout_eval.py`, `check_search.py` |
| Calibration | PRESENT | reliability tables published in the accuracy registry |
| White's Reality Check | ABSENT | only an informal "reality check" in `research/dirfix/probe.py` |

**This is the important positive finding of the audit.** The repo is not
methodologically weak; PBO via CSCV and a trials-aware deflated-Sharpe bar are
the correct tools, honestly applied. The obstacle to profitability is the
SIGNAL, not the rigour used to measure it. That also means the usual failure
mode -- overfitting a backtest into a false positive -- is already defended
against here, and the settled verdicts in CLAUDE.md are the output of those
defences working.

## E13 — forecast-monitor: an unresolved contradiction, left unresolved on purpose
`DayStat.Starved()` is `Symbols >= MinSymbolsForCollapse && CoverageRatio() < MinCoverageRatio`,
so the denominator is the whole active symbol count -- which oscillates with the
daily sweep (measured denominators in consecutive messages: 291, 997, 2567).

The contradiction I cannot yet settle:
- `forecast-monitor` says `only 0 of 2567 symbols received a forecast`,
  most recently for 2026-08-27.
- `model_forecasts` holds rows for **882 distinct symbols** on 2026-08-27.

Both cannot be describing the same quantity. Either the monitor reads a
different notion of "received a forecast" than `model_forecasts`, or one of the
two is measuring the wrong population.

NOT CHANGED, for three reasons: my first hypothesis (wrong denominator) is
unproven; given E9 the monitor may be correctly reporting real abstention, in
which case "fixing" it would silence a true alarm; and commit 61f8bfb shows
another session actively working this exact surface ("stop the health gate
crying wolf at the restart window"), so editing it would collide.

A permanently-degraded monitor is still a defect in effect -- an alarm that is
always on is an alarm nobody reads -- but the fix requires settling which of the
two numbers is right, and that is the next piece of work here, not a guess.

## E14 — LLM repair VERIFIED live (and the first attempt was verified FAILING)
Two-stage, and the first stage is the instructive one.

Stage 1 (e5393b9) corrected `internal/llm`'s constants, deployed green on
1f90332, and CHANGED NOTHING: `sentiment-tagger` answered
`llm: provider error: HTTP 410` at 00:11:47, seconds after the new binary came
up. Cause: `config.go` spelled the same three ids out again as `pick()`
defaults, so the corrected constants were never consulted.

Stage 2 (e25505e) made `config` reference `llm.DefaultModel/Deep/Fast` so there
is ONE definition.

Verified against the running daemon at `/api/ai/status`, not inferred:

| field | value |
|---|---|
| model | `nvidia/nemotron-3-nano-30b-a3b` |
| fastModel | `nvidia/nemotron-3-nano-30b-a3b` |
| deepModel | `nvidia/nemotron-3-super-120b-a12b` |
| stats.calls (today) | **79** |
| stats.lastError | **`''` (empty)** |

79 successful calls with an empty lastError, against 31 failures/day before.

The lesson is the one this repo keeps relearning: a deploy that goes green
proves the binary changed, never that the behaviour did. Stage 1 would have been
reported as fixed by anything that did not go and look.

## E15 — R7 was MY error: the escalation works. Correcting it.
I recorded R7 as "31 LLM errors/day sat in worker_runs with nothing escalating
them". That was wrong, and I only found it by reading the raw record instead of
my own first parse (which used `failing`/`stale` and got nulls, because the
fields are `failingWorkers`/`staleWorkers`).

`data/health.json`, written 2026-08-27 00:15:
```
{"ok":false,
 "staleWorkers":["offsite backup is 15.3 days old (last 2026-08-11) - the local
                 copy is not a disaster-recovery copy"],
 "failingWorkers":["sentiment-tagger"],
 "ts":1787814903}
```

Both of this session's real problems were already detected and named:
- `health.FailingWorkers` caught the LLM failure by consecutive-error streak --
  the mechanism added after the 2026-08-11 forecast-monitor incident.
- The offsite staleness reached the same surface, phrased for a human.

Delivery is configured too: `SIGNALDECK_DISCORD_WEBHOOK` is SET, and
`logs/notify-silence.log` confirms on 08-24, 08-25 and 08-26
`remote notify configured - daemon alerts are pageable beyond this Mac`.

So the platform detected both faults, named them accurately, and had a live
transport to deliver them. The gap was that nobody read the alert -- a human
process gap, not a code defect. **R7 is reclassified NOT-A-DEFECT.**

I did not send a test alert: that would push to an external service, which this
goal puts out of scope.

## E16 — Both repairs CONFIRMED in production
Settled worker runs after the e25505e deploy, read from `worker_runs`:

| worker | before | after |
|---|---|---|
| `sentiment-tagger` | `llm: provider error: HTTP 410` x24/day | **ok** - `tagged 60 headlines` |
| `ai-analyst` | `llm: provider error: HTTP 410` x7/day | **ok** - `wrote analyst brief` |
| `universe-poller` | `status 400: invalid symbol: ATC.220816` every run | **ok** - `refreshed 283/286 universe symbols (1d 1968, 1h 31668, 1m 824229 bars)` |

The universe-poller line is the material one. `283/286` is the repair behaving
exactly as designed: the vendor-rejected names were dropped and every other
symbol was kept, where previously the whole batch died. **824,229 one-minute
bars** were ingested on that single run -- data the platform had been silently
failing to collect on every poll since at least 2026-08-24.

Fleet-wide error scan over the following 20 minutes returns only the 00:11:47
sentiment-tagger row, which predates the deploy and is still inside the window.

Note what this does NOT do: none of it admits a leg, widens the book, or moves
the forward test off 0 of 60. It restores data collection and two analysis
workers. The edge question is untouched, and E9 still stands.

## E17 — R4 RESOLVED: forecast-monitor is correct. The contradiction was mine.
E13 recorded an unresolved conflict between
`only 0 of 2567 symbols received a forecast` (2026-08-27) and 882 distinct
symbols in `model_forecasts` that day. Resolved by reading the definitions:

- `store.ForecastDayStats` counts `prediction_outcomes` -- RESOLVED predictions.
  So `Symbols` (2567) is that day's resolved cross-section, NOT the swept-wide
  active universe. My "oscillating denominator" hypothesis was wrong.
- `DayStat.Forecast() = Symbols - Withheld`, and `ForecastDayStatsRaw` documents
  that Withheld is exactly the `n_used = 0` abstentions (verified in-repo across
  the whole table: 4442 rows at `n_used=0 AND raw_prob=0.5`, zero at `n_used=0`
  with any other raw_prob, so `n_used > 0` is an EXACT filter for "carries a
  forecast").

So `0 of 2567` means every resolved prediction that day was an abstention. That
is **E9 measured from the outcomes side**, by an independent path, and it
corroborates rather than contradicts it.

The apparent conflict with `model_forecasts` was a category error on my part:
`model_forecasts` holds TRAINED MODEL OUTPUTS; `Forecast()` counts predictions
that ADMITTED a leg. A model is trained and its leg is still refused admission.
Both numbers are right and they measure different things.

**R4 reclassified NOT-A-DEFECT.** The permanent `degraded` is the honest status
of a system abstaining on its whole cross-section. Had I "fixed" the denominator
on my first hypothesis I would have silenced a true alarm -- which is exactly
why E13 left it alone.

### Pattern worth recording
Three of my own findings this session inverted on inspection: R2 (gating
"defect" -> correct abstention), R7 (escalation "missing" -> present and
working), R4 (monitor "wrong denominator" -> correct, and corroborating). In
every case the platform was right and my first reading was wrong. The
instruments in this repo are more trustworthy than a first pass over them, and
the fastest way to be wrong here is to believe an initial hypothesis over the
source.

## E18 — R5 (OneDrive re-accumulation): deliberately NOT changed
Measured: `C:\Users\Nicholas_N\OneDrive\SignalDeckBackups` regrew to **6.4 GB**
in the three days after I cleared 9.5 GB from it on 2026-08-23 (930M on 08-24,
927M on 08-25, a 4.6G uncompressed copy on 08-26).

Left alone, on the ladder:
1. It is bounded, not unbounded -- `compress_and_prune` runs on that path, so
   the pile is roughly one retention window, not a leak.
2. The volume has 1.2 TB free. 6.4 GB is not pressure.
3. The behaviour is a DELIBERATE, documented decision by another author:
   *"The copy is KEPT -- a second copy still survives an accidental delete --
   but it is recorded as what it is."* The `same_volume` guard already refuses
   to miscount it as offsite, which is the part that mattered and is correct.
4. **It resolves itself.** The moment `SIGNALDECK_OFFSITE_DIR` or
   `SIGNALDECK_OFFSITE_S3` is set (B2/R1), `OFFSITE` resolves to that
   destination instead and the OneDrive path stops being written at all. Fixing
   R5 separately is work that the real fix deletes.

The one fact that weakens the original rationale on THIS machine: the comment
justifies the Windows fallback on a synced OneDrive being "genuinely off-machine
in the way that matters", and no account is signed in here, so it syncs nowhere.
But that is precisely what `same_volume` already catches, and it does.

**R5 -> WONTFIX (superseded by R1).** Recorded rather than silently skipped.

## E19 — THE HIGHEST PROVEN MODE, MEASURED: paper trading does not beat SPY
This is the "paper-trade, measure" step, and it is the closest thing this repo
has to an answer on after-cost performance. Four `flagship-*` paper strategies,
509 trades, 312 equity marks.

**Two corrections applied before any number below was trusted:**
1. `paper_epochs` holds THREE epochs, and epoch 2 (2026-07-21) is labelled
   `backdated-fills-fixed` -- *"INTEGRITY BOUNDARY, not a strategy change"*.
   Measuring across it is invalid. My first pass did (07-03 -> 08-26, giving
   -1.77% / -0.15% / +0.41% / -3.88%) and those figures are DISCARDED.
2. Both `-replay` strategies are DORMANT: last equity mark 2026-08-18, eight
   days stale, positions still open. Their marks are not current prices.

Epoch 3 only (from 2026-08-03), live strategies:

| strategy | window | return | SPY | excess |
|---|---|---|---|---|
| flagship-1d | 08-03 -> 08-26 | +0.46% | +1.11% | **-0.65pp** |
| flagship-1w | 08-03 -> 08-26 | +1.35% | +1.11% | **+0.24pp** |
| flagship-1d-replay | stale to 08-18 | -0.05% | +1.29% | -1.34pp |
| flagship-1w-replay | stale to 08-18 | -0.13% | +1.29% | -1.42pp |

### The number that must NOT be quoted
Daily excess returns, epoch 3:

| lane | n | mean daily excess | annualised | t |
|---|---|---|---|---|
| flagship-1d | 14 | +0.1263% | **+31.8%** | **+0.22** |
| flagship-1w | 14 | -0.0492% | -12.4% | -0.25 |

`flagship-1d`'s mean daily excess annualises to **+31.8%**, within sight of this
goal's 40-50% aspiration -- on **t = 0.22 with n = 14**, and with a CUMULATIVE
excess of the OPPOSITE SIGN (-0.65pp). Neither lane clears |t| >= 2, let alone
the |t| >= 2.50 a Bonferroni correction over the 4 strategies tested demands.

That +31.8% is exactly the false positive this repo's methodology exists to
prevent, and it is exactly what a search over 4 lanes on 14 observations
produces by chance. **It is noise. Do not quote it.**

**Verdict: no lane demonstrates SPY outperformance.** Three of four trail SPY;
the fourth leads by 0.24pp on 23 days and fails every significance test. The
honest statement about the highest proven mode is that it is running, it is
measured, and it shows no edge.

## E20 — Lane coverage: crypto validated independently. Same verdict.
The goal asks that each supported market/horizon lane be validated on its own. I
had only examined stocks. Crypto is a real lane, not just ingestion: 7 active
symbols, 462,795 bars current to 2026-08-27, 31,772 predictions, 48,459 resolved
outcomes, 56 confluence rows, 8 paper trades.

Day-weighted (one observation per day, because 7 crypto names share one market
move -- the pseudo-replication guard that turned +0.332% into -0.435% on the
stock book):

| horizon | rows | days | accuracy | naive base | edge | t |
|---|---|---|---|---|---|---|
| 1d | 12,967 | 36 | 42.71% | 49.60% | **-6.89pp** | -0.85 |
| 1w | 18,030 | 43 | 46.64% | 58.30% | **-11.66pp** | -1.41 |

### Checked for the identity trap before concluding
CLAUDE.md records that the -13/-23/-28pp directional figure is an ARITHMETIC
IDENTITY (acc = 1 - null) on a one-sided book, not anti-skill. Tested here:

| horizon | calls UP | actual UP | accuracy | 1-base | gap |
|---|---|---|---|---|---|
| 1d | 34.2% | 43.5% | 47.1% | 56.5% | 9.38pp |
| 1w | 25.9% | 54.4% | 52.6% | 45.6% | 7.01pp |

The book is TILTED short but not one-sided, and accuracy sits 7-9pp away from
`1 - base`, so this is not the clean identity. The deficit is real rather than
arithmetic -- but at |t| = 0.85 and 1.41 it is **not statistically
significant either way**.

**Crypto verdict: no demonstrated edge, and no demonstrated anti-skill.**
Identical in kind to the stock lanes. Do NOT invert it -- that is the refuted
dircall program.

### Lane coverage now complete
| lane | verdict |
|---|---|
| stocks 1d | book SHUT by its own gates (E9); forecast RankEdge -0.0434 |
| stocks 1w | only lane with an admitted leg (+0.0048); 635 symbols blending |
| crypto 1d | below base rate, not significant (t=-0.85) |
| crypto 1w | below base rate, not significant (t=-1.41) |
| paper flagship-1d | -0.65pp vs SPY; daily t=+0.22 (E19) |
| paper flagship-1w | +0.24pp vs SPY; daily t=-0.25 (E19) |
| paper 1d/1w-replay | dormant 8 days; both negative excess |

Every lane independently validated. **Not one shows a significant edge.**

## E21 — Cost and integrity requirements: verified, and honestly flagged where not clean
The goal requires testing fees, spread, slippage, liquidity, partial fills,
borrow, corporate actions, point-in-time and survivorship. Checked each:

| requirement | status | evidence |
|---|---|---|
| Spread | MODELLED | `papertrade/execution.go`: `cost = notional * (halfSpread + impact)`, stored as `SpreadBps` per side |
| Market impact / slippage | MODELLED | sqrt-scaling impact; sizing solves `N*(1+spread+impact(N)) = budget` as a contraction |
| Liquidity + partial fills | MODELLED | notional CAPPED by bar volume, filling SHORT with `UnfilledNotional` rather than pretending the rest filled |
| Fees applied in practice | YES | all **509** paper trades carry a nonzero `cost`; total **$3,560.81**, mean **$7.00**/trade |
| Corporate actions | HANDLED | `barAdjustment = "split"` at the vendor; `split-repair` ran 2026-08-27: `scanned 322, contaminated 25, repaired 0, confirmed-real 1` |
| Borrow | NOT APPLICABLE | **0** negative-qty paper positions -- the book is long-only, so there is no borrow exposure to model. The 245 `sell` trades are exits, not shorts |
| Survivorship | MEASURED, NOT CLEAN, and SAID SO | 738 of 2621 inactive symbols carry no `delisted_at`; the accuracy registry publishes `survivorship_clean=false` and a bound ("at most 0.70pp") rather than hiding it |
| Leakage / embargo / purge | PRESENT | `tools/controls_evidence.py`, `alphax.go` (E12) |

This validates E19's "after-cost" claim, which I had asserted before checking --
it holds: the paper equity curves are net of modelled spread, impact and
capped fills, not gross.

The one genuinely soft spot is survivorship, and the platform already declares
it rather than quietly assuming it away. That is the correct handling of a
limitation that cannot be closed from the data on hand.

## E22 — NEW DEFECT: the structural lane is silently unresolvable, and survivorship-biased
Found by completing the instrument coverage the goal asks for. `regime_outcomes`
is a whole instrument class (58,206 rows, 7 kinds) that I had not examined.

### It cannot be graded against its own baseline
| bucket | rows |
|---|---|
| resolved AND has a naive baseline (**gradeable**) | **2** |
| resolved, no baseline | 5,421 |
| baseline, never resolved | 42,013 |

Two gradeable rows out of 58,206. This is why the accuracy registry publishes
*"every structural kind grades NO BASELINE"* and refuses to print a figure.

### The raw accuracies are exactly the trap
Ungraded against any baseline, the lane reports: `trend21` **80.81%**,
`liquidity21` 72.74%, `vol21` 66.83%, `trend21-crypto` 92.16%,
`liquidity21-crypto` **100.00%** (n=51). These are the numbers someone would
quote as proof SignalDeck works. Without a baseline they mean nothing: if the
market trends up on 80% of days, calling "trend up" every day scores 80%.

### Root cause: resolution stalled 31 days ago, silently
Resolution stopped at **2026-07-26** while 1,100-1,900 new rows/day keep
arriving through 2026-08-26. `regime-outcome-runner` reports **`ok`** on every
run with `resolved 0`.

Measured against the resolver's own predicate
(`ts + horizon_days*1.45*86400 <= now`):

| | count |
|---|---|
| rows DUE right now | **2,288** |
| ...on INACTIVE symbols | **2,267** |
| ...with enough forward bars to actually grade | **0** |

A 21-day horizon needs ~21 forward daily bars. These rows sit on symbols the
universe sweep PRUNED, and an inactive symbol stops receiving bars, so they can
never reach the bar count. The resolver's
`continue // not enough forward bars yet -- stays unresolved, retried later`
is correct per-row and wrong in aggregate: "later" never comes. They retry
forever and accumulate in silence.

### Why this matters beyond the stall
It is a **survivorship bias**, not just a stuck worker: the structural record can
only ever grade symbols that STAYED in the universe. Every pruned symbol is
excluded, uncounted, and invisible. That is precisely the leakage/survivorship
failure this goal requires be tested for, and it was live.

### The principled fix (not applied - see below)
Mark a due row UNGRADABLE with a reason once it can no longer reach its bar
count (symbol inactive, or past some multiple of its horizon), and REPORT the
count. That converts a silent stall into a measured exclusion, matching patterns
the repo already uses: `confluence_outcomes.ungradable` and the stale-feed
quarantine, both of which say what they dropped and why.

NOT implemented this pass: it changes grading semantics in a subsystem another
session has been editing, and a wrong cutoff would silently discard gradeable
rows -- the opposite failure. Recorded with the exact measurements so it can be
done deliberately rather than guessed at.

## E23 — E22 REPAIRED (082e6dc): unresolvable regime calls are retired and counted
The defect recorded in E22 is fixed rather than deferred. My stated reason for
deferring -- "another session is editing this subsystem" -- was WRONG:
`git log` shows `regimeoutcomes.go` last touched **2026-08-08**, nineteen days
ago. Checking that instead of assuming it is what unblocked the work.

**The change.** Past `regimeAbandonMultiple` (3x) its horizon, a still-
unresolvable due row is marked `ungradable` WITH A REASON, leaves the due queue,
and is COUNTED in the worker summary (`retired N ungradable`). The row keeps
every frozen byte, exactly as `confluence_outcomes.ungradable` keeps its
`entry_px`.

The due queue admits at 1.45x, so 3x is a wide grace window: a merely slow
symbol, or one the sweep prunes and later re-admits, still grades normally.
Schema rides the pragma-guarded ALTER path, and pre-existing rows keep NULL
("still gradeable"), so no existing verdict changes.

**Tests** (`regimeungradable_test.go`): retires past the window and counts it;
does NOT retire inside it; still grades a slow symbol whose bars arrive late;
retirement is idempotent.

### A flaw in my own test, caught by mutating in both directions
The first mutation -- multiplier 100000, i.e. never retire anything -- **PASSED**.
The test computed its clock as
`callTs + ungradableHorizon*regimeAbandonMultiple*86400`, deriving it from the
very constant it was guarding, so widening the constant moved the clock with it.
A test that cannot fail when the value it guards changes is not guarding it.

Fixed to a literal 5x. Both mutations now fail:

| mutation | meaning | result |
|---|---|---|
| `regimeAbandonMultiple = 100000` | never retire | **FAIL** (past-window test) |
| `regimeAbandonMultiple = 0` | retire everything | **FAIL** (inside-window test) |

This is the second time this session that mutating a guard in only ONE direction
would have shipped a test that proves nothing. Mutate both ways.

`go vet` clean, `go test ./...` 124 packages ok. Deployed and VERIFIED on
082e6dc (`worker_runs.revision` == HEAD).

**Production confirmation PENDING**: `regime-outcome-runner` is mid-pass over
the 2,288 due rows at the time of writing. The retirement count will appear in
its summary; until that row settles this is verified by test and deploy, not by
production observation, and is recorded as such rather than claimed.

## E24 - E23's fix changed nothing in production, and what that revealed
Verified rather than claimed, and it is the THIRD "shipped fix that changed nothing" this session. The pass after deploying commit 082e6dc read: froze 0 ... resolved 0 ... retired 0 ungradable, with zero rows marked.

**The fix is correct; it simply had not fired.**

| Metric | Value |
|---|---|
| oldest due row | 2026-07-17 (40 days old) |
| grace window, 21-day horizon at 3x | 63 days |
| retirement therefore begins | 2026-09-18 |

retired 0 was the RIGHT answer on 2026-08-27, not a failure; reporting E23 as "working in production" without checking the arithmetic would have been wrong in the flattering direction.

### What it revealed: retirement was never the complaint
E22's defect was SILENCE - the worker regime-outcome-runner printed a bare "resolved 0" for 31 days while 2,288 rows sat unresolvable. A deliberately wide grace window defers RETIREMENT and so also defers VISIBILITY by another three weeks. Commit 082e6dc fixed accumulation and left the silence in place.
Commit 5fb3cdd makes every pass report how many due rows could not be graded for want of forward bars, whether or not they are old enough to retire. The fleet now says "2288 due but stuck short of forward bars" instead of "resolved 0". No row retires earlier, no gate is weakened, and the hole is visible today rather than on 2026-09-18.
Tests: the stuck count appears on the FIRST pass long before anything is retirable, and reads 0 when everything grades so it cannot cry wolf. Mutation-checked - dropping the increment fails the visibility test.

### The session pattern, now three for three

| fix | deployed green | actually changed behaviour? |
|---|---|---|
| A14 track-record gate (earlier session) | yes | **no** - gated on one condition of two |
| e5393b9 LLM models | yes | **no** - config.go held a duplicate |
| 082e6dc regime retirement | yes | **no** - grace window not yet elapsed |

every one looked finished at the commit and was caught only by going and measuring the running system afterwards; a green deploy proves the binary changed but never proves the behaviour did.

### Production confirmation (2026-08-27 03:04, build 5fb3cdd)

The post-deploy pass, read from `worker_runs`:

```
froze 0 regime calls (0 without a naive baseline, 2 abstained on a tied null),
resolved 0 (0 wrong, 0 high-conviction postmortems), retired 0 ungradable,
2288 due but stuck short of forward bars
```

**2288** matches the measured due count exactly. `retired 0` remains correct
until 2026-09-18. The 31-day silence is broken: the worker now names the hole
on every pass instead of printing a bare `resolved 0`.

This is the first of the three fixes in the table above to move a production
number on the first check after shipping - because it was the one written
AFTER measuring what the previous fix actually did, rather than after assuming
it had worked.

## E25 - the structural lane is not broken; it grades for the first time TODAY

E22 recorded 2 gradeable rows of 58,206 and I read that as a lane that could
not be graded. The fuller measurement says otherwise, and the explanation is
an accident of two dates one day apart:

| | |
|---|---|
| `naive_label` introduced | **2026-07-27** |
| resolution stalled | **2026-07-26** |

Every RESOLVED row predates the baseline; every BASELINED row postdates the
stall. The two sets barely intersect BY CONSTRUCTION, which is the whole of the
'2 rows' figure. The 5,421 resolved rows carrying no baseline are therefore
honest history, not a defect: the column did not exist when they were called.

### The transition is now

| horizon | baselined rows | earliest becomes due |
|---|---|---|
| 21d | **27,060** | **2026-08-27 (today)** |
| 63d | 8,925 | 2026-10-26 |

Sampling the 300 earliest 21-day baselined rows: **282 on ACTIVE symbols** and
**285 already hold >= 21 forward bars**. They are ready to grade.

So the lane produces its first baseline-comparable rows today, and ~27k over
the coming month. Only then can `trend21`'s ungraded 80.81% headline be judged
against anything -- which is exactly why the registry has refused to print it.

### Why the repairs landed at the right time

The ~5% of that cohort that is inactive or bar-short is precisely what
082e6dc/f10646b now retire WITH A REASON instead of accumulating silently, and
5fb3cdd makes the stuck count visible every pass. Without them this transition
would have been mixed in with 2,288 permanently-stuck legacy rows and reported
as a bare `resolved 0`.

**Revises E22.** The lane was never permanently ungradable; it was one day of
calendar misalignment plus a silent stall. The stall is fixed and the
misalignment ages out on its own.
