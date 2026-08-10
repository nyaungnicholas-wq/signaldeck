-- ===========================================================================
-- BLOCKED-6 :: prune_universe.sql
--
-- ***  DRAFT ONLY. DO NOT EXECUTE. REQUIRES EXPLICIT HUMAN APPROVAL.  ***
--
-- Nothing in this file has been run. It was written by an audit agent working
-- under a read-only invariant on data/signaldeck.db; every number quoted below
-- came from SELECTs against `?mode=ro` handles or the static 13:10 backup.
--
-- TARGET   : data/signaldeck.db  (the LIVE database)
-- PURPOSE  : restore active=0 for the symbols outside the intended universe,
--            after the 'SignalDeck Daily-Refresh' sweep was terminated mid-run
--            on 2026-08-06 13:15 (LastTaskResult 3221225786 = 0xC000013A,
--            STATUS_CONTROL_C_EXIT) between its reactivate and its prune.
-- STATE    : SELECT COUNT(*), SUM(active) FROM symbols  ->  2950 / 2950.
--            Live impact: dq-auditor "checked 2950 active symbols",
--            downsampler "rolled up 2950 symbols".
--
-- ---------------------------------------------------------------------------
-- READ SECTION 4 OF THE ACCOMPANYING REPORT FIRST. Re-running the sweep to
-- completion (`ops/signaldeck-ctl.sh refresh`) is the SAFER remedy and is the
-- recommended one. This file exists for the case where the sweep cannot be
-- re-run -- and it carries a real risk the sweep does not: it prunes on
-- liquidity computed from bars that were never refreshed today.
-- ---------------------------------------------------------------------------
--
-- PRECONDITION -- NOT OPTIONAL:
--   The daemon must not be writing while this runs. ops/signaldeck-refresh.sh
--   stops signaldeckd before its own prune and says why at lines 78-85: "the
--   daemon holds the single SQLite writer while backfilling; pruning under that
--   lock stalls -- which is exactly how a past sweep got stuck." Stopping the
--   daemon is itself a change of running state and needs its own approval.
--   PRAGMA busy_timeout below bounds the wait; it does not remove the need.
--
-- HOW TO RUN (only after approval, and only with the daemon quiesced):
--   cd "C:/Users/Nicholas_N/Desktop/claude code/signaldeck"
--   sqlite3 data/signaldeck.db < drafts/pending-approval/data-ops/prune_universe.sql
--   As written the transaction ENDS IN ROLLBACK: it prints the verification
--   numbers and changes nothing. Read them, and only then swap the final
--   ROLLBACK for COMMIT and run it again.
--
-- WHERE THE "INTENDED SET" COMES FROM -- nothing here is hardcoded to 328.
--   The criterion is copied verbatim from ops/signaldeck-refresh.sh:89-92, so
--   this file narrows to exactly the set a completed sweep would have left:
--
--     WITH broad AS (SELECT id FROM symbols WHERE active=1 AND market='stocks'
--                      AND id NOT IN (SELECT symbol_id FROM user_symbols)),
--        liq AS (SELECT b.id, AVG(bar.close*bar.volume) dv FROM broad b
--                  JOIN bars bar ON bar.symbol_id=b.id AND bar.tf='1d'
--                   AND bar.ts>=$CUT GROUP BY b.id),
--        keep AS (SELECT id FROM liq ORDER BY dv DESC, id ASC LIMIT $KEEP)
--        UPDATE symbols SET active=0
--         WHERE id IN (SELECT id FROM broad WHERE id NOT IN (SELECT id FROM keep));
--
--   Read as three rules, the survivors are:
--     (a) every symbol with market <> 'stocks'  -- `broad` is stocks-only, so
--         the 7 active crypto symbols are never touched;
--     (b) every symbol in user_symbols          -- explicitly excluded from
--         `broad`; a user's watchlist is never pruned;
--     (c) the top $KEEP stocks by mean daily dollar volume (close*volume) over
--         the trailing 60 days, ties broken by lowest id.
--   Symbols in `broad` with NO 1d bars in the window fall out of `liq` and are
--   therefore deactivated -- absence of data is treated as "not liquid".
--
--   $CUT  = now - 60*86400, written below as strftime('%s','now') - 60*86400,
--           which is the same UTC epoch the script's `date +%s` produces.
--   $KEEP = 150. This is SIGNALDECK_SWEEP_KEEP, and it is NOT set in the
--           'SignalDeck Daily-Refresh' task (verified: schtasks /Query /XML has
--           no environment override), so the script's default of 150 is what a
--           real sweep uses. If that env var is ever set, change it here too.
--
-- EXPECTED OUTCOME (COMPUTED 2026-08-06 by running the criterion as a pure
-- SELECT against a ?mode=ro handle on the live DB -- no writes):
--     active_now 2950 | broad 2772 | liq 916 | keep 150
--     would_deactivate 2622 -> active_after 328
--   328 = 150 keep + 171 active user_symbols stocks + 7 active crypto.
--   The pre-incident 13:10 backup holds 329 active; the extra symbol is one the
--   daemon re-activated singly after the 08-05 prune (store.go:591 does a
--   per-id `UPDATE symbols SET active=? WHERE id=?`). A one-symbol difference
--   from the backup is expected and is not an error.
--
-- DRY-RUN VERIFIED, but NOT against the live DB. This exact file was executed
-- against a slim throwaway copy of the 13:10 backup in the scratch directory,
-- re-damaged to match production with `UPDATE symbols SET active=1 WHERE
-- market='stocks'` (2950/2950). Observed:
--   BEFORE  total 2950 | active 2950 | stocks 2943 | non_stocks 7
--           user 172 | liq_rows 913 | would_deactivate 2622
--   AFTER   active_total 328 | active_stocks 321 | non_stocks 7 | keep_only 150
--           all four assertions OK
--   ROLLBACK as shipped left the fixture at 2950 -- the dry run is genuinely dry.
--   The COMMIT variant landed on 328, and the documented inverse below took it
--   back to 2950.
-- liq_rows reads 913 on the backup and 916 on the live DB: the interrupted
-- sweep did pull some bars for the widened universe before it died. That moves
-- a handful of symbols across the top-150 boundary. It does not change the
-- count, and the resulting set is still exactly what a completed sweep gives.
--
-- REVERSING THIS. The pre-change state is trivially exact, which is unusual and
-- worth stating plainly: right now EVERY stock is active=1, so the complete
-- inverse of this file is one statement --
--     UPDATE symbols SET active=1 WHERE market='stocks';
-- No snapshot table is needed. That is only true while the universe is still
-- fully open; once a prune commits, the prior set is gone.
-- ===========================================================================

