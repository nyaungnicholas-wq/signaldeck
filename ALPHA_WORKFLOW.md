# SignalDeck Alpha Workflow — merged spec

<!-- DOCUMENT CONTROL -->
> **Owner:** Nicholas Nyaung · **Version:** 1.0 · **Last reviewed:** 2026-08-04
> **Status:** AUTHORITATIVE — freeze lifted 2026-08-04
> **Scope:** Merged alpha-workflow spec. §B2/§B3 are the corroborated authority on the two open data-integrity defects.
> **Frozen claim classes:** FC3, FC6 — set `C` defined in `proofs/P6_GOVERNANCE_CLEANUP.md` §4, statuses in `proofs/P10_FREEZE_LIFT.md` §4
> **Authority:** `proofs/P10_FREEZE_LIFT.md` (freeze LIFTED 2026-08-04) · `proofs/P6_GOVERNANCE_CLEANUP.md` (status)
> **Publication:** PUBLISHABLE — caveats are the frozen classes above

> **Backtest data: `pre-survivorship-fix`.** Every historical figure below was
> computed on the universe as it stood BEFORE the 2026-08-04 survivorship
> backfill (`proofs/P3A_SURVIVORSHIP_BACKFILL.md`) and the point-in-time
> universe rebuild (`proofs/P3B_PIT_UNIVERSE.md`). It has not been re-run on
> the repaired universe. Read the numbers as a record of what was measured
> then, not as what the repaired data would produce now.

> ## ⚠ P0 freeze (2026-08-04) — LIFTED 2026-08-04 by `proofs/P10_FREEZE_LIFT.md`
> Remediation complete; freeze lifted. **Do not attach capital.** Figures below are historical unless generated.
> Frozen-class gloss (non-normative; `C` is defined once in `proofs/P6_GOVERNANCE_CLEANUP.md` §4): **live accuracy · intervals · survivorship · point-in-time data ·
> kill switch · position sizing · "already built / verified".**
>
> **This file is the corroborated authority on the two data-integrity defects.**
> Its §B2/§B3 measurements were never in dispute — they are what other documents
> contradicted — and **both have since been re-derived and moved**, so read the
> UPDATE blocks inside §B2 and §B3 rather than the numbers in this header:
>
> - **§B2 survivorship** — the "21 delistings / 1,077 names ≈ 2% cumulative"
>   figure is the **pre-import** state, confirmed against
>   `data/signaldeck.db.bak-preimport-20260802`. Now 723 / 1,777 with 716
>   `delisted_at` stamps. **Materially closed 2019–2022, residual 2023–2025 gap
>   quantified** — not closed. `proofs/P3A_SURVIVORSHIP_BACKFILL.md`.
> - **§B3 point-in-time universe** — `universe_membership` now holds **1,854,228
>   rows over 2,146 days**, not zero. The predicted look-ahead in the
>   cross-sectional denominators was **measured and is not there**; the one
>   active-set-applied-to-history path is documented and non-default.
>   `proofs/P3B_PIT_UNIVERSE.md`.
>
> Its status table remains frozen: "Kelly sizing — Exists" is contradicted by
> `ARCHITECTURE_EV.md` (riskgate sizes only *after* go/no-go; portopt not wired
> to allocation).
>
> Authority: `proofs/P0_FREEZE.md`, **lifted 2026-08-04** by `proofs/P10_FREEZE_LIFT.md`.

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

