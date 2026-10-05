# Review of the SPYX work — what would actually make it better

Written 2026-08-31 after the sealed block was graded and the goal closed. Findings are ordered
by value, and every claim here was **measured**, not asserted. Where a reviewer's claim failed
verification it is recorded as rejected, with the evidence.

---

## 1. HIGH — the seal guard was bypassable. Fixed.

The single most important safety property in the protocol compared raw ISO strings:

```python
if not seal_is_open() and end >= SEAL_START:
```

`"12/31/2026" >= "2021-01-01"` is **False** — `'1'` sorts below `'2'` — so the guard passed and
pandas then parsed the US-format date correctly and returned **8,453 rows through 2026-08-28**.
`"01/01/2027"` did the same. `datetime.date` and `pd.Timestamp` raised `TypeError` (fail-safe by
accident, not design). `"2026/08/28"` and `"28.08.2026"` were blocked only by the luck of their
leading digit.

**Fixed**: `_as_ts()` parses both bounds to `pd.Timestamp` at the function boundary; the guard
compares timestamps; unparseable input raises `ValueError`. A regression test in `evalcore.py`'s
self-check asserts all six alternate formats are refused plus garbage rejected.

**Rule to carry forward: never implement a safety comparison on stringly-typed dates.** Parse at
the boundary, compare parsed values. This guard read correctly and passed its happy-path test
for the entire project while being wide open.

---

## 2. HIGH — PBO and DSR both reward a homogeneous candidate set. This is why they missed.

Both multiple-testing controls measure variation **across trials**. A narrow grid has almost
none, so both drift toward "everything is fine" precisely when the search is least diverse.

**DSR.** Deflation is driven by `sr_std`, the cross-sectional SD of the trial Sharpes. Holding
SR, skew, kurtosis and T at their measured values and varying only search diversity:

| candidate set | sr_std | N | SR0 | DSR |
|---|---|---|---|---|
| 8 near-identical configs (**actual**) | 0.0061 | 8 | 0.0090 | **0.9955** |
| 8 moderately varied | 0.0150 | 8 | 0.0219 | 0.9666 |
| 8 genuinely different families | 0.0300 | 8 | 0.0438 | **0.6961** |
| 50 diverse trials | 0.0300 | 50 | 0.0683 | 0.1672 |
| realistic total search incl. prior sessions | 0.0500 | 200 | 0.1383 | **0.0000** |

The reported 0.9955 is a statement about the *grid*, not the strategy.

**PBO.** Unstable in magnitude across an arbitrary parameter:

| blocks S | splits | PBO | median λ |
|---|---|---|---|
| 8 (**used**) | 70 | **0.0571** | +1.253 |
| 10 | 252 | 0.0476 | +1.253 |
| 12 | 924 | **0.1158** | +1.253 |
| 14 | 3,432 | 0.0414 | +1.253 |
| 16 (standard) | 12,870 | **0.0370** | +1.253 |

A 3× spread. The median λ is stable at +1.253 for every S, so the *qualitative* verdict holds —
the in-sample winner does generalise **within this family** — but the headline number should
never have been quoted to three decimals.

**Actions**: report DSR against an honest N and sr_std spanning the whole search, including
prior sessions; use S=16; report PBO as a range across S, and lead with median λ rather than
the point estimate.

---

## 3. HIGH — the grid searched one knob, not the problem

All 8 slots went to SMA length × weighting inside a single trend-following family. Given that
the hindsight-optimal allocation inverted ~78% between DEV and the sealed block, those slots
should have spanned **families** — cross-sectional momentum, carry, low-vol, risk-parity,
faster filters — not one family's tuning parameter.

This also compounds finding 2: a one-knob grid is exactly the input that makes PBO and DSR
look best while telling you least.

---

## 4. REJECTED — "a different selection rule would have chosen differently"

Tested directly against `out/trials.jsonl`:

| config | dev | val | full | min | avg |
|---|---|---|---|---|---|
| **dt_sma150_invvol** | **0.752** | **1.015** | **0.830** | **0.752** | **0.883** |
| dt_sma150_equal | 0.724 | 0.974 | 0.797 | 0.724 | 0.849 |
| dt_sma200_equal | 0.514 | 0.861 | 0.608 | 0.514 | 0.688 |

