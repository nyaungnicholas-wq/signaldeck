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

Enforced by the `Downsampler` worker (`internal/maintain`), archive-before-prune
at every tier:

| Data | Hot window (default) | Env override | On expiry |
|------|----------------------|--------------|-----------|
| `snapshots_1s` (crypto 1Hz bid/ask) | **6 hours** | `SIGNALDECK_SNAP_RETENTION_H` | archive → prune (redundant with real crypto 1m bars) |
| `bars` 1m | **60 days** | `SIGNALDECK_1M_RETENTION_D` | compact → 1h **+** archive raw → prune |
| `bars` 1h | **3 years** | `SIGNALDECK_1H_RETENTION_D` | compact → 1d **+** archive raw → prune |
| `bars` 1d (daily) | **forever** | — (refused in code) | never archived, never pruned |
| feature store, sentiment_daily, scores/outcomes | **forever** | — | never pruned (the learning flywheel's training set) |

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

At the current fleet (~30 symbols under `SIGNALDECK_SYMBOL_CAP`):

- 1m bars, 60-day hot window ≈ 30 syms × ~390 min/day × 60 d ≈ **0.7M rows** (~tens of MB).
- 1h bars, 3-year hot window ≈ 30 × ~7 h/day × 750 trading days ≈ **0.16M rows**.
- daily bars, forever ≈ 30 × ~250/yr — negligible; grows ~7.5k rows/yr.
- snapshots_1s at 6 h ≈ tiny; the bulk of their history lives in cold archive.

The hot DB stabilizes in the low-hundreds-of-MB range and stops growing without
bound, while the cold archive grows slowly and compresses ~10× as gzip-CSV. A
single-Mac SQLite store handles this for **years**.

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
| `SIGNALDECK_VACUUM_THRESHOLD_MB` | `2048` | DB size above which the governor VACUUMs |

Daily bars have no retention knob by design: `store.PruneBars` refuses `tf=1d`,
so the permanent record cannot be pruned by any caller, present or future.
