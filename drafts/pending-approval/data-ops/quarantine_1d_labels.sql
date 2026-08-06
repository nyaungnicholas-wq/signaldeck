-- =====================================================================
--  DO NOT EXECUTE. THIS FILE IS A DRAFT PENDING HUMAN APPROVAL.
--  BLOCKED-2 (audits/completion-2026-08-06/blocked_items.md:39).
--  Nothing in this file has been run. It writes to the live database.
-- =====================================================================
--
-- TARGET   : data/signaldeck.db  -> table prediction_outcomes, column basis_epoch
-- APPLY    : review, then paste PART A alone, record its numbers, then paste
--            PART B. Never pipe the whole file blind.
--              sqlite3 "C:/Users/Nicholas_N/Desktop/claude code/signaldeck/data/signaldeck.db"
--            (NOT ?mode=ro — this writes. Stop the daemon or accept a busy wait.)
-- ROLLBACK : rollback_quarantine_1d_labels.sql (same directory)
-- VERIFY   : verify_quarantine_1d_labels.sql   (same directory)
--
-- ---------------------------------------------------------------------
-- 1. WHAT IS BEING QUARANTINED, AND WHY THIS EXACT PREDICATE
-- ---------------------------------------------------------------------
-- Derived from the SETTLEMENT GUARD at HEAD 0b1f73b,
-- daemon/internal/pipeline/predict.go:1107-1136, inside PredictionResolver.Run:
--
--     // SETTLEMENT GUARD — the forward bar must be FINISHED.
--     ...
--     // `now < target` was the only time check, and target is the next
--     // session's bar STAMP (ET midnight). Ingest creates that bar at
--     // the open, so any resolver pass during the session found a bar
--     // whose Close was the live price, froze it as a close-to-close
--     // label, and never revisited it.
--     if _, settled, err := w.St.BarAtOrAfter(ctx, p.SymbolID, md.TF1d, fwd.Ts+1); err != nil {
--             return "", err
--     } else if !settled {
--             continue
--     }
--
-- That guard is ABSENT from running revision 0499416, so every row resolved by
-- the live daemon was frozen the moment a forward bar existed.
--
-- The guard's own test ("a strictly later bar exists") is NOT replayable after
-- the fact: `bars` records no write time, so nothing in the database says when
-- the successor bar appeared. The replayable predicate — and the one the HEAD
-- audit used (audits/2026-08-06-1d-label-disagreements.md:108, "Bucket on
-- resolved_at < fwd.ts + 16h") — is the clock form:
--
--     a resolved row is CONTAMINATED  <=>  resolved_at < <forward bar's session close>
--
-- reconstructing the resolver's own join (predict.go:1089-1104):
--     base_ts  = MAX(bars.ts) WHERE tf='1d' AND ts <= prediction_outcomes.ts
--     target   = base_ts + horizon_secs      (86400 for 1d/1d#pm, 604800 for 1w/1w#pm;
--                                             predict.go:249-254 — the '#pm' benchmark
--                                             rows run on the SAME horizon clock,
--                                             predict.go:1083-1084)
--     fwd_ts   = MIN(bars.ts) WHERE tf='1d' AND ts >= target,
--                dropped when fwd_ts - target > 3*horizon_secs (predict.go:1104)
--
-- SESSION-CLOSE CONSTANT, measured not assumed:
--   stocks -> fwd_ts + 57600.  1d stock bars are stamped at ET midnight, so
--             +16h lands exactly on the 16:00 ET close in BOTH DST regimes.
--             Verified on the backup: Jan-2026 stock 1d bars all stamp 05:00
--             UTC (EST), Jul-2026 all stamp 04:00 UTC (EDT); crypto stamps
--             00:00 UTC in both.
--             No US half-day (13:00 ET close) falls inside the affected window
--             2026-07-06 .. 2026-08-03 — checked session by session — so the
--             constant is exact for every row selected here.
--   crypto -> fwd_ts + 86400. Crypto has no 16:00 ET close; its bar day is the
--             UTC day. Using 57600 here would UNDER-count crypto by 2,896 rows.
--
-- ---------------------------------------------------------------------
-- 2. MEASURED POPULATION  (read-only, on the static backup
--    data/backups/signaldeck-20260806-131007.db, 2026-08-06)
-- ---------------------------------------------------------------------
--   TOTAL 68,368 rows across 1,048 symbols
--   prediction ts 2026-07-03 .. 2026-08-02 ; resolved_at 2026-07-04T00:52:31Z
--   .. 2026-08-03T15:39:13Z (UTC)
--
--     market  horizon    rows   symbols   of all resolved rows in that horizon
--     ------  -------  ------   -------   ------------------------------------
--     stocks  1d       52,385     1,041   34.03% of 153,931
--     stocks  1d#pm     5,666       327   15.90% of  35,634
--     stocks  1w        1,365        21    1.11% of 123,290
--     stocks  1w#pm         0         0    0     of   6,051
--     crypto  1d        5,648         7   54.30% of  10,401
--     crypto  1d#pm       350         7   11.25% of   3,110
--     crypto  1w        2,954         7   38.51% of   7,670
--     crypto  1w#pm         0         0    0     of     388
--
--   DO NOT CONFUSE THIS WITH THE "~3.7%" HEADLINE. 3.7% is the share of the 1d
--   record whose LABEL IS WRONG (37.8% frozen mid-session x ~9.7% disagreement).
--   68,368 is the number of rows FROZEN BEFORE SETTLEMENT — the ones whose label
--   is not prequentially valid whether or not it happens to be right. The
--   quarantine population is the second number, necessarily much larger, because
--   the first cannot be identified row by row (see section 5).
--
--   Cross-check that the join above reproduces the resolver: it yields exactly
--   77 stock 1d rows with no successor bar, the same 77 the HEAD audit reports
--   (audits/2026-08-06-1d-label-disagreements.md:30).
--
--   How early were they frozen (stocks, seconds before the 16:00 ET close):
--     1d    min 29   avg 5,153   max 23,041  (~09:36 ET — near the open)
--     1w    min 28   avg 7,228   max 22,596
--
-- ---------------------------------------------------------------------
-- 3. WHY THIS IS A QUARANTINE AND NOT A RELABEL  (RULES 8/9)
-- ---------------------------------------------------------------------
-- The labels are NOT exactly reconstructable, so nothing here recomputes them.
-- Proof, not assertion:
--   * daemon/internal/store/store.go:607-609 writes bars with
--         INSERT OR REPLACE INTO bars (symbol_id, tf, ts, open, high, low, close, volume)
--     and `bars` has PRIMARY KEY (symbol_id, tf, ts) with columns
--     open/high/low/close/volume only — no revision, vintage or as-of column.
--   * `bars` is the ONLY bar table in the database (sqlite_master: bars,
--     symbol_bars_epoch — and symbol_bars_epoch holds a per-symbol counter, not
--     bar history, and has 0 rows).
--   => the intraday close that was frozen into each label has been overwritten
--      and is unrecoverable, AND nothing records when any bar row was written,
--      so it cannot even be PROVEN that the close now stored is a settled close
--      rather than another stale intraday snapshot.
-- Do not "repair" these rows by recomputing sign(fwd.close/base.close - 1).
-- That substitutes today's bar vintage for the frozen one and breaks the
-- prequential property of the whole record.
--
-- ---------------------------------------------------------------------
-- 4. THE MECHANISM: basis_epoch — WHAT IT IS AND WHAT IT IS NOT
-- ---------------------------------------------------------------------
-- FACT (verified): prediction_outcomes.basis_epoch exists in the live schema,
-- is INTEGER, and is NULL on all 517,641 rows. Same for score_outcomes and
-- regime_outcomes.
-- FACT (verified): it has ZERO readers. `grep -rn basis_epoch` over all *.go,
-- *.ts, *.tsx and *.sql in the repo returns NOTHING outside research/eighty
-- scratch scripts that merely recite the column list. It is also absent from
-- daemon/internal/store/schema.sql and from the ALTER-TABLE migration list in
-- daemon/internal/store/store.go:225-331 — it was added out of band, and a
-- database rebuilt from schema.sql will NOT have it (PART A step 0 fails loudly
-- in that case, which is the correct outcome).
-- FACT: HEAD's own audit names it as the intended mechanism —
--   audits/2026-08-06-1d-label-disagreements.md:91-94: "That column exists on
--   `prediction_outcomes` and is NULL on every row; it is exactly the
--   'mark the build boundary' mechanism."
--
-- *** THEREFORE, READ THIS BEFORE APPROVING: ***
-- Stamping basis_epoch EXCLUDES NOTHING BY ITSELF. There is no reader, so every
-- published grade keeps counting these 68,368 rows until a code change adds
-- `AND basis_epoch IS NULL` to the grading reads. The sites are:
--     daemon/internal/store/directionalrecord.go:48, :64, :112
--     daemon/internal/store/trackrecord.go:48, :102, :195
--     daemon/internal/store/predict.go:165, :234, :298
--     daemon/internal/store/dashboard.go:97, :112
--     daemon/internal/store/signalreport.go:25, :47
--     daemon/internal/store/signalbt.go:69
--     daemon/internal/store/stage2.go:36, :62
--     daemon/internal/store/postmortem.go:38
--     daemon/internal/store/honestygaps.go:289
--     daemon/internal/store/stressreads.go:23
-- (predict.go:111 UnresolvedPredictions must NOT be filtered — it selects
--  resolved_at IS NULL rows, which this quarantine never touches.)
-- This SQL is half the change. Approving it without the reader change gives a
-- durable, auditable MARK and no behaviour change — which is a defensible
-- first step, but do not report it as "the record has been corrected".
--
-- A `prediction_outcome_quarantine` + `_manifest` pair (the house pattern used
-- for regime_outcomes, daemon/internal/store/regimeoutcomes.go:152,181,205) was
-- deliberately NOT proposed: prediction_outcomes already carries the spare
-- marker column, and a second table needs its own schema.sql + schemacontract.go
-- change to pass the contract check. The digest that a manifest would carry is
-- produced instead by verify_quarantine_1d_labels.sql, PART C.
--
-- ---------------------------------------------------------------------
-- 5. THE OBJECTION YOU MUST WEIGH BEFORE APPROVING
-- ---------------------------------------------------------------------
-- audits/2026-08-06-1d-label-disagreements.md:95-96 rules out
--   "NOT a `resolved_at`-selected subset. It mixes ~1,557 correctly-frozen rows
--    into the repair and cannot be defended as 'only the defect'."
-- That was written about RELABELING, and it applies with reduced but non-zero
-- force here:
--   * It does NOT apply as invention: this script writes no label. up and
--     fwd_return are untouched. Nothing is restated.
--   * It DOES apply as selection: of the ~5,643 mid-session 1d flips, roughly
--     1,557 are ordinary bar revision, not the partial-bar defect, and no query
--     can tell which row is which (ibid. :44-54). Quarantining on resolved_at
--     therefore removes some correctly-frozen rows from the graded record.
-- The case FOR doing it anyway: every selected row was labeled against a price
-- that had not happened at the instant the label was frozen. Whether or not the
-- resulting label is right, it is not prequentially valid, and dropping it is
-- conservative in a way that inventing one is not.
-- The case AGAINST: the 1d directional record is already emitting=false /
-- verdict=retired, so the payoff is small, and quarantine changes measured
-- accuracy on a criterion correlated with the flip rate.
-- BOTH published accuracy figures over 2026-07-03 .. 2026-08-02 become invalid
-- the moment the reader change lands. That consequence is the decision, and it
-- is the operator's, not this script's.
--
-- =====================================================================

.bail on

-- =====================================================================
--  PART A — READ ONLY. Safe. Run this first, on its own, and record the
--  output. Nothing below PART A executes as part of PART A.
-- =====================================================================
.headers on
.mode column

-- STEP 0 — structural preconditions. Any of these erroring means STOP.
.print '--- step 0: basis_epoch column must exist (empty result => STOP, wrong schema) ---'
SELECT name, type FROM pragma_table_info('prediction_outcomes') WHERE name = 'basis_epoch';

.print '--- step 0: basis_epoch must be 100% NULL before the run (must be 0) ---'
SELECT COUNT(*) AS already_stamped FROM prediction_outcomes WHERE basis_epoch IS NOT NULL;

-- STEP 1 — the membership set. This view is the SINGLE definition of the
-- predicate; PART B, the rollback and the verification all reuse it verbatim.
DROP VIEW IF EXISTS temp.contaminated;
CREATE TEMP VIEW contaminated AS
WITH po AS (
  SELECT p.symbol_id, p.horizon, p.ts, p.resolved_at,
         CASE WHEN p.horizon LIKE '1w%' THEN 604800 ELSE 86400 END AS hsec
  FROM prediction_outcomes p
  WHERE p.resolved_at IS NOT NULL
    AND p.basis_epoch IS NULL          -- idempotent: an already-stamped row is not re-selected
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

.print '--- step 1: THE COUNT. Expected 68368 on the 2026-08-06 backup. ---'
SELECT COUNT(*)                              AS rows_to_quarantine,
       COUNT(DISTINCT symbol_id)             AS symbols,
       date(MIN(ts), 'unixepoch')            AS first_pred_day,
       date(MAX(ts), 'unixepoch')            AS last_pred_day,
       datetime(MIN(resolved_at),'unixepoch') AS first_resolved_utc,
       datetime(MAX(resolved_at),'unixepoch') AS last_resolved_utc
FROM contaminated;

.print '--- step 1: split by market x horizon (compare against section 2 above) ---'
SELECT market, horizon, COUNT(*) AS n, COUNT(DISTINCT symbol_id) AS syms
FROM contaminated GROUP BY market, horizon ORDER BY market, horizon;

.print '--- step 1: what stays graded (the denominator that survives) ---'
SELECT p.horizon,
       COUNT(*) AS resolved_total,
       SUM(CASE WHEN c.symbol_id IS NULL THEN 1 ELSE 0 END) AS surviving
FROM prediction_outcomes p
LEFT JOIN contaminated c
       ON c.symbol_id = p.symbol_id AND c.horizon = p.horizon AND c.ts = p.ts
WHERE p.resolved_at IS NOT NULL
GROUP BY p.horizon ORDER BY p.horizon;

--  STOP HERE. Record the numbers above. If rows_to_quarantine differs from the
--  value you carry into PART B's guard, the population drifted (the live daemon
--  resolves every 10 minutes) — re-derive it, do not override the guard.

-- =====================================================================
--  PART B — WRITES. Paste separately, only after PART A and only after
--  written approval. Requires the temp view created in PART A, so run
--  both in the SAME sqlite3 session.
--
--  TAKE A BACKUP FIRST:
--    copy "…\signaldeck\data\signaldeck.db" "…\signaldeck\data\backups\pre-blocked2-<date>.db"
--  and confirm the daemon is stopped (or accept SQLITE_BUSY and retry).
-- =====================================================================

-- The epoch stamp. 1785974400 = 2026-08-06T00:00:00Z, the instant this set was
-- frozen. It is a CONSTANT, hard-coded identically in all three scripts on
-- purpose: rollback and verification must address the exact same set, and a
-- clock-read value would drift between them.
--
-- Fail-closed guard: the INSERT below raises a CHECK violation and `.bail on`
-- aborts the whole run if the population is not the size PART A measured.
-- Edit the literal 68368 ONLY to the number PART A actually printed.
CREATE TEMP TABLE quarantine_guard (n INTEGER CHECK (n = 68368));

BEGIN IMMEDIATE;

INSERT INTO quarantine_guard SELECT COUNT(*) FROM contaminated;

UPDATE prediction_outcomes
   SET basis_epoch = 1785974400
 WHERE basis_epoch IS NULL
   AND (symbol_id, horizon, ts) IN (SELECT symbol_id, horizon, ts FROM contaminated);

-- Inspect before committing. Expect 68368.
SELECT COUNT(*) AS stamped FROM prediction_outcomes WHERE basis_epoch = 1785974400;

-- Labels must be untouched — this quarantine invents nothing. Expect 0 both.
SELECT (SELECT COUNT(*) FROM prediction_outcomes WHERE basis_epoch = 1785974400 AND up IS NULL)         AS lost_up,
       (SELECT COUNT(*) FROM prediction_outcomes WHERE basis_epoch = 1785974400 AND fwd_return IS NULL) AS lost_fwd_return;

COMMIT;

-- Then run verify_quarantine_1d_labels.sql in a fresh READ-ONLY session and
-- record its PART C digest in the approval record.
