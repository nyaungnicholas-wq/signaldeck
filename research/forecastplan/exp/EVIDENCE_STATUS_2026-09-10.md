# SignalDeck forecast quality — evidence status, 2026-09-10

Three outcomes, kept apart: **(1) engineering defects repaired** (audits/2026-09-10-forecast-repair-reaudit.md,
commit 87e7f57, tested, NOT deployed), **(2) historical research** (this file; manifest `forecastplan-exp-v1`
frozen before any fit), **(3) prospective evidence** (unchanged: the pinned grader's REFUSED envelope, prereg
seq 87 at 3/60 sessions, seq 105 at 4 qualifying call days). None of the three implies the next. Passing tests is
not forecasting skill, and nothing below is a publishable grade.

## 1. Evidence matrix (state on 2026-09-09/10)

| family / row | model, version | target, horizon | origin and availability rule | universe | baseline | rows / independent units | gate status and exact reason | source, reproduction | verified / inferred / unknown |
|---|---|---|---|---|---|---|---|---|---|
| directional-ensemble 1d | ensemble of admitted legs (pressure, expectancy, forecast, sentiment, gbm, meanrev, alphax), per-symbol or global isotonic map; revision d08d3f1 running | up = fwd close / settled base close − 1 > 0; forward bar = first bar at or after base + 1d − 6h | prediction at pass time from settled bars only (`loadBars` trims the forming bar); features include `pred_raw`/`pred_cal` (self-referential) | active stocks + crypto; 329-symbol hot set intraday, ~2,950 during the nightly sweep | prequential majority (`1d#pm`, committed daily) | n 2,893; 28 settle days; design effect 7.1x → effective n 408 | FAILED (worse than baseline), retire=true; whole registry REFUSED: 23 collapsed cross-sections in 83 days (07-27..08-06) | `data/accuracy_registry.json` (stale_last_registry); `.venv/Scripts/python.exe tools/accuracy_registry.py` (pinned sha a97e33…) | verified from the registry; within-day AUC 0.519 (se 0.017) by `audit_direction_information.py` |
| directional-ensemble 1w | same | forward bar at or after base + 7d − 6h | same | same | prequential majority `1w#pm` | n 6,102; 35 settle days, 6 credible | INSUFFICIENT DAYS (6/10 credible, 29 degenerate) | same | verified; within-day AUC 0.498 (se 0.009) |
| high-conviction 1d / 1w | same, |p−0.5| ≥ 0.15 | same | same | same | same | n 455 / 1,000; 5 / 6 credible days | INSUFFICIENT DAYS | same | verified |
| trend21 / liquidity21 / vol21 | deterministic rules (`structregime.go`): call = current side of SMA200 / trailing-median dollar volume / EWMA vol rank; conviction = trailing-200 percentile | resolver labels at t+21 sessions (`labels.py`, parity 287/0) | frozen daily calls with `naive_label` since 2026-07-28; calls before 07-27 carry no baseline | active stocks | frozen persistence (`<kind>#persist`) | n 3,155 / 3,138 / 3,166; 11 / 11 / 10 call days; **1 of 10 non-overlapping 21-day blocks** | INSUFFICIENT BLOCKS (1/10); acc 79.6 / 69.4 / 48.6 vs persistence 79.8 / 68.4 / 47.6 | registry; `audit_structural_cohorts.py` | verified; block 10 cannot resolve before ~2027-03-05 |
| crypto trend21 / liquidity21 | same rules on 7 crypto symbols | same | same | 7 symbols | persistence | n 93 each; 1 block | INSUFFICIENT BLOCKS | registry | verified; 7 symbols cannot support a study |
| filingsdrift21 | reaction-day sign persistence after a 10-Q/10-K | sign of the 21-session return | frozen within 7 days of `filed_ts`, reaction bar = first session at or after filing | tracked stocks with filings | none frozen (claim 0.50) | n 141; 1 block | NO BASELINE; INSUFFICIENT BLOCKS | registry | verified; baseline proposal in §5 |
| trend63 | same rule, 63-day horizon | side of SMA200 at t+63 | same | active stocks | persistence (frozen) | 0 resolved | PENDING, first grade 2026-09-25 | registry | verified |
| HAR realized variance (seq 105) | HAR on RV^GK, refit per pass, both nulls frozen per row | mean RV over t+1..t+h, h ∈ {1, 5} | call bar = last daily bar (defective until 87e7f57: forming and stale bars accepted) | active stocks with ≥ 530 bars | EWMA(0.94), random walk | 2,001 resolved h=1 rows; **4 qualifying call days (≥ 30 symbols), 3 resolved**; 306 stale-bar rows ungradable | INSUFFICIENT: 60 qualifying days required; 60th not before 2026-11-27 | `audit_rv_coverage.py` → `out/rv_coverage_by_day.csv` | verified; "223 calendar days" in the snapshot was the stale-bar artefact |
| paper books | flagship-1d / flagship-1w (live), two replay reconstructions (cursor stopped 2026-08-19), manual:u5 | triple-barrier exits since 2026-08-04 | as-of clock on settled bars since 2026-09-07 | confluence long book, ≥ $20 | matched same-session universe (seq 87) | live: 0 + 1 open positions; replays 11; manual 1; cash reconciles to 0.00 on all five | seq 87: 3/60 eligible sessions, mean excess −0.244% | `tools/forward_test.py --verdict` (dry run) | verified; "13 open positions" conflates the finished replays with the live book |

