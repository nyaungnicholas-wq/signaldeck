# forecastplan — work package 1: data, labels, population and collapse audit

Research-only. Reads `data/signaldeck.db` in `mode=ro`, writes only under `research/forecastplan/out/`.
Production models, the pinned grader (`tools/accuracy_registry.py`) and the prereg chain are untouched.
Plan of record: the 2026-09-08 "research before replacement" plan (memory `project_signaldeck_forecastplan_wp1`).

## Files

| File | Purpose | Check |
|---|---|---|
| `labels.py` | Pure-Python port of the Go resolvers (`structregime` ResolveTrendAt / ResolveLiquidityAt / ResolveVol21At and the Naive* persistence nulls), `bar_returns2`, `direction_up` | `python labels.py` prints `SELFCHECK OK` |
| `../../daemon/cmd/label-parity/main.go` | stdin/stdout JSON harness over the real Go resolvers; changes no production code | `go vet ./cmd/label-parity` |
| `parity_check.py` | Runs constructed edge cases plus real series from the DB through both implementations | `python parity_check.py` prints `PARITY OK cases=287 functions=6 mismatches=0` |
| `auditlib.py` | Constants (with their production origin), helpers, `SCHEMA_SQL`, deterministic fixture | `python auditlib.py` prints `SELFCHECK OK` |
| `audit_ledger.py` | Eligibility ledger, membership gap, HAR screen bias, history depth | `--selfcheck`; real run prints `LEDGER OK symbols=2950 uncovered_days=18` |
| `audit_provenance.py` | Provenance manifest and `out/summary.md` | `--selfcheck`; real run prints `PROVENANCE OK ...` |
| `audit_collapse.py` | Collapse traces for predictions and outcomes, registry cross-check | `--selfcheck`; real run prints `COLLAPSE OK pred_days=136 outcome_days=126 flagged=22 exact=0` |
| `audit_withheld_counterfactual.py` | What the graded record WOULD have held had the cross-section gate not withheld 2026-09-10..09-18. Replicates the Go outcome rule and PROVES it against already-graded rows before trusting it | real run prints `PARITY: ... DIRECTION mismatches` then `COUNTERFACTUAL OK` |

Interpreter: `.venv/Scripts/python.exe` from the repository root.

## Results so far (2026-09-08, HEAD 2508b41)

Label parity: 287 cases (31 constructed, 256 real over 32 symbols), six functions, zero mismatches.
The ok/not-ok counts are identical per function (246/276/282/247/276/275 of 287), so the
degenerate paths (ties, NaN medians, short windows, negative t) are exercised, not skipped.

Membership gap: `universe_membership` is a full rebuild from `bars-1d` and stops at 2026-08-22;
daily bars run to 2026-09-09. Eighteen calendar days and 11,287 symbol-days have bars without
membership. Any evaluation that relies on that table for cross-sectional denominators must stop at
2026-08-22 or rebuild first.

HAR universe screen (stocks, 2,943 symbols; 2,622 are delisted or inactive):

| Screen | Passers | Delisted/inactive among them | Share |
|---|---:|---:|---:|
| bars >= 530 and flat share < 1% | 1,285 | 1,010 | 78.6% |
| ... and has fundamentals (`--companies-only`) | 762 | 526 | 69.0% |

`--companies-only` removes 484 of 1,010 dead passers (48%) but only 39 of 275 surviving passers (14%).
The rule is differential, so the concern in the plan stands and is now quantified. The plain screen
also admits 71 funds and one closed-end fund; 14 funds survive `--companies-only`.

History depth (daily bars): stocks median 740 bars per symbol; 1,454 symbols have >= 756 bars and
737 have >= 756 bars before 2023-07-03, which is the population that can support three years of
initial training ahead of six 126-session blocks. Crypto is 7 symbols with 781 bars each starting
2024-07-11, so it cannot support that design.

Instrument classes (from `research/harness/instruments.py`): 2,384 common, 230 funds, 181 units,
51 warrants, 45 dotted-suffix, 32 closed-end, 12 rights, 8 preferred, 7 crypto.

Collapse (per-day tables in `out/collapse_trace_*.csv`; flag = fewer than one distinct probability
per ten symbols on a day with >= 30 symbols): 40 of 67 eligible 1d days and 18 of 69 1w days are
collapsed on the prediction side, through two different mechanisms. In July (1d 2026-07-27: 331
symbols, 280 distinct raw, 8 distinct calibrated) the fleet calibration map folds hundreds of raw
values onto a handful. From 2026-08-30 (329 symbols, 31-33 distinct, 90% with `n_used = 0`) it is
abstention: nine in ten symbols carry the same fallback value because no leg is admitted.

Registry cross-check: the 22 flagged days reproduce only when the registry's printed date is shifted
back one day (5 exact and 8 near of 22, versus 0 exact unshifted), so the grader labels a trading-day
index one calendar day later than `day_str(index)`. The residual gaps are the grader's settlement
and stale-feed clauses, which this trace does not replicate. The `1d#pm` / `1w#pm` benchmark rows
are one value per day by construction (18 and 24 flagged days) and must never enter a candidate set.

## Contract facts gathered for the timestamp ledger

- Stock daily bars are stamped at ET midnight: 04:00Z (EDT, 1.78M rows) and 05:00Z (EST, 0.92M rows).
  Crypto bars are 00:00Z. Grader trading day = `(ts - 18000) // 86400`.
- Direction outcome: base = settled bar at or before the prediction; forward = first bar at or after
  `base.ts + horizon - 6h`; requires a successor bar (settlement) and `fwd.ts - target <= 3 * horizon`;
  `up = fwd.close / base.close - 1 > 0` (a zero return is down). Training label is `close[i+h] > close[i]`.
- `prediction_outcomes` also holds `1d#pm` / `1w#pm` prequential-majority benchmark rows; exclude them
  from any candidate population.
- Regime outcome resolver loads bars from `call_ts - 760 days` with `close > 0`, so SMA200 needs 200
  bars inside that window; `rollMean` is a running sum and one NaN poisons every later value.

## Status

All six scripts pass their selfchecks and have been run against the live database (read-only).
Remaining work-package items not started: the per-origin eligibility ledger keyed by origin
(this ledger is per symbol), the timestamp contract tests (forming bars, holidays, DST, crypto UTC
boundaries), the feature-availability ledger, and the matched candidate/incumbent/baseline table.
Nothing here is committed.
