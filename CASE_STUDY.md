# Building a market-research system that refuses to lie to me

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** SUPERSEDED — historical record; current status is `STRATEGY_DECK.md`
> **Scope:** Narrative account of how the platform was built and what it measured.
> **Frozen claim classes:** FC3 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE as a record, NOT as a current statement — see `STRATEGY_DECK.md`

> **Backtest data: `pre-survivorship-fix`.** Every historical figure below was
> computed on the universe as it stood BEFORE the 2026-08-04 survivorship
> backfill (`proofs/P3A_SURVIVORSHIP_BACKFILL.md`) and the point-in-time
> universe rebuild (`proofs/P3B_PIT_UNIVERSE.md`). It has not been re-run on
> the repaired universe. Read the numbers as a record of what was measured
> then, not as what the repaired data would produce now.

> ## ⛔ SUPERSEDED — publishable as a record, not as current status
> **P0 freeze 2026-08-04, lifted 2026-08-04** (`proofs/P10_FREEZE_LIFT.md`). Remediation
> is complete. This document is publishable as a record of what was measured and when;
> it **must not be quoted as *current* status** — quote `STRATEGY_DECK.md` for that.
>
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **live accuracy · confidence-interval verdicts ·
> survivorship control · point-in-time data · "already built / verified" status.**
>
> Known defects: it prints a live-record snapshot that three other documents state
> differently; and it narrates survivorship as closed while `ALPHA_WORKFLOW.md` §B2
> measures it open.
>
> **Closed in P6:** this document previously asserted "the entire day-clustered
> interval below the baseline" while the platform withholds every interval for
> insufficient distinct blocks. The interval verdict was removed — see
> `proofs/P6_GOVERNANCE_CLEANUP.md` §3.
>
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

SignalDeck is a market-intelligence platform I built to answer one question
honestly: **does any of this actually predict anything?**

Most trading projects answer that with a backtest. Backtests are easy to make
beautiful and almost impossible to trust — the failure modes (lookahead,
survivorship, overlapping samples, post-hoc slicing) all push results in the
same flattering direction. So the interesting engineering problem was never the
strategy. It was building a system that would tell me it had no edge, in public,
without me having to be honest in the moment.

This is a write-up of what that took, including the parts where the honesty
machinery caught me.

---

## What it is

A Go daemon ingests live market data (Alpaca for US equities, a separate Go
service for consolidated crypto order books), computes a decomposable score per
symbol and horizon, measures what usually happened next in comparable historical
states, and **grades its own past predictions against realised returns**. A
Next.js app serves the result. Everything lands in one SQLite database — 13.4M
bars and ~1.5M scored predictions at the time of writing.

```
Alpaca REST + WebSocket ──┐
Kraken OHLC ──────────────┼──▶ signaldeckd (Go, ~90 packages) ──▶ Next.js app
TickStream (crypto L2) ───┘         SQLite · worker fleet           /accuracy
```

**Stack:** Go 1.26, Next.js 16 / React 19, SQLite, Python for the research and
grading layer.

---

## The design idea: make dishonesty structurally hard

Anyone can write "be rigorous" in a README. The question is what happens at 2am
when a number looks bad and nobody is watching. The answer has to be
mechanical.

### 1. The grader is pinned by hash

The script that decides verdicts is registered in a hash chain inside the
database. On every run it hashes itself and compares:

```
UNREGISTERED GRADER: the chain pins graderSha256 e26d7568… at commit 65c865f4,
but this file hashes 6908c6f9…. Refusing to grade: the pinned grader and the
running grader are different code.
```

You cannot quietly improve the grader after seeing the results. Changing it
requires committing the change and letting a registrar append a chained
amendment — a permanent, ordered record that the rules moved and when.

I hit this myself while fixing a bug in the grader, and had to go through the
full amendment path to get my own fix accepted. That is the system working.

### 2. The daemon refuses to run unattributable builds

```
refusing to start: build is unattributable — rows it writes cannot be graded
revision_stamp=1a8c67ea…+dirty  vcs_modified=true
```

Every row is stamped with the commit that produced it. Build from a modified
working tree and the daemon exits rather than producing evidence nobody can
reproduce. Rows written by an unattributable binary later grade as **verdict
withheld** — the field is dropped entirely rather than downgraded, because an
absent verdict cannot be quoted as one.

### 3. Publication is fail-closed

If the grader cannot run, the README's accuracy tables are **removed** and
replaced with the refusal and its reason. It does not reprint yesterday's
numbers under today's date. A number graded by code that refused to run today is
not a live number.

### 4. Statistical guards, not statistical decoration

