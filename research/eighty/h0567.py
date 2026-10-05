# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 566
# cycle_index: 24
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta
from collections import defaultdict
import math

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def connect():
    return sqlite3.connect(DB_PATH, uri=True)

def load_symbols(conn):
    cur = conn.execute("SELECT id, symbol FROM symbols WHERE active=1")
    return {row[0]: row[1] for row in cur.fetchall()}

def load_fundamentals_float(conn):
    """Load EntityPublicFloat fundamentals: symbol_id, value, as_of, fetched_at"""
    cur = conn.execute("""
        SELECT symbol_id, value, as_of, fetched_at
        FROM fundamentals
        WHERE metric = 'EntityPublicFloat'
        ORDER BY symbol_id, fetched_at
    """)
    by_symbol = defaultdict(list)
    for sym_id, val, as_of, fetched_at in cur.fetchall():
        try:
            val = float(val)
            # Parse dates - handle both unix epoch and ISO strings
            def parse_ts(ts):
                if ts is None:
                    return None
                ts = str(ts).strip()
                if not ts:
                    return None
                # Try unix epoch first (10 or 13 digits)
                if ts.isdigit() and len(ts) >= 10:
                    ts_int = int(ts)
                    if ts_int > 1e12:  # milliseconds
                        ts_int //= 1000
                    return datetime.utcfromtimestamp(ts_int)
                # Try ISO format
                for fmt in ('%Y-%m-%d', '%Y-%m-%d %H:%M:%S', '%Y-%m-%dT%H:%M:%S'):
                    try:
                        return datetime.strptime(ts[:len(fmt)], fmt)
                    except ValueError:
                        pass
                return None
            as_of_dt = parse_ts(as_of)
            fetched_dt = parse_ts(fetched_at)
            if as_of_dt and fetched_dt and val > 0:
                by_symbol[sym_id].append((fetched_dt, as_of_dt, val))
        except (ValueError, TypeError):
            continue
    return by_symbol

def load_insider_purchases(conn):
    """Load insider purchases (code='P'): symbol_id, filed_ts, shares, value"""
    cur = conn.execute("""
        SELECT symbol_id, filed_ts, shares, value
        FROM insider_trades
        WHERE code = 'P'
        ORDER BY symbol_id, filed_ts
    """)
    by_symbol = defaultdict(list)
    for sym_id, filed_ts, shares, value in cur.fetchall():
        try:
            dt = parse_timestamp(filed_ts)
            if dt:
                by_symbol[sym_id].append((dt, float(shares or 0), float(value or 0)))
        except (ValueError, TypeError):
            continue
    return by_symbol

def parse_timestamp(ts):
    if ts is None:
        return None
    ts = str(ts).strip()
    if not ts:
        return None
    if ts.isdigit() and len(ts) >= 10:
        ts_int = int(ts)
        if ts_int > 1e12:
            ts_int //= 1000
        return datetime.utcfromtimestamp(ts_int)
    for fmt in ('%Y-%m-%d', '%Y-%m-%d %H:%M:%S', '%Y-%m-%dT%H:%M:%S'):
        try:
            return datetime.strptime(ts[:len(fmt)], fmt)
        except ValueError:
            pass
    return None

