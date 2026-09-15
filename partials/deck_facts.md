<!-- BEGIN GENERATED deck_facts -->

Measured from `data/signaldeck.db` by `tools/deck_facts.py`. Do not edit by hand — CI fails when this block no longer matches the database. The universe reaches **2026-08-22**, the last observation day it holds.

| Measurement | Value |
|---|---|
| `universe_membership` rows | 2,697,299 |
| — observation days | 2,163 |
| — distinct symbols | 2,947 |
| — `source` values present | `bars-1d` |
| `symbols.delisted_at` stamps | 1,898 |
| — delisted 2020-2022 | 621 |
| — delisted 2023-2025 | 1,117 |
| — recent window against earlier | **179.9%** of the 2020-2022 count |
| Daily-bar calendar (from `SPY`) | 1,935 sessions |
| Stock bar coverage | 91.92% — 2,709,961 of 2,948,305 symbol-days over 2,940 symbols |
| — still-listed names only | 98.91% over 1,042 symbols |
| — names carrying `delisted_at` only | 83.31% over 1,898 symbols |
| — symbols that stop printing early with no `delisted_at` | 9 |
| Crypto bar coverage | 100.00% over 7 symbols |

The membership derives entirely from the daily-bar history, so it is point-in-time only to the extent that history is complete: the stock coverage row is the bound under every point-in-time claim in this deck. **Read the two cohort rows before the blended one.** They answer different questions — the still-listed row is whether the live universe has holes, the delisted row is how densely the imported dead names were ever sampled — and while dead names are being imported the blended figure moves with the import rather than with data quality. The symbols that stop printing with no `delisted_at` are the survivorship-relevant ones: they leave the universe without being recorded as dead, which is indistinguishable from having stopped looking.

<!-- END GENERATED deck_facts -->
