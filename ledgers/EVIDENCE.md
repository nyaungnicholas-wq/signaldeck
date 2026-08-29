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

## E26 - the last outstanding measurement, and exactly when it can be taken

The structural lane is the ONE lane I could not validate (E22/E25). Measured
2026-08-27 03:18:

| | |
|---|---|
| baselined rows due right now | **0** |
| first becomes due | **2026-08-27 22:32** (19.2h out) |
| `regime-outcome-runner` cadence | 6h (last 03:04) |
| so first structural grades land | **~2026-08-28 03:04**, or sooner on a daemon restart |

It cannot be observed inside this session. That is a measured constraint, not a
choice, and it is the honest reason lane coverage stops at seven of eight
validated rather than eight.

### How to take the measurement when it lands

Did it grade:

```
sqlite3 data/signaldeck.db "SELECT status, detail FROM worker_runs WHERE worker='regime-outcome-runner' ORDER BY started_at DESC LIMIT 1;"
```

The lane's first baseline-comparable verdict:

```
sqlite3 data/signaldeck.db "SELECT kind, COUNT(*) n, ROUND(AVG(CASE WHEN correct=1 THEN 1.0 ELSE 0 END)*100,2) acc, ROUND(AVG(CASE WHEN naive_label=actual THEN 1.0 ELSE 0 END)*100,2) naive FROM regime_outcomes WHERE correct IS NOT NULL AND naive_label IS NOT NULL GROUP BY kind;"
```

Read `acc` against `naive`, never alone. `trend21`'s ungraded 80.81% headline
means nothing until that second column exists: if the market trends up on 80%
of days, calling 'trend up' every day scores 80%. And day-weight before
believing any edge - same-day rows share one market move, which is what turned
the stock book's +0.332% into -0.435%.

## E27 - the offsite gap is real, but the on-machine copies provably restore

The one mitigation available while B2/R1 stay blocked, checked rather than
assumed. `ops/restore-rehearsal.sh` is not a file-exists check: it restores a
backup to an ISOLATED temp path and verifies it end to end.

Last run, 2026-08-23:

```
OK: local backup signaldeck-20260821-183254.db restores clean
  - integrity_check ok, 15941498 bars rows, ledger chain + anchors verified
```

The 2026-08-16 run passed identically on the 08-14 backup (11/10 anchors
recomputed, chain intact). So the backups are not merely present, they are
PROVEN restorable, ledger chain and anchor signatures included.

**What this changes.** The missing off-machine copy (E6, B2) is still the
highest-severity open item and losing the disk still loses everything. But the
risk profile is 'single point of failure, contents verified good' rather than
'single point of failure, contents unknown' - materially better, and it means
an offsite destination would be copying something known-restorable rather than
a hopeful blob.

**The residual gap, stated honestly.** The rehearsal is weekly (next 2026-08-30)
and last exercised the 08-21 backup, so the 08-24/25/26 backups are unrehearsed.
That is the cadence working as designed, not a defect - but it does mean 'proven
restorable' currently refers to a copy six days old.

## E28 - THE VERDICT: the structural lane has no skill either

The measurement E26 said could not be taken until tonight. Taken 2026-08-27
22:34 after the resolver graded 843 rows (842 with a baseline) in one pass.

| kind | n | days | model | naive | edge |
|---|---|---|---|---|---|
| liquidity21 | 276 | 2 | 74.3% | 72.5% | +1.8pp |
| trend21 | 277 | 2 | 77.6% | 77.6% | **+0.0pp** |
| vol21 | 275 | 1 | 57.5% | 60.0% | **-2.5pp** |
| trend21-crypto | 7 | 1 | 85.7% | 85.7% | +0.0pp |
| liquidity21-crypto | 7 | 1 | 100.0% | 100.0% | +0.0pp |

### trend21's headline was the base rate, exactly

