# SignalDeck Alpha Workflow — merged spec

One pipeline combining the three specs: **ensemble stacking & calibration**
(spec 1), **system audit** (spec 2), and **PIT data → triple-barrier →
meta-labeling → Kelly** (spec 3).

Mandate as of 2026-08-01: **full auto-trader with live execution**, Python
model sidecar, Go daemon for inference and order routing, audit running in
parallel with the modeling build.

---

## 0. What the specs get right, and where they collide

The three documents are not three workflows. They are three slices of one
pipeline, and two of them overlap in a place that matters:

- Spec 1 Phase 1 ("stacking meta-model") and spec 3 Phase 5 ("meta-labeling")
  are **different layers that both get called the meta-model**. Stacking asks
  *which way does price go, given the six legs and the regime*. Meta-labeling
  asks *is that directional call trustworthy right now*. They compose in
  series — stacker first, meta-labeler second — and collapsing them into one
  model throws away the entire bet-sizing mechanism.
- Spec 1 Phase 3.2 (EV gate) and spec 3 Phase 5.3 (Kelly sizing) are the same
  decision layer split in two: EV decides **whether** to trade, Kelly decides
  **how much**. EV gate runs first; a trade that fails EV never reaches Kelly.
- Spec 3 Phase 3 (triple-barrier labeling) is upstream of everything in spec 1.
  Changing the label changes what every leg is trained against. It is the
  earliest change and the most invasive.

Ordering therefore runs: labels → features → legs → stacker → calibration →
meta-labeler → EV gate → Kelly → costs → execution → decay monitor.

---

## 1. Ground truth: what already exists

Measured against the repo at `d25ceda` on branch `audit/2026-07-27`, not assumed.

| Spec requirement | Status in repo | Location |
|---|---|---|
| Six legs | **Exists** | `internal/{pressure,expectancy,forecast,newssent,gbm,meanrev}` |
| Weighted-average ensemble | **Exists — to be replaced** | `internal/ensemble/ensemble.go` |
| Purged / embargoed CV | **Exists** | `purge_test.go` in `gbm`, `forecast`, `metalabel`; `internal/alphax` |
| Meta-labeling | **Exists** | `internal/metalabel/metalabel.go` |
| EV gate | **Exists** | `internal/ev/ev.go` |
| Kelly sizing | **Exists** | `internal/riskgate`, `internal/papertrade/roundtrip.go` |
| Cross-sectional factors | **Partial** | `internal/xsfactor`, `tools/xsfactor_edge.py` |
| Delisting backfill | **Partial — see §2** | `tools/backfill_delistings.py` |
| Isotonic calibration | **Exists** | prediction pipeline (`raw_prob` / `cal_prob` in `predictions`) |
| Triple-barrier labeling | **MISSING** | — |
| Return-magnitude sample weights | **MISSING** | — |
| Stacking meta-learner + regime features | **MISSING** | — |
| Fractional differencing | **MISSING** | — |
| Slippage / square-root cost model | **MISSING** | — |
| Shadow trading + IC-decay monitor | **MISSING** | — |
| Live order execution | **MISSING** | — |

So roughly half the spec is built. The work is a merge, not a rewrite.

---

## 2. Blockers found during inspection

These are measured facts from the running system, and each one invalidates
downstream numbers if left alone.

### B1 — The grader is refusing to publish (CRITICAL)

`README.md` live-accuracy block reads `GRADING REFUSED`. Research-loop
liveness has failed since **2026-07-27** (61.1h stale at time of refusal).
Four `worker_runs` narrated 48-rule grid searches with **zero** corresponding
`research_loop_judgments` rows for their UTC days.

The system is behaving correctly — it is refusing to publish numbers it cannot
corroborate. But it means **SignalDeck currently has no verified accuracy
figure**. Every profit number computed on top of it inherits that.

*Blocks:* all acceptance claims. Fix before any accuracy or P&L is reported.

### B2 — Survivorship bias is substantially unresolved (CRITICAL)

Measured from `bars`:

- 1,077 symbols carry daily bars, 2019-01-02 → 2026-08-01
- 676 have the full ~7.5-year history
- **Only 21 symbols stopped trading** more than 14 days before the newest bar

Twenty-one delistings out of 1,077 names across 7.5 years is roughly 2% total.
Real broad-universe US delisting runs several percent *per year*. The universe
is therefore close to "names that exist today, backfilled to 2019" — precisely
the failure spec 3 Phase 1.2 warns about, where the model never sees a company
go to zero and learns to buy falling knives.

`tools/backfill_delistings.py` exists but has not closed this gap.

*Blocks:* cross-sectional ranking, any long-horizon backtest, all profit claims.

### B3 — `universe_membership` is empty (CRITICAL)