Reconciliation with the supplied snapshot: every row count and rate in the snapshot matches `stale_last_registry`
graded at 2026-09-09T16:48:38. The "internal structural diagnostics" (79.3 / 69.3 / 56.2 / 43.6) are the all-resolved
cohort including the unbaselined July 18-26 calls; the pinned grades are the frozen-baseline cohort after the
settlement and stale-feed clauses (vol21 56.2 → 48.1 → 48.6). Same labels, same horizon, different cohort.

## 2. Root causes and repairs (engineering)

Detailed in the ledger F1-F7. Fixed and tested (87e7f57): rv call bar must be the last completed session and is
frozen at first write (F1, F2); the publication gate's window starts at the survivorship epoch instead of a sliding
newest-N slice that would have opened within two weeks (F3); the prediction runner no longer mints on stale daily
series (F4). Refuted as defects: the diagnostic-vs-grade gap (F5), the paper book count (F6), the rv coverage
expectation (F7). **Deployed 2026-09-10 03:2x UTC on Nicholas's authorisation** (`deploy VERIFIED: daemon is
running commit b84670c9…`); the first live passes read `froze 0 ... (564 already frozen by an earlier pass);
3 symbol(s) skipped: last bar forming or stale` and `1 stock(s) skipped: daily series stale by two or more sessions`,
and the shared gate run against a registry with rows refuses on 18 collapsed cross-sections of 74 epoch-window days
(pre-epoch days gone). Rollback: `git revert 87e7f57` then `bash ops/signaldeck-ctl.sh deploy`.

Collapse mechanisms (from WP1 and this pass): July 2026 = the fleet calibration map folding ~280 raw values onto
5-14; from 2026-08-06 = abstention (no admitted 1d leg), which writes evidence rows without outcome rows. Both are
gated in code today (dispersion gate; `n_used = 0` rows never reach `prediction_outcomes`). The contaminated window
stays withheld; no forecast was relabelled or removed.

## 3. Research under manifest forecastplan-exp-v1 (historical, out of sample, not publishable)

Datasets: `research/dirfix/panel.parquet` and `features_v2.parquet` (hashes in the manifest), re-aligned so an
origin's features use data through its own close; 1,082,271 eligible direction rows over 1,664 sessions
(2019-12-30..2026-08-13) and 1,102 symbols; 1,838,635 structural rows over 2,047 symbols (2020-01-10..2026-08-28).
Protocol: six chronological 126-session outer blocks (2023-07-26..2026-08-13 for direction), expanding training
window purged by the horizon and embargoed 21 sessions, three inner folds for selection, calibration and abstention
thresholds fit on inner out-of-sample predictions only, matched baselines, block bootstrap, Holm over the 48-row
family. Trial ledger: `out/ledger.jsonl` — 1,278 records, 1,216 fits, 14 stopped candidates, 88 minutes of fitting
(`out/ledger_summary.md`).

**Direction (h = 1 and 5 sessions).** No candidate beats the prequential majority. h=1 best pooled skill +0.37 pp
(logistic, CI21 −0.67..+1.45, p_le_0 0.25, 2 of 6 blocks ≥ 1 pp, within-day AUC 0.515); h=5 best +0.06 pp
(market-only logistic, CI −0.16..+0.29). Boosted trees stopped after block 3 under the frozen rule. Isotonic
variants of non-discriminating models collapse to the base rate (hc20 coverage → 1.0), which is calibration doing
its job, not a leak. Always-up equals the baseline (50.97% / 52.17%); return persistence is −1.5 / −2.6 pp.
Controls (within-day shuffle, 21-session lag, noise): all within ±1.5 pp with intervals including zero; not void.
Ablations on the logistic: cross-sectional-only +0.84 pp at h=1 (CI +0.01..+1.33, uncorrected p 0.0245; ≥ 1.0 after
Holm over the family), nothing at h=5. Matched live window (exploratory, contaminated, 2026-07-24..08-13): h=1
incumbent 42.3%, candidate 47.7%, always-up 55.1%; h=5 incumbent 41.9%, candidate = always-up 62.5%. Neither the
incumbent nor any candidate beats the base rate there.

**Structural.** trend21: every model within ±0.06 pp of persistence (82.4% screened, 83.3% unscreened); the
incumbent equals persistence by construction (agreement 1.0). vol21: best +0.81 pp screened (CI −1.20..+2.62) and
+1.08 pp unscreened (CI −0.32..+2.15, 4 of 6 blocks), lower bounds below zero. liquidity21: the boosted
transition model is the one lead: +1.02 pp screened (CI63 +0.26..+1.80, p_le_0 0.002, family Holm 0.094, blocks
≥ 1 pp 3 of 6: +1.14, +0.51, +1.58, −0.35, +2.32, +0.91; grid offsets 7/14: +0.79/+0.76; balanced accuracy 0.681 vs
0.669) and +0.68 pp unscreened (CI +0.24..+1.16, p_le_0 < 0.0005, family Holm ≈ 0, 1 of 6 blocks). It predicts a
transition on 7% of origins (31% actually transition), consistent with the repo's geometry finding that log dollar
volume mean-reverts across its median. **It fails the frozen MPUI on block consistency and is not promoted.**

**Volatility (continuous).** Registered metrics untouched. Candidate study on the tool's universe: the 40-symbol
smoke run (1,027 day clusters) shows no candidate separating from the incumbent HAR (h=1: V3 −0.0048 QLIKE, DM t
−0.72; V1 and V2 worse; h=5: V1/V3 ≈ −0.011, |t| < 1.2; Holm 1.0) while HAR beats EWMA (t 1.98) and the random walk
(t 7.6) as the registration's backtest stated.

Full-universe run (`out/vol_summary.md`, `out/vol_results.json`; 1,292 symbols, 1,242,726 matched rows at h=1,
1,382 day clusters, refit every 5 sessions): V1 (HAR plus the cross-sectional mean log-RV term) lowers QLIKE
against the incumbent HAR by 1.3% at h=1 (mean diff −0.0077, DM t −1.76, uncorrected p 0.079, Holm 0.47; better in
5 of 6 calendar years, worse in 2021) and by 1.4% at h=5 (t −1.28, p 0.20). V2 (leverage term) is worse (+0.090 at
h=1, driven by a 2021 blow-up), V3 (63-session term) is flat. HAR beats EWMA by 0.073 (t 7.1) and the random walk by
0.72 (t 7.1). Under the registered decision rule (DM, Bonferroni/Holm over the family) no candidate clears 0.05; the
block-bootstrap interval for V1 at h=1 (−0.0173, −0.00003) just excludes zero, the known disagreement between the
two inferences on this loss, and the DM rule governs. V1 is a mild, directionally consistent lead that does not meet
the manifest's MPUI (≥ 1% relative with a corrected lower bound above zero); nothing is promoted and seq 105 is
untouched.

Reviews: `out/review_interim_direction_2026-09-10.md`, `out/review_final_2026-09-10.md` (independent workers);
both reach the same verdict. Adjudication of their objections: the market-only model winning inner selection is
the log-loss criterion rewarding a day-level base-rate model, not look-ahead; the noise control's zero-width interval
is a constant predictor coinciding with the baseline and says nothing about label leakage (that is what the shuffle
and lag controls test).

## 4. Publication status

Unchanged and correct: the registry is REFUSED (collapsed cross-sections inside the incumbent's own graded window),
the directional 1d row is FAILED with retire=true, every structural row is INSUFFICIENT BLOCKS, seq 87 and seq 105
are far below their floors. No candidate from this campaign qualifies for a shadow series under the manifest's
promotion rule, so no shadow was created, no identifier issued, and the incumbent stays in place.

## 5. What requires new data, future sessions, or Nicholas

- Deployed; the rv record accrues under the new rules from the next session.
- Structural verdicts need 10 non-overlapping 21-day blocks with frozen baselines: block 10 opens ~2027-02-02 and
  resolves ~2027-03-05. Trend63 first grades 2026-09-25. Seq 105 needs 60 qualifying days (not before 2026-11-27).
  Seq 87 needs 60 eligible sessions (3 so far). Calendar time is not qualifying evidence; gaps push these dates back.
- FilingsDrift21 baseline (proposal, not filed): freeze a constant "up" null per call, graded by the same resolver,
  under a NEW kind identifier so the old un-baselined rows keep grading NO BASELINE. Filing it appends to the prereg
  chain (irreversible), so it is Nicholas's decision.
- The liquidity21 transition lead may only enter a prospective test as a separately pre-registered, separately
  identified series with fixed hyper-parameters and its own floors; nothing here authorises that.

## 6. Final table

| Forecast family | Proven defect | Repair | Best candidate | Matched baseline delta and uncertainty | Independent evidence | Publication status | Next required evidence |
|---|---|---|---|---|---|---|---|
| Direction 1d | collapsed July window (calibration fold, then abstention); stale-feed minting; fail-open gate window | gate window from the epoch; stale-feed guard (87e7f57); abstention already gated | logistic, cross-sectional features | +0.37 pp pooled vs prequential majority, CI −0.67..+1.45, Holm 1.0; live window: incumbent 42.3% vs always-up 55.1% | 6 outer blocks, 1,664 sessions; controls clean | REFUSED / FAILED, retire=true, retained | none can qualify: no per-symbol information found (within-day AUC 0.52) |
| Direction 1w | same | same | market-only logistic | +0.06 pp, CI −0.16..+0.29 | 6 blocks | REFUSED / INSUFFICIENT DAYS | same |
| Trend21 | none (diagnostic vs grade is a cohort difference) | none needed | none (all equal persistence) | 0.00 ± 0.1 pp vs persistence | 6 blocks, 50,590 grid rows | INSUFFICIENT BLOCKS 1/10 | 9 more blocks (~2027-03) |
| Liquidity21 | none | none | HGB transition model | +1.02 pp screened (CI +0.26..+1.80, Holm 0.094, 3/6 blocks); +0.68 pp unscreened (CI +0.24..+1.16, 1/6 blocks) | 6 blocks, two universes, two grid offsets | INSUFFICIENT BLOCKS 1/10 | 9 more blocks; the lead needs its own pre-registration before any prospective use |
| Vol21 | none | none | HGB transition model | +0.81 pp (CI −1.20..+2.62) | 6 blocks | INSUFFICIENT BLOCKS 1/10 | 9 more blocks |
| Crypto trend21 / liquidity21 | none | none | not studied (7 symbols) | n/a | n/a | INSUFFICIENT BLOCKS | cannot be studied at this universe size |
| FilingsDrift21 | no frozen baseline by design | proposal only (§5) | not studied (no availability-timestamped history) | n/a | n/a | NO BASELINE | new kind with a frozen null, then 10 blocks |
| Trend63 | none | none | not studied | n/a | n/a | PENDING 2026-09-25 | first resolutions |
| HAR realized variance (seq 105) | forming and stale call bars; forecasts rewritten until resolution | settled-and-fresh call bar, freeze once (87e7f57, deployed as b84670c) | V1 = HAR + market log-RV term | −1.3% QLIKE vs HAR at h=1 (DM t −1.76, Holm 0.47; 5 of 6 years), −1.4% at h=5 (t −1.28); HAR beats EWMA and RW (t 7.1) | 1,292 symbols, 1,382 day clusters, 2021-2026 | INSUFFICIENT: 4 qualifying days of 60 (registered test) | 56 more qualifying days (≥ 2026-11-27); V1 would need its own registration to be tested prospectively |
| Paper book / seq 87 | none (cash reconciles; count conflated replays) | none | n/a | 3 sessions, mean excess −0.24% | 3 of 60 sessions | INSUFFICIENT EVIDENCE | 57 more eligible sessions |

## 7. Files, tests, commands

Commits on public-launch: 87e7f57 (daemon), 1b76fac, 63449b1, 9dd47de, d8cca71, 1590959, e5aaf42, 9bdde16,
fc9effb, 98237e9, 5c9ba12 (research), 6c157bb (ledger). Tests executed: `go test ./...` clean including
TestRVForecastRunnerRefusesFormingAndStaleCallBars, TestRVForecastRunnerFreezesOnce, TestFreezeRVForecastReportsInsertion,
marketcal session tests, the collapse-gate suite; Python selfchecks of every exp module; `tools/audit_register.py` OK.
Reproduce: `.venv/Scripts/python.exe research/forecastplan/exp/data_direction.py`, `... data_structural.py`,
`... run_direction.py --horizons 1,5`, `... direction_controls.py --what controls,ablations --ablation-model M1_c0.1`,
`... run_structural.py`, `... run_vol.py`, `... ledger_summary.py`, each printing its OK token; audits as listed in
the ledger. Not done: filingsdrift registration (decision), full volatility results (pending at write time; see §3
addendum when present).