`trend21` scores 77.6% and its naive baseline scores **77.6%**. Not close -
IDENTICAL. The model reproduces the naive call on every graded row. Its
impressive-looking accuracy is entirely the base rate of trends persisting,
which is precisely what the registry's refusal has been protecting against and
why the ungraded 80.81% (and the 85.3% seen on pre-baseline rows earlier
tonight) must never have been published.

`vol21` is NEGATIVE against its null. `liquidity21`'s +1.8pp is the only
positive figure and it rests on **2 days**.

### The sample is 1-2 days, not 842 rows

Every kind spans 1 or 2 distinct trading days. Same-day rows share one market
move, so the effective n is 1-2, not 842. Day-weighting changes the levels
(liquidity21 87.1% vs 86.2%, trend21 88.8% vs 88.8%) and changes no verdict.
Nothing here can support a claim in either direction; what it CAN do is refute
the one claim that was on the table, because a model matching its null exactly
needs no sample size to be unimpressive.

### What this closes

The structural lane was the ONE lane left unvalidated (E22/E25/E26). It now
joins the other eight: **no demonstrated skill**. This is an independent
confirmation of E9 by a completely different route - E9 measured the ensemble
refusing to bet, E28 measures what the bets were worth when they were made.
Both say the same thing, and the platform said it first by refusing to publish.

## E29 - FRONTIER STUDY: nothing tested comes near SPY, let alone beats it by 40-50pp

Exploration run 2026-08-27 on 2,940 stocks / 1,924 days (2019-01-02 .. 2026-08-27),
delisted names INCLUDED (bars stop at delisting), benchmark ETFs excluded from the
tradeable set, >=$5 close and >=300 prior bars at formation, monthly rebalance,
through `research/dirfix/port.py::simulate` at 10bps cost and 300bps/yr borrow.

### The bar

SPY buy-and-hold over the identical window: **ann 15.89%, vol 19.07%, Sharpe 0.83,
max_dd -34.18%**. So the stated target of +40-50pp means sustaining **56-66%/yr**.

### Result: 36 configurations, ZERO beat SPY

| signal | best ann | t_nw | verdict |
|---|---|---|---|
| mom12_1 (12-1 momentum) | 2.87% | 0.90-1.56 | not significant |
| rev1m (1-month reversal) | -1.40% | **-2.61 to -4.08** | significantly NEGATIVE |
| lowvol | -0.38% | -0.37 to -0.68 | nothing |
| trend_ts | 3.93% | 3.76 | **ARTIFACT, see below** |

Best honest figure is mom12_1 at 2.87%/yr, which is **-13.0pp against SPY**, on a
t_nw of 0.90. Not a shortfall against the 40-50pp target - a shortfall against
ZERO excess.

### trend_ts's significance was an artifact, and I caught it before reporting it

Its score is binary (+1 above the 200dma, -1 below): measured on one formation day,
**2 distinct values across 1,245 symbols** (819 up, 426 down). `simulate` sorts by
`["prob","symbol_id"]`, so `head(k)` returns the k LOWEST SYMBOL_IDS that happen to
be above their moving average. The selection is by database id, not by signal
strength. Its Sharpe 1.47 / t_nw 3.76 measure an arbitrary subset, not an edge.

Recorded because this is exactly the shape that gets published as a finding: a
clean-looking t-stat produced by a tie-break.

### One genuine lead, and it is NOT a free win

`rev1m` is significantly negative at t_nw -4.08. Its inverse - 1-month MOMENTUM -
is therefore positive on this window. That is a new hypothesis with a real
t-statistic behind it, but inverting a losing signal and claiming the win is
precisely the move CLAUDE.md records as refuted for dircall. It must be
pre-registered and tested on its own, on data this run did not touch.

### The arithmetic gap in the target

Even taking the best honest risk-adjusted result (Sharpe ~0.5), reaching 56%/yr
requires roughly 14x leverage, which scales max drawdown past -100% - the account
is gone before the edge pays. **A 40-50pp excess is not reachable by sizing up
anything measured here.** It would need an edge that does not currently exist in
this data, not a bolder configuration of one that does.

