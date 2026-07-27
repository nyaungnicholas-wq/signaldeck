# REPRODUCE — regenerate every published number from this repository alone

The live database (`data/signaldeck.db`) is gitignored, so a checkout used to
contain the grading *code* but none of the grading *inputs* — no published
number was reproducible by anyone else. This file and the committed snapshot in
`repro/` close that gap: the minimal inputs behind every published accuracy
interval ship with the repo, content-hashed so a modified snapshot refuses to
grade.

## One command

```sh
python3 tools/accuracy_registry.py --snapshot repro
```

This regenerates the full accuracy registry — every predictor row, its live n,
accuracy, 95% CI (printed to three decimals), design effect, and verdict — from
the committed CSVs, with no database and no network. The grader first verifies
each CSV against `repro/MANIFEST.json`; a single edited cell aborts the run.

Append `--json out.json` for the full-precision rows. Against the same
snapshot, output is bit-identical to a database grade with one pinned
exception: `claimed` backtest accuracies round-trip at 10 significant digits
(`%.10g`, the repo-wide canonical float format — see
`daemon/internal/datasetver`), which cannot move any printed value or verdict.

## What is in the snapshot

| file | contents |
|---|---|
| `repro/directional_days.csv` | per-(horizon, UTC-day) tallies behind the directional-ensemble rows: n, correct, up-days, plus the high-conviction (\|p−0.5\| ≥ 0.15) slice |
| `repro/structural_days.csv` | per-(kind, horizon, call-day) resolved tallies behind the structural rows |
| `repro/structural_claims.csv` | each structural predictor's frozen backtest claim, forecast count, and first-call timestamp (drives the PENDING dates) |
| `repro/pairs_inputs.csv` | dataset **versions** (not data) of every per-symbol series the pairs study consumes: bounds, row count, SHA-256 |
| `repro/MANIFEST.json` | hash per CSV, generation time, and the git commit the snapshot was cut at |

These are day-level tallies and hashes — dozens of rows per predictor, **no
OHLCV bars** — so nothing here redistributes licensed market data (the A10
data-license guard is not implicated).

The tallies are produced by the *same* functions the database grade uses
(`fetch_directional_days` / `fetch_structural` in `tools/accuracy_registry.py`),
so the snapshot cannot drift from the SQL that defines the published numbers,
including the independence dedup (one observation per symbol, horizon, UTC-day)
and the 2026-07-24 survivorship boundary. `TestSnapshotRoundTrip` in
`tools/test_accuracy_registry.py` pins DB-grade ≡ snapshot-grade.

## Hash scheme

Every hash is the canonical scheme from `daemon/internal/datasetver`:

```
sha256( "v1|{name}|{kind}|{N}\n"  +  for each record: f1|f2|…|fk|\n )
```

with floats pinned to `%.10g`. `datasetver.HashRecords` (Go) and
`hash_records` in `tools/make_repro_snapshot.py` (Python) implement it byte for
byte; `TestHashRecordsMatchesPythonImplementation` in
`daemon/internal/datasetver/datasetver_test.go` holds a shared vector so the
two cannot drift silently.

## Pairs study (tools/pairs_trading.py)

The pairs study consumes raw daily bars, which are licensed and cannot be
committed. `repro/pairs_inputs.csv` instead versions every input series: one
row per symbol with the SHA-256 of its canonical `(ts, close, volume)`
serialization (kind `pairs-1d-close-volume`, same filter the study applies:
`close` present and > 0, ordered by ts). To reproduce the study:

1. Obtain your own licensed daily bars and load them into the schema.
2. Hash your series with `hash_records` and compare against
   `repro/pairs_inputs.csv`. Matching hashes mean you hold the identical
   dataset; only then are result differences attributable to code.
3. Run `python3 tools/pairs_trading.py` (deterministic: fixed seed 12345,
   block bootstrap included). The shipped result is embedded at
   `daemon/internal/pairsstudy/result.json`.

## Maintainers — cutting a new snapshot

```sh
python3 tools/make_repro_snapshot.py            # regenerate repro/ from the live DB
python3 tools/make_repro_snapshot.py --verify   # committed snapshot vs DB right now
```

Re-cut and commit the snapshot whenever new outcomes resolve. `--verify`
reporting a changed hash **with no new rows** means history was rewritten
inside a graded range — regrade before republishing any number, exactly as
`datasetver.Compare` treats a revised bar range.
