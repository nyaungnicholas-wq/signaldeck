# SignalDeck Storage Strategy — Optimal Free Data Permanence

**One-paragraph recommendation (the TL;DR):**
Keep the SQLite WAL database as the single hot store — it is fast, transactional,
and comfortably handles many GB on a single Mac, so there is no reason to add a
second engine yet. Bound its growth with **aggressive tiered retention** (short
hot windows per timeframe) while **never truly deleting anything**: every row
past its hot window is first exported to a compressed **gzip-CSV cold archive**
(stdlib only, DuckDB/pandas-readable) and only then pruned, with a fail-safe that
skips the prune if the archive write fails. Daily bars are the permanent record
and are never pruned; reconsider a DuckDB/Parquet cold store only once the
archive grows past tens of GB or research queries over it become routine.

---

## The three storage tiers

| Store | Engine | What lives here | Retention |
|-------|--------|-----------------|-----------|
| **Hot** | SQLite (WAL) `data/signaldeck.db` | everything the app reads live | tiered, per table (below) |
| **Cold archive** | gzip-CSV files `data/archive/<table>/…` | every row before it is pruned from hot | grow-only, never deleted |
| **Backups** | `VACUUM INTO` snapshots `data/backups/` | consistent full-DB copies | nightly, keep 7 |

### Tiered retention (all windows env-tunable)

Enforced by three sibling workers in `internal/maintain` — the `Downsampler`
(bars/snapshots/anomalies), the `ScoresCompactor` (near-tier blob strip +
daily-downsample of the score tables), and `DerivedRetention` (far tier of the
derived tables) — archive-before-prune at every tier:

| Data | Hot window (default) | Env override | On expiry |
|------|----------------------|--------------|-----------|
| `snapshots_1s` (crypto 1Hz bid/ask) | **6 hours** | `SIGNALDECK_SNAP_RETENTION_H` | archive → prune (redundant with real crypto 1m bars) |
| `bars` 1m | **60 days** | `SIGNALDECK_1M_RETENTION_D` | compact → 1h **+** archive raw → prune |
| `bars` 1h | **3 years** | `SIGNALDECK_1H_RETENTION_D` | compact → 1d **+** archive raw → prune |
| `bars` 1d (daily) | **forever** | — (refused in code) | never archived, never pruned |
| `anomalies` | **90 days** | `SIGNALDECK_ANOM_RETENTION_D` | archive → prune (descriptive detections, not market history) |
| `scores` / `composite_scores` heavy JSON blobs | **2 days** | `SIGNALDECK_SCORES_HEAVY_RETENTION_D` | archive full rows → strip blobs (numeric row stays hot) |
| `scores` / `composite_scores` intraday rows | **30 days** | `SIGNALDECK_SCORES_INTRADAY_RETENTION_D` | daily-downsample — keep the daily-last row per (symbol, horizon) |
| `scores` + `score_outcomes` (far tier) | **90 days** | `SIGNALDECK_SCORES_RETENTION_D` | archive → prune |
| `features` (resolved rows only) | **180 days** | `SIGNALDECK_FEATURES_RETENTION_D` | archive → prune (an unlabeled training row is never deleted) |
| `filings` | **180 days** | `SIGNALDECK_FILINGS_RETENTION_D` | archive → prune |
| `insights` | **90 days** | `SIGNALDECK_INSIGHTS_RETENTION_D` | archive → prune |
| `prediction_postmortems` | **180 days** | `SIGNALDECK_POSTMORTEM_RETENTION_D` | archive → prune (the miss *explanations* age out; the predictions they explain stay) |
| `research_weeks` | **7-year window** | `SIGNALDECK_RESEARCH_WEEKS_RETENTION_D` | archive → prune; the history-backfill recompute floor moves in lockstep so pruned rows never resurrect |
| `predictions` + `prediction_outcomes` (the prediction ledger) | **forever** | — (by doctrine) | never pruned — the append-only track record |
| `sentiment_daily` | **forever** | — | one small row per symbol-day; negligible growth |

"Compact → coarser" means information is preserved in a smaller form *before* the
raw rows are archived and pruned, so nothing is lost from the live store either —
the coarse bar stays hot, the raw bars move to cold storage.

---

## Cold archive format (why gzip-CSV, and how to read it)

- **Path:** `data/archive/<table>/<symbol>_<fromTs>-<toTs>.csv.gz`
  (e.g. `data/archive/bars_1m/AAPL_1719792000-1719878340.csv.gz`).
  Override the root with `SIGNALDECK_ARCHIVE_DIR`.
- **Format:** ordinary CSV with a header row and typed columns, gzip-compressed —
  `compress/gzip` + `encoding/csv`, **standard library only, no new dependencies.**
- **Fail-safe writes:** each file is written to a `.tmp` sibling, fsynced, and
  atomically `rename`d into place; a crash mid-write can never leave a truncated
  file that a later prune would trust. If archiving fails, the matching prune is
  **skipped** (rows stay hot) and a `dq_events` record + log line make it visible.
  Nothing is ever lost silently.

**Read it back for research — no import step needed:**

```sql
-- DuckDB: query the whole cold archive in place, gzip and globbing handled natively
SELECT symbol, count(*), min(ts), max(ts)
FROM read_csv_auto('data/archive/bars_1m/*.csv.gz')
GROUP BY symbol;
```

```python
# pandas
import pandas as pd, glob
df = pd.concat(pd.read_csv(f) for f in glob.glob('data/archive/bars_1m/*.csv.gz'))
```

Columns are typed and stable: bars → `symbol_id,symbol,tf,ts,open,high,low,close,volume`;
snapshots → `symbol_id,symbol,ts,bid,ask,mid,wmid,imb_signed,spread,apply_lat_ns`.