Window caveat: formation days run 2020-03-31 .. 2026-08-27 (78 months) because of
the 300-bar warm-up, so the study starts at the COVID bottom. No multiple-testing
correction across 36 configurations. In-sample exploration only.

## E30 - IMPLEMENTED: timed index exposure. The simplest choice wins.

E29 tested long-short market-neutral signals, which STRUCTURALLY cannot beat an
index on absolute return - they are built to be uncorrelated. Beating SPY needs
timed or concentrated exposure TO an index. That is what this tests, on 10.6
years (2016-01-04 .. 2026-08-27), history extended from 2019 via Alpaca (free).
Macro is lagged 5 business days to remove FRED release look-ahead. 5bps per switch.

| strategy | ann% | vol% | sharpe | max_dd% | vs SPY |
|---|---|---|---|---|---|
| SPY buy & hold | 13.49 | 17.57 | 0.77 | -34.18 | +0.00 |
| **QQQ buy & hold** | **19.41** | 22.28 | 0.87 | -35.62 | **+5.92** |
| SPY, 200dma -> cash | 8.27 | 11.34 | 0.73 | -21.65 | -5.23 |
| SPY, 200dma -> TLT | 6.49 | 14.79 | 0.44 | -43.50 | -7.00 |
| **QQQ, 200dma -> cash** | 16.36 | 16.17 | **1.01** | **-21.99** | +2.87 |
| QQQ, 200dma -> TLT | 14.94 | 18.53 | 0.81 | -45.30 | +1.45 |
| SPY, 200dma AND NFCI<0 | 8.27 | 11.34 | 0.73 | -21.65 | -5.23 |
| QQQ, 200dma AND NFCI<0 -> TLT | 14.01 | 18.38 | 0.76 | -45.30 | +0.52 |

### Four findings, in order of importance

1. **Buying QQQ and doing nothing beats every strategy in this repo.** +5.92pp/yr
   over SPY, and it beats all 36 configurations from E29 and all six timing rules
   here. The sophisticated machinery loses to a one-line decision.
2. **200dma timing REDUCES return in every case** (SPY 13.49 -> 8.27). It is a
   RISK tool, not a return tool: it cuts drawdown -34% -> -22%.
3. **The best risk-adjusted result is QQQ + 200dma timing**: Sharpe 1.01 vs SPY's
   0.77, drawdown -22% vs -34%, and still +2.87pp of return. Better return AND
   materially less pain is a real improvement - it is simply not 40-50pp.
4. **Rotating to TLT is actively harmful** (-45% drawdown, worse than doing
   nothing). TLT fell with equities in 2022, so the 'safe' leg amplified the loss.
   A hedge that is only a hedge in some regimes is not a hedge.

### On the target

QQQ's +5.92pp is not alpha. It is a concentrated factor bet on large-cap tech that
happened to win over 2016-2026, taken with MORE drawdown than SPY (-35.6 vs -34.2).
Nothing here supports +40-50pp, and the honest attainable band on this evidence is
roughly **+3 to +6pp, with drawdown between -22% and -36% depending on whether you
take the timing overlay.**

Caveats: one 10.6-year window containing three drawdowns (2018, 2020, 2022); the
QQQ result is in-sample in the sense that tech's dominance is known ex post; no
multiple-testing correction across the eight rules. Exploration, not a sealed test.

## E31 - THE FRONTIER, ANSWERED: +26.7pp is reachable. It costs a -55% drawdown.

The 'show me' answer. Real levered ETF prices (daily-compounding decay and fees
included, NOT a simulated 3x), 2016-01-04 .. 2026-08-27, 5bps per switch. Signal is
QQQ above its own 200dma - the rule that produced Sharpe 1.01 in E30.