def load_news_sentiment(conn):
    """Load news sentiment: symbol_id, ts, sentiment"""
    cur = conn.execute("""
        SELECT symbol_id, ts, sentiment
        FROM news
        WHERE sentiment IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    by_symbol = defaultdict(list)
    for sym_id, ts, sent in cur.fetchall():
        try:
            dt = parse_timestamp(ts)
            sent = float(sent)
            if dt:
                by_symbol[sym_id].append((dt, sent))
        except (ValueError, TypeError):
            continue
    return by_symbol

def load_daily_volume(conn):
    """Load daily volume from bars (tf='1d'): symbol_id, ts, volume"""
    cur = conn.execute("""
        SELECT symbol_id, ts, volume
        FROM bars
        WHERE tf = '1d' AND volume IS NOT NULL AND volume > 0
        ORDER BY symbol_id, ts
    """)
    by_symbol = defaultdict(list)
    for sym_id, ts, vol in cur.fetchall():
        try:
            dt = parse_timestamp(ts)
            vol = float(vol)
            if dt:
                by_symbol[sym_id].append((dt, vol))
        except (ValueError, TypeError):
            continue
    return by_symbol

def load_labels(conn):
    """Load prediction_outcomes for horizon=21: symbol_id, ts, up"""
    cur = conn.execute("""
        SELECT symbol_id, ts, up
        FROM prediction_outcomes
        WHERE horizon = 21 AND up IS NOT NULL
        ORDER BY symbol_id, ts
    """)
    by_symbol = defaultdict(dict)
    for sym_id, ts, up in cur.fetchall():
        try:
            dt = parse_timestamp(ts)
            up = int(up)
            if dt:
                by_symbol[sym_id][dt] = up
        except (ValueError, TypeError):
            continue
    return by_symbol

def get_float_qoq_decline(float_data, decision_dt):
    """
    Check if public float declined >=10% QoQ as of decision_dt.
    Uses only data with fetched_at <= decision_dt.
    Returns (declined_bool, current_float, prior_float) or (False, None, None)
    """
    available = [(fetched, as_of, val) for fetched, as_of, val in float_data if fetched <= decision_dt]
    if len(available) < 2:
        return False, None, None
    # Sort by as_of (period end) to get chronological quarters
    available.sort(key=lambda x: x[1])
    # Take the two most recent quarters by as_of
    current = available[-1]
    prior = available[-2]
    # Ensure they're sequential quarters (roughly 90 days apart)
    if (current[1] - prior[1]).days > 120:  # allow some gap
        return False, None, None
    current_val = current[2]
    prior_val = prior[2]
    if prior_val <= 0:
        return False, None, None
    decline = (prior_val - current_val) / prior_val
    return decline >= 0.10, current_val, prior_val

def count_insider_purchases_21d(purchases, decision_dt):
    """Count insider purchases with filed_ts in [decision_dt-21d, decision_dt]"""
    cutoff = decision_dt - timedelta(days=21)
    count = 0
    for filed_dt, shares, value in purchases:
        if cutoff <= filed_dt <= decision_dt:
            count += 1
    return count

def get_news_sentiment_5d(news_data, decision_dt):
    """Get 5-day average sentiment ending at decision_dt"""
    cutoff = decision_dt - timedelta(days=5)
    sentiments = [sent for dt, sent in news_data if cutoff <= dt <= decision_dt]
    if not sentiments:
        return None
    return sum(sentiments) / len(sentiments)

def get_news_sentiment_5d_at(news_data, ref_dt):
    """Get 5-day average sentiment ending at ref_dt"""
    cutoff = ref_dt - timedelta(days=5)
    sentiments = [sent for dt, sent in news_data if cutoff <= dt <= ref_dt]
    if not sentiments:
        return None
    return sum(sentiments) / len(sentiments)

def get_avg_volume_20d(volume_data, decision_dt):
    """Get 20-day average volume ending at decision_dt"""
    cutoff = decision_dt - timedelta(days=20)
    volumes = [vol for dt, vol in volume_data if cutoff <= dt <= decision_dt]
    if not volumes:
        return None
    return sum(volumes) / len(volumes)

def find_label(labels, decision_dt):
    """Find label for horizon=21 at or nearest to decision_dt"""
    if not labels:
        return None
    # Find exact match first
    if decision_dt in labels:
        return labels[decision_dt]
    # Find closest timestamp within reasonable window (e.g., 1 day)
    closest = None
    min_diff = timedelta(days=1)
    for ts, up in labels.items():
        diff = abs(ts - decision_dt)
        if diff < min_diff:
            min_diff = diff
            closest = up
    return closest

def main():
    conn = connect()
    
    print("Loading data...", file=sys.stderr)
    symbols = load_symbols(conn)
    float_data = load_fundamentals_float(conn)
    insider_data = load_insider_purchases(conn)
    news_data = load_news_sentiment(conn)
    volume_data = load_daily_volume(conn)
    labels = load_labels(conn)
    
    # Universe: symbols with at least 2 quarters float, insider trades, news, and volume data
    universe_symbols = []
    for sym_id in symbols:
        if (sym_id in float_data and len(float_data[sym_id]) >= 2 and
            sym_id in insider_data and len(insider_data[sym_id]) > 0 and
            sym_id in news_data and len(news_data[sym_id]) > 0 and
            sym_id in volume_data and len(volume_data[sym_id]) > 0 and
            sym_id in labels and len(labels[sym_id]) > 0):
            universe_symbols.append(sym_id)
    
    print(f"Universe size: {len(universe_symbols)}", file=sys.stderr)
    
    if len(universe_symbols) == 0:
        print("INSUFFICIENT=1")
        return
    
    # For each symbol, determine valid decision date range
    all_decisions = []  # (decision_dt, sym_id, label)
    
    for sym_id in universe_symbols:
        float_sym = float_data[sym_id]
        insider_sym = insider_data[sym_id]
        news_sym = news_data[sym_id]
        vol_sym = volume_data[sym_id]
        label_sym = labels[sym_id]
        
        # Earliest decision date: need 20d volume, 15d news, 21d insider, 2 quarters float
        # Float: need two quarters with fetched_at <= decision_dt
        # Get earliest fetched_at of second quarter
        float_sym_sorted = sorted(float_sym, key=lambda x: x[0])  # by fetched_at
        if len(float_sym_sorted) < 2:
            continue
        earliest_float_dt = float_sym_sorted[1][0]  # fetched_at of second quarter
        
        earliest_news_dt = news_sym[0][0] + timedelta(days=15)
        earliest_insider_dt = insider_sym[0][0] + timedelta(days=21)
        earliest_vol_dt = vol_sym[0][0] + timedelta(days=20)
        
        start_dt = max(earliest_float_dt, earliest_news_dt, earliest_insider_dt, earliest_vol_dt)
        
        # Latest decision date: need label at decision_dt + 21d (but label ts is decision time)
        # Labels are at prediction time, so we need decision_dt to have a label
        latest_label_dt = max(label_sym.keys())
        end_dt = latest_label_dt
        
        if start_dt >= end_dt:
            continue
        
        # Generate decision points at daily frequency (UTC days)
        # Align to UTC days (midnight)
        current_dt = datetime(start_dt.year, start_dt.month, start_dt.day)
        end_day = datetime(end_dt.year, end_dt.month, end_dt.day)
        
        while current_dt <= end_day:
            # Check entry conditions
            # 1. Float decline >=10% QoQ
            float_declined, _, _ = get_float_qoq_decline(float_sym, current_dt)
            if not float_declined:
                current_dt += timedelta(days=1)
                continue
            
            # 2. At least 3 insider purchases in last 21 days
            insider_count = count_insider_purchases_21d(insider_sym, current_dt)
            if insider_count < 3:
                current_dt += timedelta(days=1)
                continue
            
            # 3. 5-day news sentiment below -0.1
            sent_5d = get_news_sentiment_5d(news_sym, current_dt)
            if sent_5d is None or sent_5d >= -0.1:
                current_dt += timedelta(days=1)
                continue
            
            # 4. Improved by >=0.1 in 10 days (compare current 5d vs 5d ending 10 days ago)
            sent_5d_10d_ago = get_news_sentiment_5d_at(news_sym, current_dt - timedelta(days=10))
            if sent_5d_10d_ago is None or (sent_5d - sent_5d_10d_ago) < 0.1:
                current_dt += timedelta(days=1)
                continue
            
            # 5. 20-day average volume > 100,000
            avg_vol = get_avg_volume_20d(vol_sym, current_dt)
            if avg_vol is None or avg_vol < 100000:
                current_dt += timedelta(days=1)
                continue
            
            # All conditions met - check for label
            label = find_label(label_sym, current_dt)
            if label is not None:
                all_decisions.append((current_dt, sym_id, label))
            
            current_dt += timedelta(days=1)
    
    print(f"Total decisions (opportunities): {len(all_decisions)}", file=sys.stderr)
    
    if len(all_decisions) == 0:
        print("INSUFFICIENT=1")
        return
    
    # Sort by decision date
    all_decisions.sort(key=lambda x: x[0])
    
    # Split: hold out most recent 20% as sealed era
    split_idx = int(len(all_decisions) * 0.8)
    train_decisions = all_decisions[:split_idx]
    sealed_decisions = all_decisions[split_idx:]
    
    def compute_metrics(decisions):
        if not decisions:
            return 0, 0, 0.0, 0.0, 0, 0.0
        issued = [(dt, sym, label) for dt, sym, label in decisions if label == 1]
        # Wait - "issued" means we made a call (entry conditions met).
        # The hypothesis tests precision of the calls we ISSUE.
        # So ALL decisions where entry conditions met are "issued calls".
        # The label tells us if it was a hit (up=1) or miss (up=0).
        
        issued_calls = decisions  # all are issued since we only record when conditions met
        n_issued = len(issued_calls)
        hits = sum(1 for _, _, label in issued_calls if label == 1)
        precision = hits / n_issued if n_issued > 0 else 0.0
        base_rate = hits / n_issued if n_issued > 0 else 0.0  # base rate within issued subset
        distinct_days = len(set(dt for dt, _, _ in issued_calls))
        
        # Design effect: cluster by time. Compute effective N.
        # Simple approach: design effect = 1 + (avg cluster size - 1) * ICC
        # Approximate: group by week, compute variance inflation
        if n_issued <= 1:
            effective_n = float(n_issued)
        else:
            # Group by week
            week_counts = defaultdict(int)
            for dt, _, _ in issued_calls:
                week_key = (dt.year, dt.isocalendar()[1])
                week_counts[week_key] += 1
            cluster_sizes = list(week_counts.values())
            mean_cluster = sum(cluster_sizes) / len(cluster_sizes)
            # ICC approximation for financial returns ~ 0.1-0.3
            # Use conservative ICC = 0.2
            icc = 0.2
            design_effect = 1 + (mean_cluster - 1) * icc
            effective_n = n_issued / design_effect
        
        return n_issued, hits, precision, base_rate, distinct_days, effective_n
    
    # Full sample metrics
    n_issued, hits, precision, base_rate, distinct_days, effective_n = compute_metrics(all_decisions)
    
    # Sealed era metrics
    sealed_issued, sealed_hits, sealed_precision, _, _, _ = compute_metrics(sealed_decisions)
    
    # Output required lines
    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={len(all_decisions)}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.6f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")

if __name__ == "__main__":
    main()