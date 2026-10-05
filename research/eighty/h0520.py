# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 519
# cycle_index: 49
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math

db_path = 'file:data/signaldeck.db?mode=ro'
conn = sqlite3.connect(db_path, uri=True)
cur = conn.cursor()

# Check if we have the necessary data: daily bars from 2018-07 onward, sentiment_features with day, and symbols.
# We'll compute everything in one CTE chain for efficiency, but must respect as-of.
# We'll use window functions to compute daily returns and 30-day average volume.

# First, get the cutoff for the 80/20 split. We'll find the 80th percentile of ts for daily bars with sentiment.
# We'll compute on the fly.

query = """
WITH universe AS (
    SELECT DISTINCT b.symbol_id
    FROM bars b
    INNER JOIN sentiment_features s ON b.symbol_id = s.symbol_id AND date(b.ts, 'unixepoch', 'utc') = s.day
    WHERE b.tf = '1d' AND b.ts >= 1532611200  -- 2018-07-26 in unix epoch (UTC)
),
daily_data AS (
    SELECT
        b.symbol_id,
        b.ts,
        b.close,
        b.volume,
        s.mean_score,
        LAG(b.close) OVER (PARTITION BY b.symbol_id ORDER BY b.ts) as prev_close,
        AVG(b.volume) OVER (PARTITION BY b.symbol_id ORDER BY b.ts ROWS BETWEEN 30 PRECEDING AND 1 PRECEDING) as avg_vol_30
    FROM bars b
    INNER JOIN sentiment_features s ON b.symbol_id = s.symbol_id AND date(b.ts, 'unixepoch', 'utc') = s.day
    WHERE b.tf = '1d' AND b.ts >= 1532611200
),
opportunities AS (
    SELECT
        symbol_id,
        ts,
        close,
        volume,
        mean_score,
        prev_close,
        avg_vol_30,
        (close - prev_close) / prev_close as daily_return
    FROM daily_data
    WHERE prev_close IS NOT NULL  -- need previous close for return
      AND avg_vol_30 IS NOT NULL  -- need 30-day average volume
      AND mean_score IS NOT NULL  -- need sentiment
),
signals AS (
    SELECT
        symbol_id,
        ts,
        close,
        daily_return,
        mean_score,
        LEAD(close, 5) OVER (PARTITION BY symbol_id ORDER BY ts) as close_5d,
        1 as issued
    FROM opportunities
    WHERE daily_return <= -0.03
      AND volume >= 2 * avg_vol_30
      AND mean_score > 0.2
),
metrics AS (
    SELECT
        ts,
        symbol_id,
        close,
        close_5d,
        issued,
        CASE WHEN close_5d IS NOT NULL THEN (close_5d - close) / close ELSE NULL END as fwd_return_5d,
        CASE WHEN close_5d IS NOT NULL AND (close_5d - close) / close > 0 THEN 1 ELSE 0 END as up
    FROM signals
    WHERE close_5d IS NOT NULL  -- ensure we have 5-day forward return
),
-- Count total opportunities for the entire period (evaluation + sealed)
total_opportunities AS (
    SELECT COUNT(*) as total_opp
    FROM opportunities
),
-- Compute 80th percentile of ts for splitting
percentile AS (
    SELECT ts as cutoff_ts
    FROM opportunities
    ORDER BY ts
    LIMIT 1
    OFFSET (SELECT CAST(total_opp * 0.8 AS INTEGER) FROM total_opportunities)
),
-- Split into evaluation and sealed era
evaluation_metrics AS (
    SELECT m.*
    FROM metrics m
    WHERE m.ts <= (SELECT cutoff_ts FROM percentile)
),
sealed_metrics AS (
    SELECT m.*
    FROM metrics m
    WHERE m.ts > (SELECT cutoff_ts FROM percentile)
),
-- Compute metrics for evaluation set
eval_stats AS (
    SELECT
        COUNT(*) as issued,
        SUM(up) as hits,
        COUNT(DISTINCT date(ts, 'unixepoch', 'utc')) as distinct_days
    FROM evaluation_metrics
),
-- Compute base rate in opportunities (entire set) for the predicted class (up) in opportunities that have a 5-day forward return
base_rate_opportunities AS (
    SELECT
        COUNT(*) as total_opp_with_fwd,
        SUM(CASE WHEN (close_5d - close) / close > 0 THEN 1 ELSE 0 END) as up_opp
    FROM (
        SELECT
            o.symbol_id,
            o.ts,
            o.close,
            LEAD(o.close, 5) OVER (PARTITION BY o.symbol_id ORDER BY o.ts) as close_5d
        FROM opportunities o
    ) sub
    WHERE close_5d IS NOT NULL
),
-- For EFFECTIVE_N: compute daily hit rates and variance
daily_hits AS (
    SELECT
        date(ts, 'unixepoch', 'utc') as day,
        COUNT(*) as daily_count,
        SUM(up) as daily_up
    FROM evaluation_metrics
    GROUP BY date(ts, 'unixepoch', 'utc')
),
overall_p AS (
    SELECT
        CAST(SUM(daily_up) AS REAL) / SUM(daily_count) as p
    FROM daily_hits
),
variance_daily AS (
    SELECT
        AVG(CAST(daily_up AS REAL)/daily_count) as mean_hit_rate,
        AVG((CAST(daily_up AS REAL)/daily_count)*(CAST(daily_up AS REAL)/daily_count)) as mean_sq_hit_rate
    FROM daily_hits
),
icc AS (
    SELECT
        (mean_sq_hit_rate - mean_hit_rate*mean_hit_rate) / (p*(1-p)) as icc_val
    FROM variance_daily, overall_p
    WHERE p > 0 AND p < 1  -- avoid division by zero
),
design_effect AS (
    SELECT
        1 + (AVG(daily_count) - 1) * (SELECT icc_val FROM icc) as deff
    FROM daily_hits
)
SELECT
    e.issued as ISSUED,
    (SELECT total_opp FROM total_opportunities) as OPPORTUNITIES,
    CASE WHEN e.issued > 0 THEN CAST(e.hits AS REAL) / e.issued ELSE NULL END as PRECISION,
    (SELECT CAST(up_opp AS REAL) / total_opp_with_fwd FROM base_rate_opportunities) as BASE_RATE,
    e.distinct_days as DISTINCT_DAYS,
    CASE WHEN e.issued > 0 THEN e.issued / (SELECT deff FROM design_effect) ELSE NULL END as EFFECTIVE_N,
    CASE WHEN s.issued > 0 THEN CAST(s.hits AS REAL) / s.issued ELSE NULL END as SEALED_PRECISION
FROM eval_stats e, (SELECT COUNT(*) as issued, SUM(up) as hits FROM sealed_metrics) s
"""

try:
    cur.execute(query)
    row = cur.fetchone()
    if row is None:
        print("INSUFFICIENT=1")
    else:
        issued, opportunities, precision, base_rate, distinct_days, effective_n, sealed_precision = row
        if issued is None or opportunities is None or precision is None or base_rate is None or distinct_days is None or effective_n is None or sealed_precision is None:
            print("INSUFFICIENT=1")
        else:
            # Check invariants
            if distinct_days > issued:
                print("INSUFFICIENT=1")
            elif effective_n >= issued:
                print("INSUFFICIENT=1")
            else:
                print(f"ISSUED={issued}")
                print(f"OPPORTUNITIES={opportunities}")
                print(f"PRECISION={precision:.6f}")
                print(f"BASE_RATE={base_rate:.6f}")
                print(f"DISTINCT_DAYS={distinct_days}")
                print(f"EFFECTIVE_N={effective_n:.6f}")
                print(f"SEALED_PRECISION={sealed_precision:.6f}")
except Exception as e:
    print("INSUFFICIENT=1")
finally:
    conn.close()