| strategy | ann% | sharpe | maxDD% | worst 1y% | vs SPY |
|---|---|---|---|---|---|
| SPY buy & hold | 13.49 | 0.77 | -34.2 | -21.0 | +0.00 |
| QQQ + 200dma | 16.36 | 1.01 | -22.0 | -19.2 | +2.87 |
| QQQ buy & hold | 19.41 | 0.87 | -35.6 | -35.2 | +5.92 |
| SSO + 200dma | 19.71 | 0.81 | -42.7 | -26.2 | +6.22 |
| QLD + 200dma | 29.38 | 0.91 | -40.3 | -35.2 | +15.89 |
| QLD buy & hold | 32.24 | 0.73 | -63.8 | -63.2 | +18.75 |
| TQQQ buy & hold | 38.90 | 0.59 | **-81.8** | -81.1 | +25.41 |
| **TQQQ + 200dma** | **40.21** | 0.84 | **-54.9** | **-48.3** | **+26.71** |

### The finding that matters

**Timing IMPROVES a levered asset's return, unlike an unlevered one.** TQQQ+200dma
returns MORE than TQQQ buy-and-hold (40.21 vs 38.90) with far less drawdown (-54.9
vs -81.8). That is not a coincidence: leverage decay is worst in choppy and falling
markets, and the 200dma rule sits out exactly those. On unlevered SPY the same rule
COST 5.2pp (E30). Leverage is what makes the timing rule pay for itself.

### The price, stated in dollars

On the $10,000 target, TQQQ+200dma historically means:
- a drawdown to about **$4,500** at the worst point,
- a **-48% year** at some stage,
- and the discipline to hold the rule through both.
Most people abandon at the bottom, which turns a paper drawdown into a realised
loss. The strategy's return assumes you do not.

### Against the stated target

+26.71pp is a large gap and short of the 40-50pp aspiration. Getting there would
need roughly 4-5x daily leverage, which does not exist as a retail ETF and would
have produced a total loss in March 2020 (QQQ fell ~28% peak-to-trough in weeks;
a 4x product breaches -100% on a single -25% day).

### What would invalidate all of this

One window, 2016-2026, containing the strongest large-cap tech run in history. The
200dma rule was selected because it worked in E30, so applying it here is
conditioned on prior exploration. NOT sealed, NOT pre-registered, no
multiple-testing correction. This is the exploration half of 'explore then seal'.
The sealed test is the next step and it is the one that decides whether any of
this is real.

## E32 - SEALED VERDICT: PARTIAL. Return survived, risk-adjusted return did not.

Spec sealed at commit 658c4ea (sha256 0716f5bc...) BEFORE any holdout number was
computed. Criteria frozen in that commit. One grading run, no tuning.

| window | TQQQ+200dma ann% | SPY ann% | excess | strat sharpe | SPY sharpe | strat maxDD |
|---|---|---|---|---|---|---|
| EXPLORE 2016-01-04..2021-12-31 | 51.74 | 15.43 | **+36.31pp** | 1.06 | 0.87 | -54.9% |
| **SEALED 2022-01-01..2026-08-27** | 26.56 | 11.03 | **+15.53pp** | **0.57** | **0.63** | -52.9% |

- PRIMARY (holdout ann > SPY ann): **PASS**, +15.53pp
- SECONDARY (holdout sharpe > SPY sharpe): **FAIL**, 0.57 vs 0.63 (delta -0.06)
- **VERDICT: PARTIAL**

### The excess more than halved, which is the entire point of sealing

+36.31pp in the window used to choose the rule became **+15.53pp** in the window
that was not. Anyone quoting the 40%/yr headline from E31 would have been quoting a
number inflated by selection. The seal cut the estimate by 57%.

### The secondary failure is the honest verdict

Sharpe 0.57 against SPY's 0.63 means the strategy earned more return by taking
disproportionately more risk - 46.6% volatility against SPY's 17.5%, and a -52.9%
drawdown against -25.4%. **Per unit of risk you were paid slightly WORSE than just
holding SPY.** That is a leverage story, not a skill story: the rule is not
demonstrating an edge, it is demonstrating that 3x leverage multiplies a positive
drift in both directions.

