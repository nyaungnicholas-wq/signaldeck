-- =====================================================================
--  DO NOT EXECUTE. THIS FILE IS A DRAFT PENDING HUMAN APPROVAL.
--  BLOCKED-2. Nothing in this file has been run. It writes to the live database.
-- =====================================================================
--
-- TARGET  : data/signaldeck.db -> prediction_outcomes.basis_epoch
-- UNDOES  : quarantine_1d_labels.sql PART B
-- APPLY   : sqlite3 "C:/…/signaldeck/data/signaldeck.db"  (NOT ?mode=ro)
--           Run PART A alone first, then PART B.
--
-- WHY THIS IS A COMPLETE ROLLBACK
-- The quarantine writes exactly one column on 68,368 rows, and that column was
-- NULL on every row in the table beforehand (asserted by
-- quarantine_1d_labels.sql PART A step 0, which refuses to proceed otherwise).
-- Setting it back to NULL restores the table byte-for-byte in value terms.
-- up, fwd_return, prob, resolved_at were never written, so there is nothing
-- else to restore and no data was destroyed at any point.
--
-- IF PART A BELOW REPORTS A NON-ZERO other_epochs, STOP.
-- It means someone stamped basis_epoch with a different value after the
-- quarantine. This script only clears epoch 1785974400 and will leave that
-- other set alone — correct, but you need to know it exists before you decide.
--
-- =====================================================================

.bail on
.headers on
.mode column

-- =====================================================================
--  PART A — READ ONLY. Establishes what is about to be cleared.
-- =====================================================================
.print '--- what is stamped right now ---'
SELECT COALESCE(CAST(basis_epoch AS TEXT), 'NULL') AS epoch,
       COUNT(*) AS rows
FROM prediction_outcomes
GROUP BY basis_epoch
ORDER BY basis_epoch;

.print '--- rows this rollback will clear (expect 68368) ---'
SELECT COUNT(*) AS will_clear FROM prediction_outcomes WHERE basis_epoch = 1785974400;

.print '--- rows stamped with some OTHER epoch (expect 0; non-zero => STOP) ---'
SELECT COUNT(*) AS other_epochs
FROM prediction_outcomes
WHERE basis_epoch IS NOT NULL AND basis_epoch <> 1785974400;

.print '--- labels must still be intact before rolling back (expect 0, 0) ---'
SELECT (SELECT COUNT(*) FROM prediction_outcomes WHERE basis_epoch = 1785974400 AND up IS NULL)         AS lost_up,
       (SELECT COUNT(*) FROM prediction_outcomes WHERE basis_epoch = 1785974400 AND fwd_return IS NULL) AS lost_fwd_return;

--  STOP HERE. Record the numbers. Continue only on written approval.

-- =====================================================================
--  PART B — WRITES. Paste separately.
--  If the reader change ('AND basis_epoch IS NULL' in the grading reads,
--  listed in quarantine_1d_labels.sql section 4) has already shipped, this
--  rollback silently re-admits 68,368 contaminated rows to the published
--  record. Revert the code first, or accept that consequence knowingly.
-- =====================================================================

BEGIN IMMEDIATE;

UPDATE prediction_outcomes
   SET basis_epoch = NULL
 WHERE basis_epoch = 1785974400;

-- Expect 0 remaining at this epoch, and 0 stamped at any epoch if the
-- quarantine was the only thing that ever wrote this column.
SELECT (SELECT COUNT(*) FROM prediction_outcomes WHERE basis_epoch = 1785974400)  AS still_at_epoch,
       (SELECT COUNT(*) FROM prediction_outcomes WHERE basis_epoch IS NOT NULL)   AS still_stamped_any;

COMMIT;
