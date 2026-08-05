# P3A — Survivorship backfill, measured

**Date:** 2026-08-04 · **Concerns:** `ALPHA_WORKFLOW.md` §B2, acceptance item A3
· **Verdict: substantially closed for 2019–2022, quantified residual gap
2023–2025. A3 must NOT be marked met without that qualifier.**

---

## 1. What the brief expected, and what was actually true

The brief said to run `tools/backfill_delistings.py` because the database held
"only 21 delistings across 1,077 names over 7.5 years". **That premise was
already stale when the brief was written.** A backfill ran on 2026-08-02 and was
never independently re-derived — `SYSTEM_CHECK_2026-08-02_REAUDIT.md` lists the
prior session's "21 → 716 delisted" claim explicitly under *not re-derived*.

So the work here was not to run the tool again. It was to **verify the claim
from evidence**, which nobody had done, and then to establish whether it is
enough. Both are below.

## 2. Before and after, re-derived

The pre-import database was preserved as `data/signaldeck.db.bak-preimport-20260802`,
so before/after is a measurement rather than a recollection. Both columns are
produced by the same query against the two files:

| metric | BEFORE (`.bak-preimport-20260802`) | AFTER (current) |
|---|---|---|
| symbols with `tf='1d'` bars | 1,077 | 1,777 |
| bar span | 2019-01-02 → 2026-08-01 | 2018-07-26 → 2026-08-04 |
| symbols with full history (first bar ≤ 2019-01-10) | 692 | 692 |
| **stopped trading >14d before newest bar** (§B2's metric) | **21** | **723** |
| **`delisted_at` stamped** | **16** | **716** |
| `universe_membership` rows | 0 | 0 → see P3B |

Stock-only, restricted to symbols that actually carry daily bars:

| | BEFORE | AFTER |
|---|---|---|
| stock symbols with 1d bars | 1,070 | 1,770 |
| …`active=1` | 322 | 322 |
| …`active=0` | 748 | 1,448 |
| …`delisted_at` stamped | 16 | 716 |

**The §B2 figure of 21 is confirmed as the pre-import state, and 716/723 is
confirmed as the post-import state.** The claim was true and is now derived.

### Command and provenance

The applied path was `tools/alpha/fetch_delisted.py` → `tag_delisted.py` →
`sdmaint import-delisted` (a Go command, because the single-writer doctrine
forbids Python writing the live database). Its staging reports are checked in:

- `tools/alpha/delisted_report.json` — generated 2026-08-02T02:59:52Z:
  2,099 candidates, 1,072 kept, **214 flagged for ticker reuse**, 813 with no
  data, 178,922 bars staged.
- `tools/alpha/delisted_cohorts.json` — cohorts `ordinary` 488, `too_short` 416
  (dropped), `spac_shell` 107, `collapse` 33, `spac_named` 22; 650 usable
  symbols, 169,296 usable bars, 521 non-SPAC usable.
- `tools/alpha/form25_report.json` — SEC EDGAR Form 25 merge: 1,817 filings,
  249 resolved tickers, 592 unresolved CIKs, 2 added delistings.

## 3. Is the delisting history now plausible? Partly.

Annual delisting rate, computed as *delistings recorded in year Y ÷ symbols
tradable at the start of Y*:

| year | universe at start | delisted | rate |
|---|---|---|---|
| 2020 | 712 | 24 | 3.4% |
| 2021 | 1,326 | 333 | **25.1%** |
| 2022 | 1,170 | 243 | **20.8%** |
| 2023 | 975 | 25 | 2.6% |
| 2024 | 978 | 26 | 2.7% |
| 2025 | 1,005 | 43 | 4.3% |
| 2026 (partial) | 1,035 | 22 | 2.1% |

The 2021–2022 spike is real and explicable — it is the de-SPAC wave, and the
cohort file counts 107 `spac_shell` plus 22 `spac_named` names concentrated in
exactly those years. It is not a data error, but it is also not the ordinary
delisting process, and any study that treats those years as a normal attrition
regime is treating a one-off structural event as a base rate.

**The residual defect is 2023–2025, and it comes from the source, not the
filter.** The vendor's own by-year candidate counts before any cohort filtering
were 2020: 182, 2021: 506, 2022: 311, **2023: 16, 2024: 12, 2025: 23**. A
delisted-companies endpoint under-reports recent years; the live detector, which
would cover them, only began watching in 2026. Broad US delisting runs several
percent per year, so 2.6–4.3% against a tracked-liquid-names universe is *low
but not absurd* — and it is unverified either way. **Names that died in
2023–2025 are underrepresented and any backtest concentrated in that window
remains partially survivor-seeded.**

## 4. A defect the backfill did not fix, found here

`tools/backfill_delistings.py` documents a ticker-reuse guard, and the staging
report flagged 214 conflicts. **One got through.** Symbol row 1122, ticker `ATC`:

```
delisted_at  2022-08-16
bar segment  2021-02-04 .. 2022-08-16   (383 bars)   Atotech
bar segment  2026-05-12 .. 2026-08-04   ( 58 bars)   GraniteShares Autocallable COIN ETF
```

Two different securities on one symbol row, with a spliced price series and a
four-year gap. `store.TradableAt` deletes the live ETF from every point-in-time
universe after 2022-08-16 — the exact failure `importdelisted.go` warns about in
its own header. It is detected and excluded by the P3B rebuild (which counts and
reports it) but the underlying row still needs splitting in two. **Not fixed
here; it is a data migration, not a phase-3 edit.**

## 5. Re-run: cross-sectional derivation, pre- vs post-backfill

`daemon/internal/xsfactor/derivation.json` was produced 2026-07-26 — **before**
the import. Its universe was `symbolsLoaded 1059, symbolsActive 320`, and
`daemon/internal/xsfactor/xsfactor.go` describes that as the "SURVIVORSHIP-CLEAN
universe of 1,059 stocks including 739 delisted names".

**That description is wrong, and the way it is wrong is the point.** On
2026-07-26 only **16** of those symbols carried a `delisted_at` stamp. The 739
were `active=0` — which this repository's own schema comment defines as a
SUBSCRIPTION decision ("the user stopped watching"), explicitly separated from
`delisted_at`, a MARKET fact, *because conflating them is what made survivorship
bias invisible*. The study conflated them anyway.

Re-run on the post-backfill universe (`python3 tools/xsfactor_edge.py --universe
all`, 2026-08-04). Universe `symbolsLoaded 1765, symbolsActive 318` — 706 more
names, now with real death dates:

| horizon | leg | pre-backfill edge | post-backfill edge | Δ |
|---|---|---|---|---|
| 5d | liquidity | −1.10pp | **−1.33pp** | −0.23 |
| 5d | lowVol | +1.10pp | +1.18pp | +0.08 |
| 5d | mom12_1 | +1.24pp | +1.23pp | −0.02 |
| 5d | composite_as_audited | +0.60pp | +0.62pp | +0.01 |
| 21d | liquidity | −1.65pp | **−1.70pp** | −0.04 |
| 21d | lowVol | +1.99pp | +1.77pp | −0.22 |
| 21d | mom12_1 | +1.35pp | +1.08pp | −0.27 |
| 21d | composite_as_audited | +0.21pp | +0.09pp | −0.11 |
| 63d | liquidity | −1.92pp | −1.47pp | +0.44 |
| 63d | lowVol | +1.84pp | +1.32pp | −0.51 |
| 63d | mom12_1 | +0.79pp | +0.97pp | +0.17 |
| 63d | composite_as_audited | +0.20pp | +0.60pp | +0.41 |

Sample sizes grew 10–12% (e.g. 21d composite 71,550 → 79,216).

**No verdict changed.** Every leg moved by less than 0.55pp. `liquidity` remains
negative at 5d and 21d and still survives Bonferroni over the 9 leg×horizon
tests. `lowVol` remains positive-but-fragile with an interval touching zero.
Every composite remains indistinguishable from zero.

The honest reading: the survivorship defect was **real as a defect and small as
an effect on this particular study** — 706 confirmed-dead names did not rescue
or destroy any cross-sectional conclusion. That is a result worth having, and it
is the opposite of the result the contamination warning implied.

## 6. Studies by backfill status

| study | artifact | date | status |
|---|---|---|---|
| cross-sectional factor edge | `daemon/internal/xsfactor/derivation.json` | 2026-07-26 | **PRE — re-run above, no verdict change** |
| alpha cross-section IC | `tools/alpha/xsection_ic.json` | 2026-08-01 | **PRE — not re-run** |
| alpha cross-section score | `tools/alpha/xscore_result.json` | 2026-08-01 | **PRE — not re-run** |
| SPA/StepM ledger | `tools/spa_ledger_result.json` | 2026-08-03 | POST |
| trend21 barrier geometry | `HOW_PREDICTORS_WORK.md` §B5 | 2026-08-03 | POST |
| structural band tables (trend21/vol21/liquidity21/trend63) | `tools/revalidate_structural.py` | — | re-run initiated 2026-08-04; see §7 |

## 7. Label to carry until every PRE study is re-run

Any document reproducing a result from the PRE rows above must carry:

> **Survivor-seeded universe.** This result was computed on a universe whose
> delisted names were identified by `active=0` (a subscription flag) rather than
> by `delisted_at` (a market fact). 706 confirmed-dead names were added
> 2026-08-02 and this study has not been re-derived against them.

## 8. Done-when, against the brief

| criterion | status |
|---|---|
| delisting history is realistic | **partly** — 716 stamps, plausible 2020–2022, thin and unverified 2023–2025 |
| A3 "Survivorship closed" can truthfully be marked met | **NO.** It may be marked *"materially closed 2019–2022; residual 2023–2025 gap quantified in P3A §3"* |
| or the deck explicitly says results remain contaminated | **the §7 label is the required wording** for the two PRE studies not yet re-run |

## 9. Open

- 735 symbols are `active=0` with no `delisted_at`. Some are genuinely delisted
  and unrecorded; some are simply unwatched. Until they are separated, the
  registry's own survivorship completeness stays at 97.9% rather than 100%.
- The `ATC` row needs splitting into two symbols (§4).
- 2023–2025 needs a second source (Nasdaq Trader symbol-directory diffs, or SEC
  Form 25 with CIK→ticker resolution improved beyond the current 249/841).