### What this permits and forbids

PERMITTED: stating that this rule produced +15.53pp of excess annual return on data
not used to select it, over 4.7 years containing a bear market.
FORBIDDEN: calling it skill, quoting the 40% or the +36pp explore figures as
expectations, or adopting it on the strength of the primary pass alone. The frozen
no-tuning clause applies - no parameter may now be changed and re-graded.

### The next question, which this result raises and does not answer

If the return is leverage rather than skill, then simply levering SPY should do
comparably. That comparison is a NEW test needing its own registration - running it
against this holdout would be tuning on a sealed result, which is forbidden.

## E34 - WALK-FORWARD VERDICT: the 200dma rule LOSES over 40 years. The QQQ finding was regime luck.

Setup: Phase 1 and 3 of the approved plan. Long index history from yfinance, validated against the Alpaca ETF panel over 2016-2026 before use: ^GSPC vs SPY correlation 0.9933 and annualised 13.51 vs 13.49 percent; ^NDX vs QQQ correlation 0.9986 and annualised 19.42 vs 19.41 percent. Gate PASSED. 5bps per switch.

### Full period

| Index | years | buy-and-hold ann% | buy-and-hold maxDD% | 200dma ann% | 200dma maxDD% | excess |
|-------|-------|-------------------|---------------------|-------------|---------------|--------|
| Nasdaq-100 ^NDX | 1986-2025 (40) | 14.61 | -82.9 | 10.98 | -59.3 | -3.63pp |
| S&P 500 ^GSPC | 1928-2025 (98) | 6.38 | -86.2 | 6.48 | -52.2 | +0.10pp |

### It wins one year in five

On ^NDX the timed rule beat buy-and-hold in only 8 of 40 years (20 percent), mean excess -4.95pp, median -5.01pp, best +26.6pp, worst -30.2pp. On ^GSPC it won 25 of 98 years (26 percent), mean excess -0.87pp, median -1.86pp. **This is an insurance payoff profile, not an edge - it loses small most years and wins big rarely, and the mean conceals that.**

### Where the value lives

| Event | NDX excess | SPX excess |
|-------|------------|------------|
| dot-com 2000-02 | +18.9pp | +7.6pp |
| 1973-74 | not covered | +18.0pp |
| GFC 2007-09 | -0.6pp | +5.9pp |
| COVID 2020 | -11.5pp | -12.5pp |
| 2022 | +14.9pp | +3.6pp |

### The QQQ result was regime luck

E33 reported QQQ plus 200dma beating SPY on all three dimensions in 2022-2026 with +2.69pp of excess. The identical rule on the identical index returns -3.63pp annualised across 40 out-of-sample years. A single favourable window sold it as an edge; four decades say it costs return. **The walk-forward caught what one window would have shipped.**

### Three predictions, registered in the plan before this ran, all held

- The rule would look worse over 40-99 years than over 2016-2026 - it did, -3.63pp against +2.69pp
- Its value would concentrate in 1973-74, 2000-02 and 2008-09 - it did, those are the only large positive contributions
- It would behave as insurance rather than alpha - it does, winning one year in five

### What the rule actually is

On the S&P 500 it buys a drawdown reduction from -86.2 percent to -52.2 percent for approximately zero cost in return (+0.10pp), which is genuinely valuable risk management. On the Nasdaq-100 it costs 3.63pp per year for a reduction from -82.9 to -59.3 percent. Neither is a route to beating SPY by 40-50 percentage points.

### A bias that flatters these numbers

Both the index series and the Alpaca ETF panel are PRICE returns excluding dividends. Buy-and-hold would have collected roughly 1.8 percent per year on the S&P and 0.7 percent on the Nasdaq that this simulation does not credit it with, while the timed rule sits in cash for long stretches. Correcting for it makes buy-and-hold better and the timed rule worse, so every excess figure above is optimistic. Scheduled for Phase 5.