PRAGMA busy_timeout = 120000;

-- ---------------------------------------------------------------------------
-- SECTION A -- BEFORE. Read-only. Nothing here modifies the database.
--   Sanity gate: if `liq_rows` is smaller than KEEP (150), the bars needed to
--   rank liquidity are missing and the UPDATE below would deactivate far more
--   than intended. STOP if that happens -- the result would be too LEAN, which
--   is safe but wrong, and re-running the sweep is then the only correct fix.
-- ---------------------------------------------------------------------------
SELECT '--- BEFORE ---' AS label;
SELECT 'total_symbols'            AS metric, COUNT(*)   AS value FROM symbols
UNION ALL SELECT 'active_total',            SUM(active)          FROM symbols
UNION ALL SELECT 'active_stocks',           COUNT(*) FROM symbols WHERE active=1 AND market='stocks'
UNION ALL SELECT 'active_non_stocks',       COUNT(*) FROM symbols WHERE active=1 AND market<>'stocks'
UNION ALL SELECT 'active_user_symbols',     COUNT(*) FROM symbols
                                             WHERE active=1 AND id IN (SELECT symbol_id FROM user_symbols)
UNION ALL SELECT 'liq_rows (must be >=150)',
       (SELECT COUNT(*) FROM (
          SELECT b.id FROM symbols b
            JOIN bars bar ON bar.symbol_id=b.id AND bar.tf='1d'
                         AND bar.ts >= strftime('%s','now') - 60*86400
           WHERE b.active=1 AND b.market='stocks'
             AND b.id NOT IN (SELECT symbol_id FROM user_symbols)
           GROUP BY b.id))
UNION ALL SELECT 'would_deactivate (predicted)',
       (SELECT COUNT(*) FROM (
          WITH broad AS (SELECT id FROM symbols WHERE active=1 AND market='stocks'
                           AND id NOT IN (SELECT symbol_id FROM user_symbols)),
               liq   AS (SELECT b.id, AVG(bar.close*bar.volume) dv FROM broad b
                           JOIN bars bar ON bar.symbol_id=b.id AND bar.tf='1d'
                                        AND bar.ts >= strftime('%s','now') - 60*86400
                          GROUP BY b.id),
               keep  AS (SELECT id FROM liq ORDER BY dv DESC, id ASC LIMIT 150)
          SELECT id FROM broad WHERE id NOT IN (SELECT id FROM keep)));

-- ---------------------------------------------------------------------------
-- SECTION B -- THE CHANGE, inside one transaction.
--   BEGIN IMMEDIATE takes the write lock up front rather than discovering a
--   conflict at COMMIT, so a busy database fails here, before any work.
-- ---------------------------------------------------------------------------
BEGIN IMMEDIATE;

