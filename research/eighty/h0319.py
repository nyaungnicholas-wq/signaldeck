# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 318
# cycle_index: 41
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

#!/usr/bin/env python3
import sqlite3
import datetime
from collections import defaultdict

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def quarter_to_date(quarter_str):
    """Convert 'YYYYQq' to date of quarter-end."""
    year = int(quarter_str[:4])
    q = int(quarter_str[-1])
    month = q * 3
    if month == 3:
        return datetime.date(year, 3, 31)
    elif month == 6:
        return datetime.date(year, 6, 30)
    elif month == 9:
        return datetime.date(year, 9, 30)
    else:
        return datetime.date(year, 12, 31)

def date_to_unix(d):
    """Convert date to unix epoch start of day."""
    return int(datetime.datetime.combine(d, datetime.time()).timestamp())

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
        c = conn.cursor()
        
        # Check for required data
        c.execute("SELECT COUNT(*) FROM inst_holdings")
        inst_count = c.fetchone()[0]
        c.execute("SELECT COUNT(*) FROM insider_trades")
        insider_count = c.fetchone()[0]
        if inst_count == 0 or insider_count == 0:
            print("INSUFFICIENT=1")
            return
            
        # Get all quarter-ends with institutional holdings
        c.execute("SELECT DISTINCT period FROM inst_holdings ORDER BY period")
        quarters = [row[0] for row in c.fetchall()]
        
        # Get symbols with insider trades (code P) in last 24 months from now
        now = datetime.datetime.now()
        two_years_ago = now - datetime.timedelta(days=24*30)
        two_years_ago_epoch = int(two_years_ago.timestamp())
        
        c.execute("""
            SELECT DISTINCT symbol_id FROM insider_trades 
            WHERE code='P' AND filed_ts >= ?
        """, (two_years_ago_epoch,))
        insider_symbols = set(row[0] for row in c.fetchall())
        
        if not insider_symbols:
            print("INSUFFICIENT=1")
            return
            
        # For each quarter-end, compute institutional ownership change and insider purchases
        signals = []
        
        for q_end_str in quarters:
            q_end = quarter_to_date(q_end_str)
            q_end_epoch = date_to_unix(q_end)
            
            # Get previous quarter-end (approx 91 days before)
            prev_q_end = q_end - datetime.timedelta(days=91)
            prev_q_end_epoch = date_to_unix(prev_q_end)
            
            # Get institutional holdings for current and previous quarter
            c.execute("""
                SELECT symbol_id, SUM(value) as total_value
                FROM inst_holdings
                WHERE period = ?
                GROUP BY symbol_id
            """, (q_end_str,))
            current_holdings = {row[0]: row[1] for row in c.fetchall()}
            
            # Get previous quarter string
            prev_q_end_str = f"{prev_q_end.year}Q{(prev_q_end.month-1)//3+1}"
            c.execute("""
                SELECT symbol_id, SUM(value) as total_value
                FROM inst_holdings
                WHERE period = ?
                GROUP BY symbol_id
            """, (prev_q_end_str,))
            prev_holdings = {row[0]: row[1] for row in c.fetchall()}
            
            # Find symbols with >=5% QoQ increase
            qualified_symbols = set()
            for sym in current_holdings:
                if sym not in prev_holdings or prev_holdings[sym] == 0:
                    continue
                increase = (current_holdings[sym] - prev_holdings[sym]) / prev_holdings[sym]
                if increase >= 0.05:
                    qualified_symbols.add(sym)
            
            if not qualified_symbols:
                continue
                
            # Get insider purchases in 30 days before quarter-end
            thirty_days_before = q_end - datetime.timedelta(days=30)
            thirty_days_before_epoch = date_to_unix(thirty_days_before)
            
            c.execute("""
                SELECT DISTINCT symbol_id FROM insider_trades
                WHERE code='P' 
                AND filed_ts >= ? 
                AND filed_ts <= ?
                AND symbol_id IN ({})
            """.format(','.join('?'*len(qualified_symbols))),
                      (thirty_days_before_epoch, q_end_epoch) + tuple(qualified_symbols))
            insider_qualified = set(row[0] for row in c.fetchall())
            
            if not insider_qualified:
                continue
                
            # Entry date: T+46 days after quarter-end
            entry_date = q_end + datetime.timedelta(days=46)
            entry_epoch = date_to_unix(entry_date)
            
            # Check symbol history (2 years of daily bars)
            cutoff_date = entry_date - datetime.timedelta(days=2*365)
            cutoff_epoch = date_to_unix(cutoff_date)
            
            for sym in insider_qualified:
                c.execute("""
                    SELECT COUNT(DISTINCT (ts/86400)) as days
                    FROM bars
                    WHERE symbol_id = ? AND tf = '1d' AND ts >= ? AND ts < ?
                """, (sym, cutoff_epoch, entry_epoch))
                day_count = c.fetchone()[0]
                if day_count < 200:
                    continue
                    
                signals.append((sym, entry_epoch, entry_date))
        
        if not signals:
            print("INSUFFICIENT=1")
            return
            
        # Get price data for signals
        hits = 0
        issued = 0
        opportunities = len(signals)
        entry_days = set()
        
        for sym, entry_epoch, entry_date in signals:
            # Get open price at entry
            c.execute("""
                SELECT open FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts LIMIT 1
            """, (sym, entry_epoch))
            row = c.fetchone()
            if not row:
                continue
            open_price = row[0]
            
            # Get close price 21 trading days later (approx 30 calendar days)
            exit_date = entry_date + datetime.timedelta(days=30)
            exit_epoch = date_to_unix(exit_date)
            
            c.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts DESC LIMIT 1
            """, (sym, exit_epoch))
            row = c.fetchone()
            if not row:
                continue
            close_price = row[0]
            
            # Calculate return
            ret = (close_price - open_price) / open_price
            if ret > 0:
                hits += 1
            issued += 1
            entry_days.add(entry_date.toordinal())
        
        if issued == 0:
            print("INSUFFICIENT=1")
            return
            
        # Split into train and sealed era (most recent 20%)
        sorted_days = sorted(entry_days)
        split_idx = int(len(sorted_days) * 0.8)
        sealed_days = set(sorted_days[split_idx:])
        
        # Recalculate with sealed era split
        train_hits = 0
        train_issued = 0
        sealed_hits = 0
        sealed_issued = 0
        
        for sym, entry_epoch, entry_date in signals:
            # Get open price at entry
            c.execute("""
                SELECT open FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts LIMIT 1
            """, (sym, entry_epoch))
            row = c.fetchone()
            if not row:
                continue
            open_price = row[0]
            
            # Get close price 21 trading days later
            exit_date = entry_date + datetime.timedelta(days=30)
            exit_epoch = date_to_unix(exit_date)
            
            c.execute("""
                SELECT close FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts >= ?
                ORDER BY ts DESC LIMIT 1
            """, (sym, exit_epoch))
            row = c.fetchone()
            if not row:
                continue
            close_price = row[0]
            
            ret = (close_price - open_price) / open_price
            is_hit = ret > 0
            is_sealed = entry_date.toordinal() in sealed_days
            
            if is_sealed:
                sealed_issued += 1
                if is_hit:
                    sealed_hits += 1
            else:
                train_issued += 1
                if is_hit:
                    train_hits += 1
        
        # Calculate metrics
        base_rate = hits / issued if issued > 0 else 0
        distinct_days = len(entry_days)
        
        # Calculate design effect (simplified: using day clustering)
        day_counts = defaultdict(int)
        for _, _, entry_date in signals:
            day_counts[entry_date.toordinal()] += 1
        
        n_clusters = len(day_counts)
        avg_cluster_size = issued / n_clusters if n_clusters > 0 else 1
        # Approximate design effect as 1 + avg_cluster_size (conservative)
        design_effect = 1 + avg_cluster_size
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        # Check invariants
        if distinct_days > issued:
            print("INSUFFICIENT=1")
            return
        if effective_n >= issued:
            print("INSUFFICIENT=1")
            return
            
        # Print results
        print(f"ISSUED={issued}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={base_rate:.4f}")
        print(f"BASE_RATE={base_rate:.4f}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n:.4f}")
        sealed_precision = sealed_hits / sealed_issued if sealed_issued > 0 else 0
        print(f"SEALED_PRECISION={sealed_precision:.4f}")
        
    except Exception as e:
        print("INSUFFICIENT=1")

if __name__ == "__main__":
    main()