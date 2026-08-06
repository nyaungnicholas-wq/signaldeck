<!-- BEGIN GENERATED deck_facts -->

Measured from `data/signaldeck.db` by `tools/deck_facts.py`. Do not edit by hand — CI fails when this block no longer matches the database. The universe reaches **2026-08-05**, the last observation day it holds.

| Measurement | Value |
|---|---|
| `universe_membership` rows | 2,642,060 |
| — observation days | 2,146 |
| — distinct symbols | 2,947 |
| — `source` values present | `bars-1d` |
| `symbols.delisted_at` stamps | 1,865 |
| — delisted 2020-2022 | 622 |
| — delisted 2023-2025 | 1,121 |
| — recent window against earlier | **180.2%** of the 2020-2022 count |
| Daily-bar calendar (from `SPY`) | 1,909 sessions |
| Stock bar coverage | 90.67% — 2,652,817 of 2,925,820 symbol-days over 2,940 symbols |
| — still-listed names only | 99.58% over 1,075 symbols |
| — names carrying `delisted_at` only | 79.10% over 1,865 symbols |
| — symbols that stop printing early with no `delisted_at` | 28 |
| Crypto bar coverage | 100.00% over 7 symbols |

The membership derives entirely from the daily-bar history, so it is point-in-time only to the extent that history is complete: the stock coverage row is the bound under every point-in-time claim in this deck. **Read the two cohort rows before the blended one.** They answer different questions — the still-listed row is whether the live universe has holes, the delisted row is how densely the imported dead names were ever sampled — and while dead names are being imported the blended figure moves with the import rather than with data quality. The symbols that stop printing with no `delisted_at` are the survivorship-relevant ones: they leave the universe without being recorded as dead, which is indistinguishable from having stopped looking.

<!-- END GENERATED deck_facts -->