- **Day-clustered intervals.** ~1,000 symbols share one market move each day, so
  a raw row count massively overstates the evidence. Intervals resample days.
- **Purged and embargoed cross-validation** everywhere a model is fit, because
  overlapping forward-return windows leak.
- **Bonferroni correction across the whole search**, including across days — the
  divisor grows as searches accumulate, so grinding until something passes gets
  harder, not easier.
- **A prequential null.** Each day's baseline is the majority class over days
  strictly before it. Beating a null computed with hindsight is not skill.

---

## What it found about itself

The flagship directional ensemble — the thing the whole project was built
around — **graded FAILED and was retired**. Its full live record:

<!-- BEGIN GENERATED live_accuracy -->

Generated from `data/accuracy_registry.json` (grade of 2026-09-03T14:05:50) by `tools/live_accuracy.py`. Do not edit by hand — edit the registry or the generator.

> **STALE — this is not a current grade.** The registry is `REFUSED` (publication gate: the graded window contains 13 collapsed cross-section(s) of 58 day(s): 1d 2026-08-01 (6 distinct across 328 symbols), 1d 2026-08-02 (8 distinct across 328 symbols), 1d 2026-08-03 (5 distinct across 328 symbols), 1d 2026-08-04 (13 distinct across 328 symbols), 1d 2026-08-06 (33 distinct across 327 symbols), 1w 2026-07-27 (7 distinct across 326 symbols), 1w 2026-07-28 (16 distinct across 328 symbols), 1w 2026-07-29 (25 distinct across 327 symbols), 1w 2026-07-31 (33 distinct across 327 symbols), 1w 2026-08-01 (16 distinct across 328 symbols), 1w 2026-08-02 (13 distinct across 328 symbols), 1w 2026-08-03 (7 distinct across 328 symbols), 1w 2026-08-04 (7 distinct across 328 symbols). On a collapsed day the whole universe receives a handful of distinct probabilities, so these rows grade one market-wide call repeated per symbol, not independent per-symbol forecasts. Figures over this window are withheld until it clears.), and the last successful grade is 0.0h old. The numbers below are that last successful grade, taken at 2026-09-03T14:05:50. Nothing here has been re-graded since.

### Live record

| Predictor | Band | n | Live acc | Null | Skill | Distinct days | Interval |
|---|---|---|---|---|---|---|---|
| directional-ensemble (1d) | all | 2,862 | 44.7% | 55.0% | -10.4pp | 27 | [35.1%, 54.7%] |
| prequential-majority (1d) | all | 2,533 | 55.8% | 53.9% | +2.0pp | 25 | [37.8%, 72.5%] |
| directional-ensemble (1w) | all | 5,520 | 45.4% | 56.1% | -10.7pp | 31 | withheld |
| prequential-majority (1w) | all | 5,159 | 56.7% | 56.1% | +0.5pp | 29 | withheld |
| directional-ensemble (1d, high conviction) | \|p-0.5\|>=0.15 | 455 | 45.1% | 48.5% | -3.4pp | 8 | withheld |
| directional-ensemble (1w, high conviction) | \|p-0.5\|>=0.15 | 816 | 42.6% | 50.2% | -7.5pp | 28 | withheld |
| filingsdrift21 | all | 68 | withheld — no null | — | — | 6 | withheld |
| liquidity21 | all | 1,390 | 72.9% | 70.9% | +2.1pp | 6 | withheld |
| liquidity21#persist | all | 1,105 | withheld — no null | — | — | 5 | withheld |
| liquidity21-crypto | all | 51 | 92.2% | 86.7% | +5.5pp | 9 | withheld |
| liquidity21-crypto#persist | all | 30 | withheld — no null | — | — | 6 | withheld |
| trend21 | all | 1,399 | 78.6% | 78.5% | +0.0pp | 6 | withheld |
| trend21#persist | all | 1,117 | withheld — no null | — | — | 5 | withheld |
| trend21-crypto | all | 51 | 51.0% | 26.7% | +24.3pp | 9 | withheld |
| trend21-crypto#persist | all | 30 | withheld — no null | — | — | 6 | withheld |
| vol21 | all | 1,405 | 54.9% | 52.9% | +2.1pp | 5 | withheld |
| vol21#persist | all | 1,118 | withheld — no null | — | — | 4 | withheld |

Sample-size notices carried by the registry itself (statements about the sample, not verdicts about skill):