WITH broad AS (SELECT id FROM symbols WHERE active=1 AND market='stocks'
                 AND id NOT IN (SELECT symbol_id FROM user_symbols)),
     liq   AS (SELECT b.id, AVG(bar.close*bar.volume) dv FROM broad b
                 JOIN bars bar ON bar.symbol_id=b.id AND bar.tf='1d'
                              AND bar.ts >= strftime('%s','now') - 60*86400
                GROUP BY b.id),
     keep  AS (SELECT id FROM liq ORDER BY dv DESC, id ASC LIMIT 150)
UPDATE symbols SET active=0
 WHERE id IN (SELECT id FROM broad WHERE id NOT IN (SELECT id FROM keep));

-- ---------------------------------------------------------------------------
-- VERIFICATION -- still inside the transaction, so these numbers describe the
-- state that WOULD be committed. All four assertions must read 'OK'.
-- ---------------------------------------------------------------------------
SELECT '--- AFTER (uncommitted) ---' AS label;
SELECT 'active_total'        AS metric, SUM(active) AS value FROM symbols
UNION ALL SELECT 'active_stocks',     COUNT(*) FROM symbols WHERE active=1 AND market='stocks'
UNION ALL SELECT 'active_non_stocks', COUNT(*) FROM symbols WHERE active=1 AND market<>'stocks'
UNION ALL SELECT 'active_keep_only',  COUNT(*) FROM symbols
                                       WHERE active=1 AND market='stocks'
                                         AND id NOT IN (SELECT symbol_id FROM user_symbols);

SELECT 'assert_no_user_symbol_deactivated' AS check_name,
       CASE WHEN (SELECT COUNT(*) FROM user_symbols u JOIN symbols s ON s.id=u.symbol_id
                   WHERE s.active=0) = 0 THEN 'OK' ELSE 'FAIL' END AS result
UNION ALL
SELECT 'assert_no_crypto_deactivated',
       CASE WHEN (SELECT COUNT(*) FROM symbols WHERE market<>'stocks' AND active=0) = 0
            THEN 'OK' ELSE 'FAIL' END
UNION ALL
SELECT 'assert_keep_count_equals_150',
       CASE WHEN (SELECT COUNT(*) FROM symbols WHERE active=1 AND market='stocks'
                    AND id NOT IN (SELECT symbol_id FROM user_symbols)) = 150
            THEN 'OK' ELSE 'FAIL' END
UNION ALL
SELECT 'assert_total_active_in_expected_band',
       CASE WHEN (SELECT SUM(active) FROM symbols) BETWEEN 300 AND 400
            THEN 'OK' ELSE 'FAIL' END;

-- ---------------------------------------------------------------------------
-- SECTION C -- OUTCOME. As shipped this DISCARDS the change.
--
--   Leave ROLLBACK in place for the first run. Read Section B's output. Every
--   assertion must say OK and active_total must be ~328. Only then edit this
--   file: comment out ROLLBACK, uncomment COMMIT, and run it a second time.
--   Two runs is the point -- the first is a dry run that costs nothing.
-- ---------------------------------------------------------------------------
ROLLBACK;
-- COMMIT;

-- ---------------------------------------------------------------------------
-- SECTION D -- POST-COMMIT VERIFICATION. Run separately, after committing.
--   sqlite3 "file:data/signaldeck.db?mode=ro" "SELECT COUNT(*), SUM(active) FROM symbols;"
--   Expect: 2950|328  (not 2950|2950).
--
--   Then confirm the live surfaces followed, which is the actual goal -- the
--   row count is only a proxy. The next dq-auditor and downsampler runs should
--   report ~328, not 2950:
--     sqlite3 "file:data/signaldeck.db?mode=ro" \
--       "SELECT worker, started_at, status FROM worker_runs
--         WHERE worker IN ('dq-auditor','downsampler')
--         ORDER BY started_at DESC LIMIT 6;"
--   and check logs for the "checked N active symbols" / "rolled up N symbols"
--   lines. If those still say 2950 after a fresh worker pass, the universe was
--   re-widened by something else and this file did not address the cause.
--
--   NOTE ON THE UNDERLYING DEFECT. This file repairs damage; it does not stop
--   it recurring. The next interruption reproduces it exactly. The structural
--   fix is drafts/patches/refresh-atomicity.patch, which makes "universe wide"
--   a state the DB records and a 5-minute tick repairs. Applying this file
--   without that patch buys one day.
-- ---------------------------------------------------------------------------
