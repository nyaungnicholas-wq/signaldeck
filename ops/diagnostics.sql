-- SignalDeck publication diagnostics — 2026-08-04.
--
-- Run with Python, not the sqlite3 CLI: sqlite3 is not installed on the Windows
-- box and the repo reads SQLite through Python everywhere for that reason.
--
--   python -c "import sqlite3,sys; c=sqlite3.connect('file:data/signaldeck.db?mode=ro',uri=True); \
--     [print(r) for r in c.execute(open('ops/diagnostics.sql').read().split(';')[N])]"
--
-- Or open the DB read-only and paste one query at a time.
--
-- NOTE ON THE DRAFT THESE REPLACE. The fix-pack draft queried
-- accuracy_registry_rows, structural_forecasts, and evidence_claims.predictor /
-- .horizon. None of those exist here: the registry is a JSON FILE
-- (data/accuracy_registry.json), structural forecasts live in regime_outcomes,
-- and evidence_claims is keyed by a composite id ('directional-ensemble-1d')
-- with its scope in scope_json. These are the working equivalents.


-- 1. WAL / journal state.
-- The daemon sets journal_size_limit=64MB in its DSN (store.Open), so a WAL far
-- above that means checkpoints are being STARVED by long-lived readers, not
-- that the bound is missing.
--
-- CAVEAT, measured 2026-08-04: journal_size_limit and synchronous are
-- PER-CONNECTION. A read-only diagnostic connection that does not repeat the
-- daemon's DSN pragmas reports the defaults (-1 and 2) no matter what the
-- daemon set. Reading -1 here is therefore NOT evidence the bound is missing;
-- check the DSN in store.Open, or the WAL file size on disk, instead.
PRAGMA journal_mode;
PRAGMA journal_size_limit;
PRAGMA busy_timeout;
PRAGMA synchronous;
-- Requires a WRITABLE connection: on a mode=ro handle this fails with
-- "disk I/O error", which is the read-only refusal, not a corrupt database.
PRAGMA wal_checkpoint(PASSIVE);


-- 2. THE CONTRADICTION CHECK: a model the evidence store has condemned that the
-- publication layer is not reporting as retired.
-- This is the 2026-08-03 defect. It must return ZERO rows.
--
-- "no such table: publication_verdicts" means the daemon has not restarted
-- since 2026-08-04 and the new schema has not been applied yet. That is a
-- not-yet-deployed answer, not a passing one.
SELECT c.id           AS claim,
       c.status       AS evidence_status,
       v.predictor,
       v.horizon,
       v.publication_status,
       v.retired      AS publication_retired,
       v.evaluated_at
  FROM evidence_claims c
  LEFT JOIN publication_verdicts v
         ON v.evaluated_at = (SELECT MAX(p.evaluated_at)
                                FROM publication_verdicts p
                               WHERE p.predictor = v.predictor
                                 AND p.horizon   = v.horizon
                                 AND p.variant   = v.variant)
        AND c.id = v.predictor || '-' || v.horizon
 WHERE c.status IN ('refuted', 'retired')
   AND (v.retired IS NULL OR v.retired = 0);


-- 3. Sticky-retirement integrity: any row whose history contains a retirement
-- but whose CURRENT verdict does not. The BEFORE INSERT trigger makes this
-- impossible, so a non-empty result means the trigger was dropped or history
-- was deleted. Must return ZERO rows.
SELECT h.predictor, h.horizon, h.variant,
       MIN(h.evaluated_at) AS first_retired_at,
       cur.publication_status, cur.retired AS current_retired
  FROM publication_verdicts h
  JOIN publication_verdicts cur
    ON cur.predictor = h.predictor AND cur.horizon = h.horizon
   AND cur.variant  = h.variant
   AND cur.evaluated_at = (SELECT MAX(p.evaluated_at) FROM publication_verdicts p
                            WHERE p.predictor = h.predictor AND p.horizon = h.horizon
                              AND p.variant = h.variant)
 WHERE h.retired = 1 AND cur.retired = 0
 GROUP BY h.predictor, h.horizon, h.variant;


-- 4. Grader liveness. /api/accuracy refuses to publish when the newest row here
-- is failed, missing, or older than GraderMaxAge (26h).
SELECT task, success, finished_at, rows_evaluated,
       COALESCE(error, '') AS error
  FROM grader_heartbeats
 ORDER BY finished_at DESC
 LIMIT 10;


-- 5. Survivorship: inactive symbols with no delisting date — the backfill
-- worklist. Each is a name the point-in-time universe cannot reconstruct.
--
-- Measured 2026-08-04: 735 rows. Do NOT compare that to the registry's "8
-- inactive symbol(s) with no delisted_at": the registry counts only symbols
-- CONTRIBUTING RESOLVED POST-EPOCH ROWS to the current grade (327/335 = 97.6%
-- coverage). Most of these 735 never enter a graded denominator. The two
-- numbers measure different populations and both are correct.
SELECT symbol, market, active, added_at
  FROM symbols
 WHERE active = 0 AND delisted_at IS NULL
 ORDER BY symbol;


-- 6. EARLIEST GRADEABLE DATE, derived — never hard-coded.
-- The pre-registered commitment is 2026-08-07 and is frozen in the hash chain;
-- this is what the outstanding calls actually permit. They differed by ten days
-- on 2026-08-04. The 1.45 factor converts trading days to calendar days and
-- matches tools/structural_liveness.py and store.EarliestGradeableByKind.
SELECT kind,
       COUNT(*)                                                    AS total,
       SUM(CASE WHEN resolved_at IS NOT NULL THEN 1 ELSE 0 END)    AS resolved,
       DATE(MIN(CASE WHEN resolved_at IS NULL
                     THEN ts + CAST(horizon_days * 1.45 * 86400 AS INTEGER) END),
            'unixepoch')                                           AS earliest_gradeable_at
  FROM regime_outcomes
 GROUP BY kind
 ORDER BY earliest_gradeable_at;


-- 7. Quarantined rows must never be graded or counted in a denominator.
-- naive_label IS NULL is the quarantine marker; any such row carrying a
-- correct/actual value means something graded what it was forbidden to grade.
-- Must return ZERO rows.
SELECT kind, COUNT(*) AS graded_quarantined
  FROM regime_outcomes
 WHERE naive_label IS NULL AND (correct IS NOT NULL OR actual IS NOT NULL)
 GROUP BY kind;
