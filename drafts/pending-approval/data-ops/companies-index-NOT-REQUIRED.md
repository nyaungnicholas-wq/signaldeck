# /api/companies — no CREATE INDEX is required. Do not run one.

**Status: NOTHING TO APPROVE.** This file exists because the brief said "put any
`CREATE INDEX` in `drafts/pending-approval/data-ops/`". The measured answer is
that no index should be created. The fix is a pure query rewrite that needs no
database write at all — see `drafts/patches/companies-latestdailyall-perf.patch`.

No SQL in this directory is intended to be executed.

## The claim under test

`/api/companies` measured 13.0–20.0 s on the live daemon. The natural first
guess is a missing index on `bars`. That guess is wrong, and the wrongness is
measured, not asserted.

## What the slow query actually was

`store.LatestDailyAll` (`daemon/internal/store/companies.go:200`), called once
per request from `daemon/internal/api/companies.go:112` — its only caller.

```sql
SELECT symbol_id, ts, close, COALESCE(prev_close, 0), volume FROM (
  SELECT symbol_id, ts, close, volume,
         LAG(close)     OVER (PARTITION BY symbol_id ORDER BY ts)      AS prev_close,
         ROW_NUMBER()   OVER (PARTITION BY symbol_id ORDER BY ts DESC) AS rn
  FROM bars WHERE tf='1d'
) WHERE rn = 1
```

It evaluates two window functions over all 2,655,104 `tf='1d'` rows and then
keeps 2,947 of them. An index cannot fix that, because the problem is not
locating rows — it is that the query asks SQLite to rank every row it will
throw away, and the existing index's `ts ASC` ordering cannot satisfy the
`ROW_NUMBER() ... ORDER BY ts DESC`, so two temp B-trees get spilled.

## The benchmark that rules an index out

Run on a **throwaway copy** of `data/backups/signaldeck-20260806-131007.db`
placed in the session scratch directory, then deleted. The live database was
never written to. The index tested is the maximal covering index for this
query — it contains every column the query touches, in the query's own order:

```sql
CREATE INDEX idx_bars_probe ON bars (tf, symbol_id, ts, close, volume);
```

| Query | Without the index | With the index |
|---|---|---|
| Current window-function query | 3.526 s | 2.895 s |
| Rewritten query (the patch)   | 0.029 s | 0.025 s |

Reading the table:

- The index buys the **current** query 18% (3.526 → 2.895 s). It stays ~100×
  slower than the rewrite. Adding it and stopping there would have left the
  endpoint broken while looking like a fix had shipped.
- The index buys the **rewritten** query 4 ms, which is inside run-to-run
  noise. The rewrite already gets covering-index SEARCHes out of the existing
  `idx_bars_tf_sym_ts`.
- `CREATE INDEX` itself took **7.546 s** and would add a permanent second copy
  of five columns × 15,566,172 `bars` rows, plus write amplification on every
  bar the ingest workers insert, forever.

Cost: gigabytes of disk and a permanent tax on the hot write path.
Benefit: 4 ms, once, on one endpoint. That is a bad trade, so it is not offered.

## What to do instead

Apply `drafts/patches/companies-latestdailyall-perf.patch`. Measured on the
live database, read-only: **6.734 s → 0.026 s**, result set byte-identical
(`EXCEPT` in both directions returns 0 rows on both the live DB and the
backup).

## If someone still wants the index later

They should not, on this evidence. If the schema changes and the question is
reopened, re-run the table above first — the numbers, not the intuition, are
what settled it.
