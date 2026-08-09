<!-- BEGIN GENERATED deck_facts -->

Measured from `data/signaldeck.db` by `tools/deck_facts.py`. Do not edit by hand — CI fails when this block no longer matches the database. The universe reaches **1970-01-03**, the last observation day it holds.

| Measurement | Value |
|---|---|
| `universe_membership` rows | 6 |
| — observation days | 3 |
| — distinct symbols | 3 |
| — `source` values present | `bars-1d` |
| `symbols.delisted_at` stamps | 4 |
| — delisted 2020-2022 | 1 |
| — delisted 2023-2025 | 2 |
| — recent window against earlier | **200.0%** of the 2020-2022 count |
| Daily-bar calendar (from `SPY`) | 10 sessions |
| Stock bar coverage | 90.00% — 18 of 20 symbol-days over 2 symbols |
| — still-listed names only | 100.00% over 1 symbols |
| — names carrying `delisted_at` only | 80.00% over 1 symbols |
| — symbols that stop printing early with no `delisted_at` | 0 |
| Crypto bar coverage | 100.00% over 1 symbols |

The membership derives entirely from the daily-bar history, so it is point-in-time only to the extent that history is complete: the stock coverage row is the bound under every point-in-time claim in this deck. **Read the two cohort rows before the blended one.** They answer different questions — the still-listed row is whether the live universe has holes, the delisted row is how densely the imported dead names were ever sampled — and while dead names are being imported the blended figure moves with the import rather than with data quality. The symbols that stop printing with no `delisted_at` are the survivorship-relevant ones: they leave the universe without being recorded as dead, which is indistinguishable from having stopped looking.

<!-- END GENERATED deck_facts -->