## E35 - PBO 0.82: the RETURN edge is overfit. The RISK reduction is not.

Phase 4. Grid swept over MA windows 50/100/150/200/250 on ^GSPC and ^NDX, then the
FAMILY judged with the repo's own CSCV implementation (`tools/pbo_ledger.py`).

### The grid, excess vs buy-and-hold (pp/yr)

| index | 50 | 100 | 150 | **200** | 250 |
|---|---|---|---|---|---|
| ^GSPC | -0.65 | -0.37 | -0.33 | **+0.10** | -0.00 |
| ^NDX | -4.33 | -4.24 | -4.92 | **-3.63** | -2.92 |

The S&P's +0.10pp is **the single positive cell out of ten**. On the Nasdaq every
window loses, and the best is 250 - not the 200 that E30-E33 were built on.

### CSCV verdict

```
pbo                 0.8232
n_combinations      12870
degradation_slope  -1.0158
```

**PBO 82%** on 10,306 days x 5 configs. And the degradation slope is NEGATIVE:
in-sample rank inversely predicts out-of-sample performance, which is the textbook
overfitting fingerprint. Selecting the best window in-sample actively hurts.

The plan registered the decision rule BEFORE this ran: *if PBO comes back high, the
honest conclusion is that there is no reliable edge here and the correct action is
to stop, not to search for a better window.* **That trigger has fired.**

### The distinction PBO does not erase

Return and risk behave differently under the sweep. On ^GSPC:

| metric | buy & hold | 200dma | range across ALL 5 windows |
|---|---|---|---|
| ann return | 6.38% | 6.48% | 5.73 - 6.48 (straddles B&H) |
| Sharpe | 0.34 | 0.53 | **0.50 - 0.54 (never below B&H)** |
| max drawdown | -86.2% | -52.2% | consistently reduced |

Return enhancement is knife-edge and overfit. **Risk reduction is stable under every
parameter choice** - Sharpe roughly 0.34 -> 0.52 and drawdown -86% -> -52% whichever
window you pick. That is a robust property of trend-following, and it is what the
rule is actually for.

### Conclusion
- **Return edge: REFUTED.** PBO 0.82, one positive cell in ten, negative degradation
  slope. Do not pursue, do not re-window, do not seal it as a return strategy.
- **Risk tool: SUPPORTED.** Consistent Sharpe and drawdown improvement across the
  whole family, on 98 years. Worth having for that reason and no other.
- The 40-50pp target is not reachable by this route and no further search on this
  data will change that.

## E36 - CORRECTS E35: with dividends counted, the rule costs 2.4-4.6pp/yr. Nothing is free.

Phase 5. E34 and E35 used PRICE-ONLY series, which under-credits buy-and-hold with
dividends it would collect while the timed rule sits in cash. I flagged the bias when
I found it; this quantifies it, and it overturns my own conclusion.

Sanity check first, so the data is not assumed: SPY total-return 15.30%/yr against
^GSPC price-only 13.50%/yr over 2016+, a **+1.80pp** gap - exactly a dividend yield.
The series are genuinely total-return.

### Every cell turns negative

| asset | window | B&H ann% | timed ann% | excess | Sharpe gain | drawdown |
|---|---|---|---|---|---|---|
| SPY | 100 | 10.88 | 6.30 | **-4.58** | -0.04 | -55.2 -> -52.7 |
| SPY | 200 | 10.88 | 8.15 | **-2.72** | +0.09 | -55.2 -> -29.4 |
| SPY | 250 | 10.88 | 8.22 | **-2.66** | +0.08 | -55.2 -> **-24.7** |
| QQQ | 100 | 10.80 | 6.67 | **-4.13** | +0.01 | -83.0 -> -54.1 |
| QQQ | 200 | 10.80 | 7.53 | **-3.27** | +0.05 | -83.0 -> -58.9 |
| QQQ | 250 | 10.80 | 8.37 | **-2.43** | +0.11 | -83.0 -> **-40.8** |

