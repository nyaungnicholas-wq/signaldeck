## Eligibility

| metric | value |
|---|---|
| market:crypto | 7 |
| market:stocks | 2943 |
| instrument_class:closed-end | 32 |
| instrument_class:common | 2384 |
| instrument_class:crypto | 7 |
| instrument_class:dotsuffix | 45 |
| instrument_class:fund | 230 |
| instrument_class:suffix-P | 8 |
| instrument_class:suffix-R | 12 |
| instrument_class:suffix-U | 181 |
| instrument_class:suffix-W | 51 |
| har_screen_pass | 1292 |
| har_companies_only_pass | 762 |

Source: eligibility_ledger.csv

## Membership gap

| metric | value |
|---|---|
| membership_max_day_utc | 2026-08-22 |
| bars_1d_max_day_utc | 2026-09-09 |
| n_uncovered_days | 18 |
| n_uncovered_symbol_days | 11287 |

Source: membership_gap.json

## HAR screen bias

| metric | value |
|---|---|
| delisted_removed_by_companies_only | 484 |
| instruments_import_error | None |
| n_delisted | 2622 |
| n_pass_companies_only | 762 |
| n_pass_companies_only_delisted | 526 |
| n_pass_screen | 1285 |
| n_pass_screen_delisted | 1010 |
| n_stock_symbols | 2943 |
| n_with_fundamentals | 866 |
| n_with_fundamentals_delisted | 604 |
| share_pass_companies_only_delisted | 0.6903 |
| share_pass_screen_delisted | 0.786 |
| survivors_removed_by_companies_only | 39 |

Source: har_screen_bias.json

## History depth

| metric | value |
|---|---|
| crypto:n_symbols_with_bars | 7 |
| crypto:n_ge_500 | 7 |
| crypto:n_ge_756 | 7 |
| crypto:n_ge_756_before_2023_07_03 | 0 |
| crypto:p50 | 781 |
| stocks:n_symbols_with_bars | 2940 |
| stocks:n_ge_500 | 1836 |
| stocks:n_ge_756 | 1454 |
| stocks:n_ge_756_before_2023_07_03 | 737 |
| stocks:p50 | 740 |

Source: history_depth.json

## Collapse

| metric | value |
|---|---|
| 1d:days | 67 |
| 1d:collapsed | 40 |
| 1d:min_n_distinct_cal | 1 |
| 1d:median_n_distinct_cal | 31.0 |
| 1w:days | 69 |
| 1w:collapsed | 18 |
| 1w:min_n_distinct_cal | 3 |
| 1w:median_n_distinct_cal | 64 |
| 1d:min_n_distinct_prob | 6 |
| 1d:median_n_distinct_prob | 31 |
| 1d#pm:min_n_distinct_prob | 1 |
| 1d#pm:median_n_distinct_prob | 1.0 |
| 1w:min_n_distinct_prob | 7 |
| 1w:median_n_distinct_prob | 223 |
| 1w#pm:min_n_distinct_prob | 1 |
| 1w#pm:median_n_distinct_prob | 1.0 |
| registry_status | REFUSED |
| n_flagged | 22 |
| n_exact | 0 |
| n_near | 3 |
| n_missing_in_trace | 5 |

Source: collapse_trace_predictions.csv

## Parity

| metric | value |
|---|---|
| n_cases | 287 |
| liquidity_mismatch | 0 |
| naive_liquidity_mismatch | 0 |
| naive_trend_mismatch | 0 |
| naive_vol21_mismatch | 0 |
| trend_mismatch | 0 |
| vol21_mismatch | 0 |

Source: parity_report.json

## Provenance

| metric | value |
|---|---|
| repo_head | 2508b416c9b3c6e8d788e6478cfecc34de973060 |
| repo_dirty_paths | 3 |
| db_size_bytes | 5623517184 |
| generated_at_utc | 2026-09-09T07:26:12.707304Z |

Source: provenance.json