---

## Storage governor (keeping the file bounded)

A `storage-governor` worker (`internal/maintain`, hourly) keeps on-disk footprint
bounded independently of logical retention:

- **`PRAGMA wal_checkpoint(TRUNCATE)` every pass** — a busy WAL database grows its
  `-wal` sidecar without bound until a checkpoint flushes it back into the main
  file; TRUNCATE also returns that space to the filesystem. Cheap and always safe.
- **`VACUUM` when the DB exceeds a threshold** (default 2 GB,
  `SIGNALDECK_VACUUM_THRESHOLD_MB`) — the DB is `auto_vacuum=NONE`, so pages freed
  by retention deletes are reused but not returned to disk until a VACUUM rewrites
  the file. VACUUM briefly takes a write lock, so it is gated behind the size
  threshold **and** a once-per-day minimum interval (a `meta` cursor).

Growth is visible + provable on **/quality → "DATA GROWTH — NOTHING IS THROWN
AWAY"**: per-table row counts and spans, `db`/`wal`/`archive` byte sizes, and the
active retention windows, all served from `/api/datastats`.

---

## Capacity math (why SQLite is fine for a long time)

> **Measured 2026-08-13 — the estimate below is stale by ~98x, and it is the
> PREMISE that went stale, not the arithmetic.** It is written for "~30 symbols".
> The fleet is now **2,950 symbols** (`symbols` table; `bars` covers 2,947), so
> every per-symbol line below is off by that factor and the conclusion no longer
> follows. Actual footprint on this machine:
>
> | tier | documented expectation | measured |
> |---|---|---|
> | hot `data/signaldeck.db` | "low-hundreds-of-MB, stops growing" | **4.8 GB**, still growing |
> | cold `data/archive/` | grows slowly | **1.1 GB** across 7 table dirs |
> | `data/backups/` | — | **19 GB** |
> | `freeze/` snapshots | — | **8.6 GB** |
> | **total** | — | **~34 GB** |
>
> Retention is NOT broken — this is the size *with* tiered retention, cold
> archiving, and a VACUUM (2026-08-12 11:25 UTC) all working, and
> `dq_events` records zero `vacuum_skip` rows. The governor does its job; the
> capacity paragraph simply describes a fleet that no longer exists. Re-derive
> it against the real symbol count before citing it in any sizing decision.

At a ~30-symbol fleet (the original `SIGNALDECK_SYMBOL_CAP` assumption):

- 1m bars, 60-day hot window ≈ 30 syms × ~390 min/day × 60 d ≈ **0.7M rows** (~tens of MB).
- 1h bars, 3-year hot window ≈ 30 × ~7 h/day × 750 trading days ≈ **0.16M rows**.
- daily bars, forever ≈ 30 × ~250/yr — negligible; grows ~7.5k rows/yr.
- snapshots_1s at 6 h ≈ tiny; the bulk of their history lives in cold archive.

At that fleet size the hot DB would stabilize in the low-hundreds-of-MB range,
while the cold archive grows slowly and compresses ~10× as gzip-CSV. That
conclusion holds only at that fleet size — see the measured table above.

---

## When to graduate to DuckDB / Parquet (not yet)

Stay on gzip-CSV + SQLite until one of these is true, then move the **cold tier**
(not the hot store) to Parquet and query it with DuckDB:

1. The cold archive passes ~tens of GB and CSV scan latency for research hurts
   (Parquet's columnar layout + predicate pushdown is dramatically faster).
2. Research/backtests over the full history become routine, not occasional
   (a DuckDB-over-Parquet lake is the natural analytics engine, still free/local).
3. Multiple machines need to share the archive (object storage + Parquet).

Even then the hot path stays SQLite: DuckDB/Parquet is an additive research tier,
not a replacement. The gzip-CSV files convert to Parquet in one DuckDB `COPY`
statement, so nothing about today's format blocks that future.

---

## Env var reference (all optional; defaults are production-safe)

| Var | Default | Effect |
|-----|---------|--------|
| `SIGNALDECK_ARCHIVE_DIR` | `<db-dir>/archive` | cold-archive root |
| `SIGNALDECK_SNAP_RETENTION_H` | `6` | snapshots_1s hot window (hours) |
| `SIGNALDECK_1M_RETENTION_D` | `60` | 1m bars hot window (days) |
| `SIGNALDECK_1H_RETENTION_D` | `1095` | 1h bars hot window (days) |
| `SIGNALDECK_ANOM_RETENTION_D` | `90` | anomalies hot window (days) |
| `SIGNALDECK_SCORES_HEAVY_RETENTION_D` | `2` | scores/composite full-blob hot window (days) |
| `SIGNALDECK_SCORES_INTRADAY_RETENTION_D` | `30` | scores/composite intraday-row hot window (days) |
| `SIGNALDECK_SCORES_RETENTION_D` | `90` | scores + score_outcomes far-tier hot window (days) |
| `SIGNALDECK_FEATURES_RETENTION_D` | `180` | resolved-features hot window (days) |
| `SIGNALDECK_FILINGS_RETENTION_D` | `180` | filings hot window (days) |
| `SIGNALDECK_INSIGHTS_RETENTION_D` | `90` | insights hot window (days) |
| `SIGNALDECK_POSTMORTEM_RETENTION_D` | `180` | prediction_postmortems hot window (days) |
| `SIGNALDECK_RESEARCH_WEEKS_RETENTION_D` | `2555` | research_weeks active window (days, ~7y) |
| `SIGNALDECK_VACUUM_THRESHOLD_MB` | `2048` | DB size above which the governor VACUUMs |

Daily bars have no retention knob by design: `store.PruneBars` refuses `tf=1d`,
so the permanent record cannot be pruned by any caller, present or future.