The table exists with schema `(day, symbol_id, source)` — exactly a
point-in-time membership design — and holds **zero rows**. There is no PIT
universe definition, so any cross-sectional Z-score or rank is computed against
today's membership applied to historical dates. That is lookahead bias in the
denominator of every cross-sectional feature.

*Blocks:* spec 3 Phase 2.1 entirely.

### B4 — 1-minute history is 2 months deep (HIGH)

| tf | rows | symbols | span |
|---|---|---|---|
| `1m` | 10,904,475 | 1,061 | 2026-06-02 → 2026-08-01 |
| `1h` | 834,255 | 1,063 | 2025-06-23 → 2026-08-01 |
| `1d` | 1,641,130 | 1,077 | 2019-01-02 → 2026-08-01 |

Two months of 1m data is ~40 trading days. After purging and embargoing, the
usable independent sample for a 1m model is small enough that any measured
edge is likely noise. Spec 1's warning about mixing timeframes is right, but
the sharper problem is that the 1m tier does not yet have the history to
support its own model at all.

*Implication:* **the daily tier is the workhorse.** 1,077 symbols × 7.5 years
is a real cross-sectional dataset. Build there first; let 1h and 1m accumulate.

### B5 — IEX-only microstructure (HIGH, not fixable in code)

Alpaca's IEX feed is roughly 2–3% of consolidated volume. Order-book imbalance
and VPIN (spec 3 Phase 2.3) computed from it describe one venue, not the
market's real flow. Crypto via TickStream has genuine L2 and is where those
features are valid.

*Implication:* microstructure features are **crypto-only** until a
consolidated-tape provider is funded. Do not let them carry weight in the
equity stacker.

---

## Stage 0 — Foundation gate (runs in parallel with Stage 1–3 design)

Nothing downstream is trustworthy until these clear.

| # | Action | Done when |
|---|---|---|
| 0.1 | Repair research-loop liveness: make grid searches write `research_loop_judgments` rows, or make the narration honest about not having run | `ops/accuracy-registry.sh` publishes a table instead of a refusal |
| 0.2 | Populate `universe_membership` with PIT daily membership from a survivorship-free source | `SELECT COUNT(DISTINCT day) FROM universe_membership` spans 2019→today |
| 0.3 | Backfill delisted tickers into `bars` + `symbols` with terminal-value rows | Delisted count is a plausible fraction of the universe, not 21 |
| 0.4 | Verify corporate-action adjustment uses the factor as of the historical date, not today's | Spot-check a known split against a PIT source |
| 0.5 | Full system audit (spec 2, all 19 sections) | Report delivered; acceptance matrix filled with evidence |

**0.2 and 0.3 are the expensive ones and they gate the most.** They likely
require a paid data source; Alpaca does not serve delisted history well.

---

## Stage 1 — Labels: triple-barrier with magnitude weights

Replaces every fixed-horizon `fwd_return` target in the codebase.

For each candidate event at time *t* on symbol *s*:

- σ = EWMA of realized daily return volatility (lookback 100 bars)
- Upper barrier: `+u·σ` (start u = 2.0)
- Lower barrier: `−l·σ` (start l = 1.0 → 2:1 payoff, tune later)
- Vertical barrier: 20 bars (daily tier)

Label = `+1` upper first, `−1` lower first, `0` vertical first. Path is walked
bar by bar on **intraday high/low**, not closes — a close-only walk misses
intrabar stop hits and systematically overstates profitability.

Sample weight = |realized return at the touched barrier|, normalized, and
additionally down-weighted by **label concurrency** (how many other labels
overlap the same bars). Concurrency weighting is what keeps overlapping
20-bar windows from counting one market move twenty times.

Output: table `labels(symbol_id, tf, ts, label, weight, touch_ts, touch_ret,
barrier_hit, sigma)`.

*Check to leave behind:* a synthetic price path with a known barrier-touch
order, asserting the labeler returns the expected label and touch time.

---

## Stage 2 — Features: cross-sectional, stationary, regime-aware

Three families, computed per bar and stored in `features`.

**2a. Cross-sectional ranks (the actual alpha).** Every raw feature is
converted to a percentile rank or Z-score **within its PIT universe and
sector** on that date. Absolute RSI of 65 tells you nothing; RSI in the 0.8
percentile of tech names that day is a signal. This is the single highest-value
change in spec 3 and it depends entirely on B3 being fixed.

**2b. Fractional differencing.** Apply to price and volume levels with *d*
chosen per series as the smallest value passing an ADF stationarity test,
searched over 0.1–0.9. Do not hardcode 0.5 — the right *d* differs by series,
and the point is to keep the maximum memory that stationarity allows.