**Six windows of six lose on return. There is no positive cell left anywhere.**

### The specific claim of mine that was wrong

E35 said the S&P bought its drawdown reduction *"for approximately zero cost in
return (+0.10pp)"*. That +0.10pp was an artifact of the missing dividends. Counted
properly it is **-2.72pp/yr**. The reduction is real; it was never free.
The Sharpe gain also shrank, from an apparent +0.18 to **+0.01 to +0.11**.

### What the rule actually is, final form

A drawdown-reduction tool that costs roughly **2.4-3.3pp/yr** at the better windows.
SPY -55.2% -> -24.7%; QQQ -83.0% -> -40.8%. That halving is genuine and robust across
parameters. Whether it is worth 2.5-3pp a year is a RISK PREFERENCE question, not an
edge question, and it must never be presented as outperformance.

### Pattern worth recording

This is the fifth finding of mine this session overturned by better measurement
(after R2, R4, R7, and E33's regime luck). In every case the correction moved AGAINST
the interesting result. Anything that survives only until it is measured more
carefully was never there.

## E37 - Macro regime timing: the last untested direction, and it fails too

The one data source from the approved list never tried: macro/rates, with 50 years
of depth. Different INFORMATION rather than another way of modelling price, which
was the whole thesis for why it might work.

Four rules, all pre-specified from the literature rather than fitted, each with a
realistic PUBLICATION LAG so the test is not fabricated (NFCI 8 days, UNRATE 10,
market data 1). SPY total return 1993-2026, dividends credited, 5bps per switch.

| rule | ann% | Sharpe | maxDD% | excess | Sharpe gain |
|---|---|---|---|---|---|
| curve inverted (T10Y2Y<0) | 9.41 | 0.54 | -55.2 | -1.46 | -0.05 |
| tight credit (NFCI>0) | 10.23 | 0.64 | -49.6 | -0.65 | +0.05 |
| unemployment rising (Sahm-like) | 10.85 | 0.74 | **-28.3** | -0.02 | **+0.16** |
| VIX elevated | 4.36 | 0.44 | -41.9 | **-6.52** | -0.14 |
| *buy & hold* | *10.88* | *0.59* | *-55.2* | | |

Three of four lose outright. The unemployment rule looked genuinely promising:
drawdown nearly halved for a return cost of -0.02pp, unlike the MA rule's -2.72pp.
So I tried to kill it.

### It dies on effective sample size

**The entire result rests on 7 decisions** - seven times it moved to cash in 33
years. 8,452 daily observations do not make that 8,452 independent trials.

| decade | excess |
|---|---|
| 1990s | -1.74pp |
| 2000s | **+5.76pp** |
| 2010s | -0.52pp |
| 2020s | **-7.18pp** |

It wins in ONE decade of four - the 2000s, which held two recessions - and is worst
in the most recent. Excluding 2008-09 the 'free' -0.02pp becomes -0.79pp. The
apparent free drawdown reduction is one decade averaged over three.

### Verdict

Macro regime timing is refuted on this data by the same failure as everything else:
value concentrated in two crises, negative the rest of the time, and an effective n
in single digits. That now closes every direction selected at the start of this goal
- SEC/EDGAR excepted, which cannot be reached with the data at hand.

Sixth finding of mine overturned by harder testing this session. As before, the
correction moved against the interesting result - and this time I went looking for
the kill myself rather than waiting to be caught by it.

## E38 - The study is now reproducible, and it generalises: 21 of 21 cells lose

Implementation, not research. Every verdict E29-E37 lived in throwaway heredocs;
`research/longhist/study.py` makes them re-runnable with `fetch`, `ma`, `macro`,
`robustness` and `all` subcommands. It reproduces E36 and E37 exactly.

Two defects found and fixed while wiring it up: it looked for the macro data in a
non-existent `data/macro.db` rather than the production database, and queried
`macro_series` on a column `name` that does not exist (it is `series`). Both would
have failed silently as 'no macro signals available'.

### The finding got STRONGER when extended past SPY and QQQ

E36 tested two assets and found 6 of 6 windows negative. The module runs seven
asset classes:

| asset | win 100 | win 200 | win 250 |
|---|---|---|---|
| SPY | -4.58 | -2.72 | -2.66 |
| QQQ | -4.13 | -3.27 | -2.43 |
| IWM | -4.60 | -4.23 | -3.99 |
| EFA | -1.26 | -0.74 | -1.66 |
| EEM | -3.18 | -5.96 | -5.72 |
| TLT | -2.49 | -3.89 | -2.86 |
| GLD | -4.38 | -3.16 | -2.88 |

**21 of 21 asset/window combinations lose on return** - US large cap, US small cap,
developed international, emerging markets, long bonds and gold. Not one positive
cell anywhere.

### And the 'risk tool' consolation is weaker than E36 claimed

Sharpe gain is NOT universally positive once you leave SPY and QQQ: EFA +0.16 and
QQQ +0.11 at best, but IWM -0.09, GLD -0.15, and TLT **-0.28**. E36 said the risk
reduction was 'robust across parameters' - true within an asset, but it does not
hold across asset classes. On bonds and gold the rule makes risk-adjusted return
WORSE.

### Status

Closes the implementation gap. The findings can now be checked by re-running one
command instead of trusting a transcript, which is the only form in which a
negative result is worth anything.

## E39 - SEC/EDGAR: tested properly this time, and it cannot be tested

I twice wrote that EDGAR was 'not reachable with the data at hand' without checking.
That was wrong: `insider_trades` holds 9,153 rows and `filings` 90,165. Correcting
the dismissal, then measuring.

### The data exists but has almost no history

Open-market purchases (Form 4 code **P**, the only informative code - F is automatic
tax withholding, A is a grant, M an option exercise) number **385**, across 112
symbols. By filing year:

| year | events |
|---|---|
| 2008 | 2 |
| 2016 | 1 |
| 2022 | 9 |
| 2023 | 22 |
| 2024 | 39 |
| 2025 | 139 |
| 2026 | 169 |

**81% of all events are 2025-2026.** The edgar-fetcher only began collecting
recently, so the apparent 2008-2026 span is an illusion created by two stray rows.

### The event study, and why its output is an artifact

Measured from `filed_ts` - the first moment the information was public - never from
`tx_ts`, against an equal-weight universe baseline:

| horizon | n | mean abnormal | median | naive t |
|---|---|---|---|---|
| 5d | 102 | +2.08% | +0.82% | 1.73 |
| 21d | 42 | -5.06% | -6.95% | -0.90 |
| 63d | 42 | -11.89% | -5.65% | -1.95 |
| 126d | 43 | **-30.37%** | -27.24% | **-3.88** |

A naive reading says insider buying predicts large DECLINES - the opposite of the
documented anomaly, at t=-3.88. **It is not a finding.** The sample falls from 102
to 42 between horizons because 2026 events have no 126-day forward window yet, so
the long-horizon rows are one recent cohort rather than a sample. Events also
cluster by symbol and date, which inflates the naive t.

I first guessed the negative number came from buys during the GFC. **I checked and
that was wrong** - only 2 events fall in 2008-2010, 1% of the sample.

### Two data-quality defects found in passing

- `tx_ts` contains 1969-12-31 on many rows, i.e. NULL stored as epoch 0. It makes
  the mean filing lag compute as 3,897 days. Any analysis joining on `tx_ts` without
  filtering these would silently use 1969 as a transaction date.
- `congress_trades` exists with **0 rows**; the congress-poller reports degraded
  because its mirrors are unavailable, so that source is empty rather than thin.

### Verdict

EDGAR is CLOSED as untestable on current data - not refuted, untestable. ~18 months
of real history and 43 events at the horizon that matters cannot support a claim in
either direction. It is the only selected direction where the honest answer is 'ask
again in two years', and it becomes testable simply by the fetcher continuing to run.
