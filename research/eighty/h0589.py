#!/usr/bin/env python3
import sqlite3
import sys
from collections import defaultdict

def connect_db():
    try:
        conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=10)
        return conn
    except sqlite3.Error:
        return None

def get_decision_points(conn):
    """Get all insider purchase decision points with sufficient price history."""
    query = """
    SELECT i.symbol_id, i.filed_ts as decision_ts
    FROM insider_trades i
    JOIN symbols s ON i.symbol_id = s.id
    JOIN (
        SELECT symbol_id, COUNT(*) as n_days
        FROM bars
        WHERE tf='1d'
        GROUP BY symbol_id
    ) p ON i.symbol_id = p.symbol_id
    WHERE i.code = 'P'
      AND s.active = 1
      AND p.n_days >= 100
    """
    cursor = conn.execute(query)
    return [(row[0], row[1]) for row in cursor.fetchall()]

def compute_conditions(conn, symbol_id, decision_ts):
    """Check all entry conditions for a given symbol and decision timestamp."""
    # Get 50-day high close before decision
    q1 = """
    SELECT MAX(close)
    FROM bars
    WHERE symbol_id = ? AND tf = '1d' AND ts < ?
    ORDER BY ts DESC
    LIMIT 50
    """
    cursor = conn.execute(q1, (symbol_id, decision_ts))
    row = cursor.fetchone()
    if not row or row[0] is None:
        return False
    high_50d = row[0]
    
    # Get current close price (most recent bar before decision)
    q2 = """
    SELECT close
    FROM bars
    WHERE symbol_id = ? AND tf = '1d' AND ts < ?
    ORDER BY ts DESC
    LIMIT 1
    """
    cursor = conn.execute(q2, (symbol_id, decision_ts))
    row = cursor.fetchone()
    if not row or row[0] is None:
        return False
    current_close = row[0]
    
    # Condition 1: close >= 5% below 50-day high
    if current_close > high_50d * 0.95:
        return False
    
    # Get 5-day average news sentiment
    q3 = """
    SELECT AVG(mean_score)
    FROM sentiment_features
    WHERE symbol_id = ? AND day <= date(?, 'unixepoch')
    ORDER BY day DESC
    LIMIT 5
    """
    cursor = conn.execute(q3, (symbol_id, decision_ts))
    row = cursor.fetchone()
    if not row or row[0] is None:
        return False
    avg_sentiment = row[0]
    
    # Condition 2: sentiment < -0.1
    if avg_sentiment >= -0.1:
        return False
    
    # Get 14-day RSI
    q4 = """
    SELECT close
    FROM bars
    WHERE symbol_id = ? AND tf = '1d' AND ts < ?
    ORDER BY ts DESC
    LIMIT 15
    """
    cursor = conn.execute(q4, (symbol_id, decision_ts))
    closes = [row[0] for row in cursor.fetchall()]
    if len(closes) < 15:
        return False
    
    # Compute RSI from close prices
    gains = []
    losses = []
    for i in range(1, len(closes)):
        change = closes[i-1] - closes[i]  # Note: most recent is first
        if change > 0:
            gains.append(change)
            losses.append(0)
        else:
            gains.append(0)
            losses.append(-change)
    
    if len(gains) < 14:
        return False
    
    avg_gain = sum(gains[:14]) / 14
    avg_loss = sum(losses[:14]) / 14
    
    if avg_loss == 0:
        rsi = 100
    else:
        rs = avg_gain / avg_loss
        rsi = 100 - (100 / (1 + rs))
    
    # Condition 3: RSI < 35
    if rsi >= 35:
        return False
    
    # Get 20-day average dollar volume
    q5 = """
    SELECT AVG(close * volume)
    FROM bars
    WHERE symbol_id = ? AND tf = '1d' AND ts < ?
    ORDER BY ts DESC
    LIMIT 20
    """
    cursor = conn.execute(q5, (symbol_id, decision_ts))
    row = cursor.fetchone()
    if not row or row[0] is None:
        return False
    avg_dollar_vol = row[0]
    
    # Condition 4: dollar volume >= $1 million
    if avg_dollar_vol < 1000000:
        return False
    
    return True

def get_label(conn, symbol_id, entry_ts, horizon_days=21):
    """Get forward return label for a symbol starting from entry_ts."""
    # Find entry price (first bar after entry_ts)
    q1 = """
    SELECT ts, close
    FROM bars
    WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
    ORDER BY ts ASC
    LIMIT 1
    """
    cursor = conn.execute(q1, (symbol_id, entry_ts))
    row = cursor.fetchone()
    if not row:
        return None
    entry_ts_actual = row[0]
    entry_price = row[1]
    
    # Find exit price (21 trading days later)
    q2 = """
    SELECT close
    FROM bars
    WHERE symbol_id = ? AND tf = '1d' AND ts > ?
    ORDER BY ts ASC
    LIMIT ?
    """
    cursor = conn.execute(q2, (symbol_id, entry_ts_actual, horizon_days))
    rows = cursor.fetchall()
    if len(rows) < horizon_days:
        return None
    exit_price = rows[-1][0]
    
    return (exit_price - entry_price) / entry_price

def compute_metrics(calls):
    """Compute all required metrics from list of (day, return) tuples."""
    if not calls:
        return None
    
    # Group by day
    by_day = defaultdict(list)
    for day, ret in calls:
        by_day[day].append(ret)
    
    # Base metrics
    issued = len(calls)
    days = list(by_day.keys())
    distinct_days = len(days)
    
    # Compute base rate
    up_count = sum(1 for _, ret in calls if ret > 0)
    base_rate = up_count / issued
    
    # Compute design effect
    n_days = len(days)
    if n_days <= 1:
        design_effect = 1.0  # Minimum
    else:
        # Compute ICC
        overall_mean = sum(ret for _, ret in calls) / issued
        overall_var = sum((ret - overall_mean) ** 2 for _, ret in calls) / (issued - 1)
        
        between_var = 0
        within_var = 0
        for day, returns in by_day.items():
            m = len(returns)
            day_mean = sum(returns) / m
            between_var += m * (day_mean - overall_mean) ** 2
            within_var += sum((r - day_mean) ** 2 for r in returns)
        
        between_var /= (n_days - 1)
        within_var /= (issued - n_days)
        
        if overall_var == 0:
            design_effect = 1.0
        else:
            icc = between_var / (between_var + within_var)
            avg_m = issued / n_days
            design_effect = 1 + (avg_m - 1) * icc
    
    effective_n = issued / design_effect
    
    return {
        'issued': issued,
        'base_rate': base_rate,
        'distinct_days': distinct_days,
        'effective_n': effective_n
    }

def main():
    # Check if database exists
    conn = connect_db()
    if not conn:
        print("INSUFFICIENT=1")
        return 0
    
    try:
        # Get all decision points
        decision_points = get_decision_points(conn)
        if not decision_points:
            print("INSUFFICIENT=1")
            return 0
        
        # Get all calls (opportunities that meet all conditions)
        calls = []
        opportunities = 0
        
        for symbol_id, decision_ts in decision_points:
            opportunities += 1
            if compute_conditions(conn, symbol_id, decision_ts):
                label = get_label(conn, symbol_id, decision_ts)
                if label is not None:
                    # Convert timestamp to date