- `directional-ensemble (1d)` — FAILED — significantly worse than the naive baseline
  - **verdict not supported by its own interval** — accuracy 0.4465 [0.3505, 0.5467] and its null 0.5505 [0.3866, 0.7041] OVERLAP across [0.3866, 0.5467]: the verdict compares the accuracy interval to the null's point estimate and ignores the null's own published interval, so the stated skill of -0.1039 is not resolved by this sample
- `prequential-majority (1d)` — NO SKILL — indistinguishable from baseline
- `directional-ensemble (1w)` — INSUFFICIENT DAYS (5/10 credible days of 31, 26 degenerate) — no interval, so no verdict
- `prequential-majority (1w)` — INSUFFICIENT DAYS (5/10 credible days of 29, 24 degenerate) — no interval, so no verdict
- `directional-ensemble (1d, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 8, 3 degenerate) — no interval, so no verdict
- `directional-ensemble (1w, high conviction)` — INSUFFICIENT DAYS (5/10 credible days of 28, 23 degenerate) — no interval, so no verdict
- `filingsdrift21` — NO BASELINE — naive-persistence null not frozen for these calls
- `liquidity21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `liquidity21-crypto` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `liquidity21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21#persist` — BENCHMARK — the frozen naive-persistence null itself
- `trend21-crypto` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `trend21-crypto#persist` — BENCHMARK — the frozen naive-persistence null itself
- `vol21` — INSUFFICIENT BLOCKS (1/10 non-overlapping horizon blocks) — no interval, so no verdict
- `vol21#persist` — BENCHMARK — the frozen naive-persistence null itself

### Backtested claims with no live record yet

- `trend63` — registered claim 70.0%, 12,614 forecasts recorded, 0 graded. Not a live result.

**Multiplicity:** family_size=18, looks=50, divisor=900, corrected_alpha=5.555555555555556e-05.

**Survivorship:** epoch 2026-07-24; listing status resolvable for 329/1044 graded symbols (31.5%): 715 inactive symbol(s) with no delisted_at; measured effect +0.50pp (active-only 83.61% minus survivorship-clean 83.10%, n=75,654 clean vs 18,007 active, revalidation of 2026-09-01T10:20:09+00:00) — POSITIVE means the active-only figure is INFLATED by excluding dead names.

<!-- END GENERATED live_accuracy -->

That table used to be typed here, and three other documents typed three
different versions of it — **FC1**. It is now generated from the registry into
`partials/live_accuracy.md` and included identically everywhere; CI fails if a
superseded figure reappears. See `proofs/P2_LIVE_RECORD_RECONCILIATION.md`.

> No interval verdict is available: the platform withholds every interval for this claim (distinct-day blocks below `min_distinct_blocks = 10`).

What stands without an interval is the point estimates above, each at or below
its baseline, and the operational outcome: an auto-retire rule — pre-registered
*before* the numbers came in — fired and stopped it publishing.

The research loop tells the same story. It searches a 48-rule grid daily and
reports, most days:

> NOTHING survived Bonferroni correction, regime-survival and the
> counterfactual. That is a result, not a failure: it is what an honest search
> returns.

That is the project's actual finding, and it is worth more to me than a
fabricated edge. Most simple technical rules on liquid US equities do not
survive honest multiple-comparison correction. Now I know that from my own data
rather than from a paper.

---

## Three bugs worth writing down

### A survivorship fix that had never worked

The database had a point-in-time universe accessor, `TradableAt(ts)`, added to
remove survivorship bias from backtests. Checking it during an audit:

```
TradableAt(2021-06-01) -> 0 symbols
TradableAt(2023-06-01) -> 0 symbols
TradableAt(2025-06-01) -> 0 symbols
```

Zero. Not biased — **empty**, for every historical date, since the day it
shipped. The cause: `added_at` was set to `time.Now()` when the daemon first
*saw* a symbol, not when the company started trading. The filter `added_at <= ts`
then excluded everything for any past date, silently. Any research path asking
for a 2021 universe got an empty set back and nothing noticed.

Repairing `added_at` from each symbol's first bar fixed it:

```
2021-06-01: 0 → 1,325     2023-06-01: 0 → 985
```

The universe is now correctly *more* populous in 2021 than 2023, because
companies that died in between finally appear.

**Lesson:** a guard nobody has watched fail is not a guard. This one returned a
plausible-looking empty list instead of an error.

### The survivorship data was free the whole time

The codebase asserted in a comment that recovering delisted companies needed a
paid point-in-time vendor. It didn't. Alpaca's asset endpoint lists ~19,000
*inactive* securities, and its bars endpoint still serves their history — 650
dead companies recovered for nothing.