Ranking by `min(dev, val)`, by full-window Sharpe, and by the average of dev and val **all
select the same finalist**. It dominates on every criterion. The selection rule is exonerated —
which usefully isolates the fault to the candidate set, not the ranking.

---

## 5. MEDIUM — vol-matching failed out of sample, and the obvious fix is already refuted here

Static leverage matched to DEV+VAL volatility produced **21.9% realised against a 16.7% target,
a 31% overshoot**, and is a direct cause of the beta rising 0.422 → 0.812.

The reviewer prescribed dynamic volatility targeting. **This repo's own evidence refutes that**:
`RESULTS_premia.md` records vol-targeted SPY at Sharpe 0.72 against 0.79 for plain trend, and
"trend + volatility scaling: lower CAGR AND lower Sharpe at every leverage." So the fix must be
something else — a realised-vol leverage cap, or simply reporting the overshoot as a known
property. Do not re-run vol targeting expecting a different answer.

---

## 6. MEDIUM — phase-in is worth doing, but for the regime, not the row count

`closes()` inner-joins and drops any row where a sleeve is missing, so DBC's 2006-02-06
inception truncates all nine.

| panel | rows | from |
|---|---|---|
| current (9 sleeves, inner join) | 2,494 | 2006-02-06 |
| ≥8 sleeves live | 2,799 | 2004-11-18 |
| ≥6 sleeves live | 3,381 | 2002-07-30 |
| SPY-length | 7,011 | 1993-01-29 |

The naive "1.88× more data" is misleading — before 2002 only 1–4 sleeves exist, testing a
different, undiversified strategy. The honest gain is **887 rows (1.36×)** at ≥6 sleeves.

The value is not the rows. Current DEV begins February 2006 and contains **exactly one crisis**.
Reaching back to 2002-07-30 adds a second regime, and specifically one where bonds and equities
were negatively correlated — directly relevant, since the failure was a regime inversion.
Cost: the rule becomes "1/N_t per live sleeve", which needs care to keep the cash drag honest.

---

## 7. MEDIUM — signal decay is never measured

Nothing in the design tests whether the trend signal's predictive power degrades over time —
e.g. regressing forward returns on the current signal and checking coefficient stability across
sub-periods. Given beta moved 0.422 → 0.812, this is the cheapest missing diagnostic.

---

## 8. LOW — precision and accounting details

- **Rounding.** `metrics()` rounds to 6 dp before storing, and downstream code compares stored
  against recomputed at 1e-6. That is tighter than the stored precision. Store full precision,
  round only for display. This is what makes audit check A7 brittle.
- **Trade count.** `n_trades` counts *days* with any weight change, so 170 means 170 rebalance
  events, not 170 trades — a 9-sleeve rebalance is 9 orders. Defensible as the unit of
  independent decisions, but it should be labelled as rebalance events.
- **Flat cost.** 10 bps applies equally to SPY and to DBC/VNQ, whose spreads are materially
  wider. Per-instrument spreads would raise the true cost of exactly the sleeves the finalist
  leaned on.
- **First-row turnover.** `cost_returns` charges full turnover on row 1 against an implicit zero
  starting book. Conservative, and it slightly penalises the strategy versus the benchmark.

---

## 9. LOW — filename parsing broke for any ticker containing an underscore. Fixed.

The loader stripped a `"<ticker>_"` prefix and required exactly one underscore to remain. A
ticker like `A_B` saves as `A_B_1990-01-01_2020-01-01.parquet`; stripping `A_B_` leaves
`B_1990-01-01_2020-01-01`, which has two underscores, so the file is skipped — its own cache
entry is never recognised and it re-downloads on every call, forever.

Latent, not live: no ticker in use contains an underscore (`^IRX`, `BF-B`, `ES=F` all avoid it).
Fixed anyway because the correct parse is simpler than the broken one — `rsplit("_", 2)` takes
the last two fields as the dates and everything before them as the ticker, then matches the
ticker exactly. This also removes the `startswith` prefix test, which was a latent
prefix-collision risk. Verified `SPY`, `^IRX` and `TLT` still resolve.

## 10. LOW — concurrent pin writes can lose an update