**2c. Regime meta-features** (feed the stacker, spec 1 Phase 1):
VIX level and 1-day change; realized vol over 20/60 bars; ADX; volume /
20-day-average-volume; cross-sectional dispersion of returns; term-structure
slope. `internal/{regime,volregime,structregime,macrofeat}` already produce
several of these — reuse, do not re-derive.

**2d. Microstructure** — crypto only, per B5. OBI and VPIN from TickStream L2.

Prune with SHAP on the existing ~40-feature set before adding more (spec 1
Phase 5.1). Adding features to an unpruned set is how tree legs overfit.

---

## Stage 3 — The six legs, normalized

Every leg must emit a calibrated probability in [0,1] before it reaches the
stacker. Current transforms are mostly linear rescalings, which is why a static
weighted average was the only thing that could consume them.

| Leg | Current | Change |
|---|---|---|
| Pressure | `(score+1)/2` — linear | Fit a logistic link from score → empirical P(up); the score↔outcome relationship is not linear |
| Expectancy | Raw historical hit rate | Empirical-Bayes shrinkage toward the sector base rate, strength ∝ 1/N. Small-N symbols currently emit overconfident rates |
| Forecast | Trainer output | Platt or Beta calibration before ingestion |
| Sentiment | `0.5 + score·scale` — static scale | Scale by rolling cross-sectional σ of sentiment, so a news shock does not saturate every symbol to 1.0 |
| GBM | Per-symbol / global | Train **cross-sectionally** — one model over the panel with symbol/sector features, so illiquid names borrow strength from liquid peers |
| MeanRev | Z-score / distance | Map through the empirical CDF of historical reversion distances |

All six produce **out-of-fold** predictions under purged+embargoed CV. In-fold
predictions fed to a stacker is the classic leak that makes a stacker look
brilliant and trade like noise.

---

## Stage 4 — Stacker: replace the weighted average

`internal/ensemble/ensemble.go`'s `WeightedProbability` assumes the legs are
independent and that their relative usefulness is constant across regimes.
Neither holds — pressure and meanrev are correlated by construction, and
expectancy degrades badly in high-vol regimes.

Replace with a meta-learner over `[p₁..p₆] ++ regime_features`:

- **LightGBM**, depth ≤ 3, lr 0.03, subsample 0.8, colsample 0.8, and
  monotonic constraints on the six leg inputs so a higher leg probability can
  never lower the ensemble output. (`monotone_test.go` already asserts this
  property for the weighted version — keep the test, change the implementation.)
- Trained on **OOF leg predictions only**, with the Stage-1 sample weights.
- **ElasticNet** trained alongside as a sanity baseline. If LightGBM does not
  beat ElasticNet out-of-sample, ship ElasticNet — it will not decay as fast.

TCN is deferred. It needs sequence data the daily tier is too short for and
the 1m tier does not yet have (B4).

---

## Stage 5 — Calibration, walk-forward only

Global isotonic regression on financial data overfits into step artifacts.

- **Beta calibration** as primary, isotonic retained as a comparator; ship
  whichever wins on out-of-sample Brier score, refit monthly.
- **Expanding walk-forward**: fit on months ≤ N, apply to month N+1. Never fit
  a calibrator on data it will score. The reference code in spec 1 Phase 4 has
  this bug — it calls `self.meta_model.predict_proba(X_train)` and fits the
  calibrator on the result, so the calibrator sees in-sample probabilities that
  are far sharper than anything it will meet in production. Fixed here by
  fitting the calibrator on OOF probabilities.

---

## Stage 6 — Meta-labeling: is the call trustworthy?

Distinct from Stage 4 (see §0). `internal/metalabel` already exists — wire it
to the new stacker rather than rebuilding.

- Input: features + the stacker's calibrated probability + regime features
- Target: 1 if the primary call would have been profitable after costs, else 0
- Output: `P_correct` — confidence in the direction call

`P_correct` is the input to sizing, and it is also the **decay alarm**: when it
trends down, the primary edge is eroding and position sizes shrink
automatically before anyone notices in the P&L.

---

## Stage 7 — EV gate, then Kelly

Two gates in series. Order matters.

**7a. EV gate** (`internal/ev`, extend). Trade only if

```
p̂·E[R⁺] − (1−p̂)·|E[R⁻]| > cost_roundtrip
```

where `E[R⁺]` and `E[R⁻]` come from a secondary regression head trained on
realized barrier-touch returns — not from a fixed assumed payoff. A calibrated
0.52 that clears costs is tradeable; a calibrated 0.58 on a name with a wide
spread is not.

**7b. Cost model** (spec 3 Phase 6.1), feeding 7a:

```
slippage = σ · sqrt(order_volume / ADV) · k
cost_roundtrip = 2·(spread/2) + slippage + commission + borrow_cost_if_short
```

