# `quarantine/` is a CODE holding pen, not a data quarantine

Read this before assuming the platform quarantines bad records here. It does
not, and the directory name is the whole reason that assumption gets made — an
audit on 2026-08-12 flagged exactly this.

## What is actually in here

Source files, not data. `incomplete-2026-07-27/` holds Go files the score-loop
workflow wrote that never compiled; its own README explains that batch. They sit
outside the module so a broken draft cannot break the build, and they are kept
rather than deleted because the defects they describe are still real.

The only other writer of this path is
`ops/signaldeck-backup-offline.sh`, which parks retired backup artifacts under
`quarantine/backups-<date>`.

**No prediction, bar, forecast or outcome row is ever routed here, and nothing
alerts when this directory fills.**

## Where data quarantine actually lives

Three separate mechanisms, none of them this folder:

| Mechanism | Where | Scope |
|---|---|---|
| `regime_outcome_quarantine` (+ `_manifest`) | SQLite tables | the only real quarantine TABLE; structural regime outcomes |
| Settlement quarantine | a query-time predicate in `tools/accuracy_registry.py` (`SETTLEMENT_PREDICATE`) | excludes outcomes graded against an unfinished forward bar |
| Stale-feed exclusion | a query-time CTE in the same file (`STALE_FEED_CTE`) | excludes observations minted on a day the symbol's own feed was flagged stale |

The last two are **filters, not quarantines**: they exclude rows at grading time
rather than moving or marking them, so the excluded rows stay in
`prediction_outcomes` looking exactly like included ones. Their sizes are
measured and published in `data/accuracy_registry.json` under
`settlement_quarantine` and `stale_feed_exclusion`, which is the only place the
exclusion is visible.

`prediction_outcomes` has **no quarantine table at all**. A bad directional row
is excluded by predicate or not at all.

## Why that matters

Because those predicates live in ONE Python file, they protect the published
registry and nothing else. Every Go surface that grades directional outcomes
reads the same rows without them — which is the divergence
`daemon/internal/store/gradeablepop.go` was written to close on the Go side.
Anything new that grades outcomes must go through that selector, or it inherits
the unfiltered population by default.
