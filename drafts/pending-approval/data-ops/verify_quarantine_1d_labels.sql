-- =====================================================================
--  DO NOT EXECUTE AS PART OF ANY AUTOMATION. DRAFT PENDING HUMAN APPROVAL.
--  BLOCKED-2. Nothing in this file has been run.
--  This file is READ-ONLY — it contains no INSERT/UPDATE/DELETE — but it is
--  still gated with the rest of the BLOCKED-2 set and must not be wired into
--  CI or a scheduled task without approval.
-- =====================================================================
--
-- TARGET : data/signaldeck.db (read-only is sufficient and preferred)
-- APPLY  : sqlite3 "file:C:/Users/Nicholas_N/Desktop/claude code/signaldeck/data/signaldeck.db?mode=ro" < verify_quarantine_1d_labels.sql
-- RUN    : after quarantine_1d_labels.sql PART B has committed.
--
-- WHAT "IT TOOK EFFECT" MEANS HERE, AND WHAT IT DOES NOT
-- PART A/B below prove the MARK is present, complete, and exact. They do NOT
-- prove any published number changed — basis_epoch has no reader
-- (see quarantine_1d_labels.sql section 4). Until the grading reads carry
-- `AND basis_epoch IS NULL`, a green run of this file means "the rows are
-- marked", never "the record is corrected". Do not paraphrase it as the latter.
--
-- =====================================================================

.bail on
.headers on
.mode column

-- The predicate, re-derived INDEPENDENTLY of the stamp. Note the deliberate
-- difference from the quarantine script's view: there is no
-- `AND basis_epoch IS NULL` clause here. That clause exists in the quarantine
-- script only to make its UPDATE idempotent; including it here would make this
-- view empty after the stamp lands and every check below vacuously pass.
DROP VIEW IF EXISTS temp.contaminated;
CREATE TEMP VIEW contaminated AS
WITH po AS (
  SELECT p.symbol_id, p.horizon, p.ts, p.resolved_at,
         CASE WHEN p.horizon LIKE '1w%' THEN 604800 ELSE 86400 END AS hsec
  FROM prediction_outcomes p
  WHERE p.resolved_at IS NOT NULL
),
b AS (
  SELECT po.*,
         (SELECT MAX(x.ts) FROM bars x
           WHERE x.symbol_id = po.symbol_id AND x.tf = '1d' AND x.ts <= po.ts) AS base_ts
  FROM po
),
f AS (
  SELECT b.*,
         b.base_ts + b.hsec AS target,
         (SELECT MIN(x.ts) FROM bars x
           WHERE x.symbol_id = b.symbol_id AND x.tf = '1d' AND x.ts >= b.base_ts + b.hsec) AS fwd_ts
  FROM b
  WHERE b.base_ts IS NOT NULL
)
SELECT f.symbol_id, f.horizon, f.ts, f.resolved_at, s.market,
       f.base_ts, f.target, f.fwd_ts
FROM f JOIN symbols s ON s.id = f.symbol_id
WHERE f.fwd_ts IS NOT NULL
  AND (f.fwd_ts - f.target) <= 3 * f.hsec
  AND f.resolved_at < f.fwd_ts + (CASE WHEN s.market = 'crypto' THEN 86400 ELSE 57600 END);


-- =====================================================================
--  PART A — the mark is present and nothing else moved
-- =====================================================================
.print '=== A1. stamped rows by epoch (expect one row: 1785974400 | 68368) ==='
SELECT basis_epoch, COUNT(*) AS rows,
       COUNT(DISTINCT symbol_id) AS symbols,
       date(MIN(ts),'unixepoch') AS first_pred_day,
       date(MAX(ts),'unixepoch') AS last_pred_day
FROM prediction_outcomes
WHERE basis_epoch IS NOT NULL
GROUP BY basis_epoch;

.print '=== A2. stamped split by market x horizon ==='
.print '    expect stocks 1d 52385 | 1d#pm 5666 | 1w 1365   crypto 1d 5648 | 1d#pm 350 | 1w 2954'
SELECT s.market, p.horizon, COUNT(*) AS n
FROM prediction_outcomes p JOIN symbols s ON s.id = p.symbol_id
WHERE p.basis_epoch = 1785974400
GROUP BY s.market, p.horizon ORDER BY s.market, p.horizon;

.print '=== A3. NOTHING was invented: labels intact on every stamped row (expect 0,0) ==='
SELECT SUM(up IS NULL)         AS stamped_missing_up,
       SUM(fwd_return IS NULL) AS stamped_missing_fwd_return
FROM prediction_outcomes WHERE basis_epoch = 1785974400;

.print '=== A4. no UNRESOLVED row was touched (expect 0) ==='
SELECT COUNT(*) AS unresolved_but_stamped
FROM prediction_outcomes WHERE basis_epoch IS NOT NULL AND resolved_at IS NULL;