But the asset list turned out to be an incomplete enumeration: First Republic,
SVB, Twitter and Activision appear in **neither** the active nor inactive list,
while the bars endpoint happily returns their full history. Enumeration was the
wrong approach. Bringing an external ticker list (Wikipedia's S&P 500
removal-event table) and querying bars directly recovered the large-cap failures
the first pass missed:

```
FRC   First Republic   219.94 →   3.52  (-98%)
SIVB  SVB Financial    755.35 → 106.08  (-86%)
RAD   Rite Aid          28.16 →   0.65  (-98%)
BIG   Big Lots          65.95 →   0.50  (-99%)
```

Delisted symbols went from **21 to 716**. A model that has never seen a company
go to zero learns to buy falling knives; now it has seen it happen 40 times.

Two guards earned their place here. **214 tickers were refused** because they had
been reassigned — COHR, CZR and ECHO all trade today under symbols a previous
company was delisted from, and marking those dead would have deleted live
companies from every point-in-time universe built afterwards. And **160 S&P 500
removals were left alone** because index removal is not delisting: Conagra left
the index in 2026 and still trades. Bars are the arbiter, not the event.

I also spent real effort on a path that **did not work**: SEC Form 25 filings are
the official delisting record, but EDGAR records *that* a delisting happened and
not *which ticker* it happened to. `company_tickers.json` drops delisted names
(70% unresolved), the per-CIK API returns the post-delisting OTC ticker, and the
filing document carries no symbol at all. Three routes, all dead. Worth writing
down so the next person doesn't repeat it.

### An alarm that fired on rows it had already exempted

The grader refused to publish for five days with:

```
INVARIANT VIOLATION: 1172 regime_outcomes rows carry no naive_label
```

Of those 1,172: **1,165 were already in an enumerated, chain-recorded quarantine**
and **7 belonged to a kind whose write path never required that field**. The true
count was zero.

The rule for "which missing baselines indicate a real problem" existed in four
places. One copy had been narrowed correctly, and its comment even warned:

> That is the third independent copy of this rule in the codebase; each one that
> drifts invents its own false alarm.

A fourth copy had drifted. The alarm designed to catch a diverged binary was
firing on rows the system had formally forgiven — and because publication is
fail-closed, that false alarm suppressed the entire accuracy table. **The
honesty machinery had jammed itself shut.**

The fix was not to narrow the fourth copy. It was to delete all four and share
one.

**Lesson:** fail-closed systems convert small bugs into total outages. That is
the correct trade, but it raises the bar on the guards themselves — a
false-positive alarm in a fail-closed system is an outage, not a nuisance.

---

## What I'd tell someone starting this

**Build the grader before the strategy.** The strategy is the fun part and the
easy part. Knowing whether it works is neither.

**Make the honest path the automatic one.** Every guard here works because it
runs without me: hash pins, build attribution, fail-closed publication,
pre-registered retirement rules. Nothing depends on me being disciplined at the
moment a number disappoints me.

**Distrust guards that have never been seen to fail.** `TradableAt` returned an
empty list for two weeks. The build-attribution gate and the grader hash pin were
both load-bearing and both *visibly* fired at me during this work — I trust those
far more than any guard that has only ever stayed quiet.

**A negative result is a result.** The headline finding is that the flagship
model had negative skill and was retired by a rule written before the data came
in. I would rather ship that than a number I can't defend.

---

## Honest limitations

- **Live equity ticks are unverified.** The WebSocket connects, authenticates and
  subscribes; the market was closed during verification, so I have proven the
  connection and not the flow. Live crypto ticks I have confirmed end to end.
- **Reconnection is code- and unit-tested, not demonstrated** against a real
  dropped stream.
- **Microstructure features are crypto-only.** Alpaca's IEX feed is ~2–3% of
  consolidated volume; order-book imbalance computed from it describes one venue,
  not the market. That is a data-licensing ceiling, not a code problem.
- **Recovered delistings skew to 2021–2022** and only 40 are outright collapses.
  Better than 21 delistings; not equivalent to a paid survivorship-free vendor.
- **Verdicts currently read "withheld"** because the rows were written by builds
  that predate revision stamping. That resolves by accumulation as the current
  attributable build records new rows.

---

## Try it

```bash
ops/docker-build.sh
docker run -p 8080:8080 -v signaldeck_data:/data \
  -e ALPACA_KEY=... -e ALPACA_SECRET=... signaldeck
```

Source: `daemon/` (Go), `web/` (Next.js), `tools/` (Python grading and research).
The grading protocol is in `PREREGISTRATION.md`; the accuracy methodology is in
`tools/accuracy_registry.py`. A full system audit, including everything that did
not work, is in `SYSTEM_CHECK_2026-08-02.md`.