`_write_pin` is read-modify-write with no lock. Two processes resolving the same ticker for the
first time can each write, and one update is lost. Only reachable on first resolution of a
ticker (afterwards the pin is authoritative and a mismatch raises), and this is a
single-process research harness, so it is accepted rather than fixed. It would matter if the
harness were ever parallelised — and note the stock-trader engine already writes into the same
cache directory, so the *directory* is genuinely shared even though the pin file is not yet.

## 11. HIGH — cumulative percentage points are not comparable across windows

The target defines excess as `strategy cumulative return − SPY cumulative total return` in
percentage points. That is well defined for one fixed window and **actively misleading across
windows of different length**, because it compounds.

| window | years | excess/yr | cumulative pp |
|---|---|---|---|
| 1993–2015 | 22.9 | −1.09 pp/yr | **−147.47** |
| 2016–2020 | 4.9 | +6.04 pp/yr | +56.41 |
| 1993–2020 | 27.8 | **−0.04 pp/yr** | −15.01 |

Compounding a −1.09 pp/yr gap over 22.9 years gives −147.48 pp, matching the reported −147.47 pp
to two decimals. So −147 pp is arithmetic, not catastrophe. And −15.01 pp over 27.8 years is a
**four-basis-point annual difference** — a dead heat with the benchmark.

**I misread this myself.** An earlier draft of `RESULTS.md` described the production strategy as
underperforming SPY over 28 years. That was wrong. It roughly matches SPY's return, at −42.9%
maximum drawdown against SPY's −55.2%, with beta 0.611 — SPY-equivalent return at materially
less pain. The correction is recorded in `RESULTS.md`.

The same flaw sits inside the target's own success gate: **+10 pp over the 5.6-year sealed
window is about 1.7 pp/yr, but +10 pp over a 23-year window is about 0.35 pp/yr** — the same
stated threshold differing roughly fivefold in difficulty on window length alone.

**Fix applied:** `evalcore.excess_ann_pp()` added and must be reported beside `excess_pp()`
everywhere. Any future gate should be stated in annualised terms, or pinned to one fixed window
length.

## Verification coverage, and what is genuinely untested

The delegated quantitative review never ran (sustained router 429s); the design and robustness
reviews completed and are adjudicated above. The quantitative lens was instead covered directly,
which is stronger evidence than a second opinion because it checks against known answers rather
than against another model's judgement.

**Metric definitions, verified against analytically known cases:**

| quantity | test | result |
|---|---|---|
| `max_dd` | path 100→110→88→120, peak 110 trough 88 | −0.200000 exact |
| `cum_return` | same path | 0.200000 exact |
| `beta` | y = 2·x + tiny noise | 2.0000 exact |
| `es95` | uniform ramp, mean of worst 50 of 1000 | exact to 8 dp |
| `ann_vol` | iid normal σ=0.01 | 0.1577 vs 0.1587 expected |
| `sharpe` | iid normal, μ/σ=0.04 | matches the hand formula exactly |

The Sharpe case initially looked wrong (0.4587 against an expected 0.6350) and was checked
rather than waved through: the sample mean was 0.000287 against a population 0.0004, a **1.12σ**
deviation at n=10,080. Sampling noise, not a defect — and a useful reminder of how noisy a
Sharpe estimate is even with 40 years of daily data.

**DSR formula verified by inspection:** `g4` is the plain fourth standardised moment, not excess
kurtosis (the classic error in this formula); `SR0` uses the correct Gumbel form; per-observation
Sharpe is used consistently in numerator, denominator and `z`. Minor inconsistency: `g3` uses
pandas' bias-corrected `.skew()` while `m4` uses a population mean — immaterial at T=3731.

**Cost accounting** is covered by `audit_finalist.py` A4, which reconstructs net returns without
calling `cost_returns` and agrees to max diff 0.0, plus A8/A9 monotonicity in cost and financing.

**Still genuinely untested:** the `closes()` inner-join behaviour under a ticker that lists mid
sample and later delists; `riskfree_daily`'s reindex-then-ffill when `^IRX` carries dates absent
from the price index; and the discount-rate-to-simple-rate conversion `(close/100)/252`, which
ignores the discount-vs-bond-equivalent-yield distinction (worth roughly 2 bps/yr at a 2% rate,
immaterial at this precision but unverified).