.print '=== A5. no OTHER table was stamped by this operation (expect 0, 0) ==='
SELECT (SELECT COUNT(*) FROM score_outcomes  WHERE basis_epoch IS NOT NULL) AS score_outcomes_stamped,
       (SELECT COUNT(*) FROM regime_outcomes WHERE basis_epoch IS NOT NULL) AS regime_outcomes_stamped;


-- =====================================================================
--  PART B — SET EQUALITY. This is the actual proof of correctness:
--  the stamped set and the independently re-derived predicate set must be
--  identical in BOTH directions. Both counts must be 0.
--
--  B2 non-zero is EXPECTED on the live database if any time passed between
--  the quarantine and this run: running revision 0499416 has no settlement
--  guard, so it keeps minting fresh contaminated rows every 10 minutes. A
--  non-zero B2 is therefore a measure of ONGOING contamination, not a failed
--  quarantine — inspect B2's date range before treating it as a failure.
--  B1 non-zero is ALWAYS a failure: it means a row was stamped that the
--  predicate does not select.
-- =====================================================================
.print '=== B1. stamped but NOT contaminated (must be 0 — hard failure otherwise) ==='
SELECT COUNT(*) AS stamped_not_contaminated FROM (
  SELECT symbol_id, horizon, ts FROM prediction_outcomes WHERE basis_epoch = 1785974400
  EXCEPT
  SELECT symbol_id, horizon, ts FROM contaminated
);

.print '=== B2. contaminated but NOT stamped (0 at freeze time; grows while 0499416 runs) ==='
SELECT COUNT(*) AS contaminated_not_stamped FROM (
  SELECT symbol_id, horizon, ts FROM contaminated
  EXCEPT
  SELECT symbol_id, horizon, ts FROM prediction_outcomes WHERE basis_epoch = 1785974400
);

.print '=== B2 detail: if non-zero, when were the misses resolved? ==='
.print '    resolved AFTER the quarantine  => ongoing contamination, deploy the guard'
.print '    resolved BEFORE the quarantine => the quarantine MISSED rows, investigate'
SELECT datetime(MIN(c.resolved_at),'unixepoch') AS earliest_missed_resolve_utc,
       datetime(MAX(c.resolved_at),'unixepoch') AS latest_missed_resolve_utc,
       SUM(c.resolved_at < 1785974400)          AS missed_and_predates_epoch,
       COUNT(*)                                  AS missed_total
FROM contaminated c
LEFT JOIN prediction_outcomes p
       ON p.symbol_id = c.symbol_id AND p.horizon = c.horizon AND p.ts = c.ts
      AND p.basis_epoch = 1785974400
WHERE p.symbol_id IS NULL;


-- =====================================================================
--  PART C — MEMBERSHIP DIGEST. The manifest substitute: it pins the exact
--  set so a later reviewer can prove it did not drift. Record the pair
--  (n, digest) in the approval record next to the operator's name.
--
--  REFERENCE VALUE, measured read-only on the static backup
--  data/backups/signaldeck-20260806-131007.db on 2026-08-06:
--      n      = 68368
--      digest = dad0f25252e95c31a94d8d86f012cdf1d4a39363b5ab85be7e94174d1254eea4
--
--  The digest computed against the LIVE database WILL differ if any row was
--  resolved between 2026-08-06T13:10Z and the quarantine — that is expected,
--  not a defect. Equality with the reference proves the live set is exactly
--  the frozen set; inequality means the population moved and PART A's count
--  is the number that governs.
-- =====================================================================
.mode list
.headers off
.print '=== C. digest of the STAMPED set (prints: <n> <digest>) ==='
SELECT COUNT(*) || ' ' ||
       lower(hex(sha3(group_concat(symbol_id || '|' || horizon || '|' || ts, char(10)
                                   ORDER BY symbol_id, horizon, ts), 256)))
FROM prediction_outcomes WHERE basis_epoch = 1785974400;

.print '=== C. digest of the RE-DERIVED predicate set (equal to the above at freeze time) ==='
SELECT COUNT(*) || ' ' ||
       lower(hex(sha3(group_concat(symbol_id || '|' || horizon || '|' || ts, char(10)
                                   ORDER BY symbol_id, horizon, ts), 256)))
FROM contaminated;


-- =====================================================================
--  PART D — the honest end state. Reported, never silent.
-- =====================================================================
.mode column
.headers on
.print '=== D. graded population before and after, per horizon ==='
SELECT horizon,
       COUNT(*)                              AS resolved_total,
       SUM(basis_epoch IS NOT NULL)          AS quarantined,
       SUM(basis_epoch IS NULL)              AS still_graded,
       ROUND(100.0 * SUM(basis_epoch IS NOT NULL) / COUNT(*), 2) AS pct_removed
FROM prediction_outcomes
WHERE resolved_at IS NOT NULL
GROUP BY horizon ORDER BY horizon;

.print '=== D. reminder: a green run above proves the MARK, not a corrected record. ==='
.print '=== The grading reads listed in quarantine_1d_labels.sql section 4 still  ==='
.print '=== count every quarantined row until they filter on basis_epoch.         ==='