> **UPDATE — P3A, 2026-08-04 (`proofs/P3A_SURVIVORSHIP_BACKFILL.md`).** The
> measurement above is the **pre-import** state and is confirmed as such against
> `data/signaldeck.db.bak-preimport-20260802`. An import ran 2026-08-02 and was
> never re-derived until now: **21 → 723** symbols that stopped trading,
> **16 → 716** `delisted_at` stamps, **1,077 → 1,777** symbols with daily bars.
>
> **Substantially closed for 2019–2022; NOT closed for 2023–2025.** The vendor's
> own candidate counts for those years were 16 / 12 / 23, so the recent window is
> thin at the source, not at the filter. Rate by year: 3.4% (2020), 25.1% (2021,
> the de-SPAC wave), 20.8% (2022), then 2.6% / 2.7% / 4.3%.
>
> **UPDATE — P3D, 2026-08-04 (`proofs/P3D_DELISTING_GAP_2023_2025.md`). The
> 2023–2025 gap is now CLOSED, and the root cause was not a thin source.**
> `fetch_form25.py` resolved CIKs through `company_tickers.json`, which lists
> only CURRENTLY-LISTED companies — a Form 25 filer is absent from it by
> construction, losing 592 of 841 CIKs. Recovering the ticker from the issuer's
> own last cover page (`dei:TradingSymbol` in `R1.htm`) lifted resolution to
> 87.6% and imported **1,216 delistings / 835k bars**: 2023 25→**477**,
> 2024 26→**348**, 2025 43→**297**. Against a realistic 5,500-name market that is
> 8.7% / 6.3% / 5.4% — the real-world band.
>
> **Effect measured, and the earlier estimate was WRONG.** On the 706-name
> increment every leg moved <0.55pp, which is what this note used to say. With
> the full repair (universe 1,059→2,935) the 21d legs move to liquidity −2.71,
> lowVol +3.59, mom12_1 +2.32, several now clearing Bonferroni. The growth is
> not monotone — most legs dip at the +706 increment before growing, which is
> why that increment read as stability. **Neither set is publishable as an edge:** the
> repaired universe is ~50% eventually-dead (P3D §7.3), so it is an upper bound.
> Cross-sectional studies must now define their universe per-day from
> `universe_membership` and match the live/dead ratio to reality.
>
> A3 may be marked *"delisting record materially complete 2020–2026; cross-
> sectional results pending a representative universe"* — not *"closed"*.

*Blocks:* cross-sectional ranking, any long-horizon backtest, all profit claims.

### B3 — `universe_membership` is empty (CRITICAL)

The table exists with schema `(day, symbol_id, source)` — exactly a
point-in-time membership design — and holds **zero rows**. There is no PIT
universe definition, so any cross-sectional Z-score or rank is computed against
today's membership applied to historical dates. That is lookahead bias in the
denominator of every cross-sectional feature.

> **UPDATE — P3B, 2026-08-04 (`proofs/P3B_PIT_UNIVERSE.md`). CLOSED, and the
> second sentence above was wrong.**
>
> `universe_membership` now holds **1,854,228** rows over **2,146** days
> (2018-07-26 → 2026-08-05, 1,777 symbols), derived from daily-bar evidence by
> `store.RebuildUniverseMembership` / `sdmaint build-universe`. Four production
> queries and seven Go regression tests prove no symbol appears before its first
> print or survives past its last.
>
> But the lookahead this section predicted was **measured and is not there**.
> Every cross-sectional denominator in the repository was already
> same-day-evidence-derived: `internal/alphax` builds its median from the
> **recorded feature rows** of that day (its cross-section is 1,077 against a PIT
> universe of 1,059 over the month `features` covers — agreement within 1.7%),
> and `tools/xsfactor_edge.py --universe all` and
> `tools/revalidate_structural.py` both select on same-day bars. The only
> active-set-applied-to-history path is `xsfactor_edge.py --universe active`,
> which the tool documents and does not default to. **No feature was rebuilt and
> no model re-run, because none had the defect.**
>
> Two real defects fell out of doing it anyway: `universe_membership` was missing
> from `schema.sql` (present only in the operator's own database, absent from
> every cold clone), and symbol row 1122 `ATC` holds **two different securities** —
> Atotech to 2022-08-16 and a GraniteShares ETF from 2026-05-12 on a recycled
> ticker. 61 symbol-days are excluded and counted; the row still needs splitting.

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

### B5 — IEX-only INTRADAY microstructure (MEDIUM, intraday only)

Alpaca's IEX feed is roughly 2–3% of consolidated volume. Order-book imbalance
and VPIN (spec 3 Phase 2.3) computed from it describe one venue, not the
market's real flow. The free tier already provides full SIP for historical
daily bars; only the trailing 16 minutes are IEX. Crypto via TickStream has
genuine L2 and is where those
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