`k` is calibrated from actual fills once live, seeded from literature until
then. **Borrow cost is not optional** — short-side backtest profit without HTB
fees is fiction on exactly the names that look most attractive to short.

**7c. Fractional Kelly** (`internal/riskgate`, extend):

```
f = P_correct − (1 − P_correct)/b      b = avg_win / avg_loss
size = clamp(f/4, 0, max_position)     quarter-Kelly
```

Quarter-Kelly, not half. Kelly's optimality assumes the probability estimate is
correct; `P_correct` is itself a model output with error, and Kelly under a
mis-estimated *p* is violently unstable on the upside. The 75%-of-growth
argument for half-Kelly assumes *p* is known.

**7d. Volatility targeting.** Scale the whole book so portfolio daily σ hits a
fixed target (start 1% of equity). Position sizes then shrink automatically
when vol spikes, before any model notices.

---

## Stage 8 — Execution (live)

Written by hand, not generated. Live trading is off by default.

- **Promotion gate:** no live order until Stage 9 shadow trading has run ≥30
  sessions and live IC ≥ 70% of backtested IC.
- **Kill switch:** a file or env flag checked before every order; flipping it
  flattens and halts.
- **Hard caps** independent of Kelly: max position per symbol, max gross
  exposure, max orders/day, max daily loss → auto-halt.
- **Order type by horizon** (spec 3 Phase 6.2): market orders for short-horizon
  signals where fill certainty dominates; limit orders at the touch for longer
  horizons, accepting miss rate to capture spread.
- **Idempotent order IDs** so a reconnect cannot double-submit — this is the
  execution-layer analogue of the duplicate-subscription bug spec 2 asks about.
- Every order logs intent, `P_correct`, EV, size rationale, and fill, so any
  trade can be reconstructed.

---

## Stage 9 — Shadow trading & decay monitoring

- Run live-but-unfunded for 30+ sessions. Log predictions at decision time,
  frozen, before outcomes exist — the existing `prereg` package is the right
  home.
- Compare live IC vs backtested IC. **< 70% ⇒ the model is overfit**; do not
  promote, return to Stage 4.
- Alarms: `P_correct` trend, calibration drift (live Brier vs backtest),
  feature drift (PSI on inputs), fill-vs-modeled slippage.
- Feeds the existing Honesty page. The refusal machinery from B1 is an asset
  here, not an obstacle — it is already built to refuse rather than flatter.

---

## Architecture

```
Python sidecar (tools/alpha/)                Go daemon (internal/)
─────────────────────────────                ─────────────────────
labels.py      triple barrier      ──┐
features.py    xs-rank, fracdiff     │       ensemble/   loads stacker.txt
legs_oof.py    OOF under purged CV   ├──▶    metalabel/  loads meta.txt
stacker.py     LightGBM + ElasticNet │       ev/         EV gate + costs
calibrate.py   beta, walk-forward  ──┘       riskgate/   quarter-Kelly + caps
                                             execution/  orders, kill switch
       artifacts → data/models/*.txt + calibration.json
```

Training is offline Python; inference is Go loading committed artifacts. The
boundary is a versioned artifact directory, so a model can be rolled back
without a redeploy, and `internal/datasetver` / `internal/lineage` already
exist to stamp provenance.

---

## Acceptance criteria

No stage is complete on code review alone.

| # | Criterion | Evidence required |
|---|---|---|
| A1 | Grader publishes | README live-accuracy block shows a table, not a refusal |
| A2 | PIT universe | `universe_membership` populated 2019→today |
| A3 | Survivorship closed | Delisted-symbol count consistent with historical base rates |
| A4 | Labels correct | Synthetic-path test passes; concurrency weights sum sanely |
| A5 | No leakage | Stacker trained on OOF only; leakage test in `alphax` extended to cover it |
| A6 | Stacker beats baseline | Out-of-sample Brier + IC vs current weighted average, walk-forward |
| A7 | Calibrated | Reliability diagram near-diagonal out-of-sample |
| A8 | Costs real | Modeled slippage vs actual fills, measured not assumed |
| A9 | Live ≥ 70% of backtest IC | 30+ shadow sessions |
| A10 | Kill switch works | Demonstrated halt during an active session |

**A9 is the one that decides whether this ships.** Everything upstream can pass
and A9 still fail — that is the point of it.

---

## Honest expectations

The framing "very high accuracy and even higher profit" deserves a straight
answer: those two goals partly oppose each other. Directional accuracy on
liquid equities lives near 52–55% for real strategies; the profit comes from
Stage 6–7, from sizing the high-conviction calls larger and refusing the
marginal ones, not from pushing accuracy toward 70%. A system reporting high
accuracy on this data would more likely have a leak than an edge — and B2/B3
are exactly the leaks that would produce it.

The workflow above is built so that if there is no edge, it says so.
