import sqlite3
from collections import defaultdict
from datetime import datetime, timedelta
import math

def main():
    conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
    c = conn.cursor()
    
    # Get all 13F quarters and compute decision dates (46 days after quarter-end)
    c.execute("SELECT DISTINCT period FROM inst_holdings")
    quarters = [row[0] for row in c.fetchall()]
    if not quarters:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    quarter_ends = [datetime.strptime(q, "%Y-%m-%d") for q in quarters]
    decision_dates = []
    for qe in sorted(quarter_ends):
        dd = qe + timedelta(days=46)
        decision_dates.append((qe, dd))
    
    # Get all symbols with 13F data
    c.execute("SELECT DISTINCT symbol_id FROM inst_holdings")
    symbol_ids = [row[0] for row in c.fetchall()]
    
    opportunities = []  # (symbol_id, decision_date, prior_q, cur_q, prior_holder_count, cur_holder_count, prior_agg, cur_agg, median_vol_21d, forward_return, hit)
    
    for qe, dd in decision_dates:
        qe_str = qe.strftime("%Y-%m-%d")
        dd_str = dd.strftime("%Y-%m-%d")
        dd_epoch = int(dd.timestamp())
        
        # Find previous quarter
        prev_qe = None
        for q, d in decision_dates:
            if q < qe:
                prev_qe = q
        if prev_qe is None:
            continue
        prev_qe_str = prev_qe.strftime("%Y-%m-%d")
        
        for symbol_id in symbol_ids:
            # Get holder counts and aggregates for current and previous quarter
            c.execute("""
                SELECT COUNT(DISTINCT manager), SUM(value)
                FROM inst_holdings
                WHERE symbol_id = ? AND period = ?
            """, (symbol_id, qe_str))
            cur_row = c.fetchone()
            if not cur_row or cur_row[0] is None or cur_row[1] is None:
                continue
            cur_holders, cur_agg = cur_row[0], cur_row[1]
            
            c.execute("""
                SELECT COUNT(DISTINCT manager), SUM(value)
                FROM inst_holdings
                WHERE symbol_id = ? AND period = ?
            """, (symbol_id, prev_qe_str))
            prev_row = c.fetchone()
            if not prev_row or prev_row[0] is None or prev_row[1] is None:
                continue
            prev_holders, prev_agg = prev_row[0], prev_row[1]
            
            # Check universe criteria: at least 10 prior holders
            if prev_holders < 10:
                continue
            
            # Check if at least 21 daily bars up to decision date
            c.execute("""
                SELECT COUNT(*)
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
            """, (symbol_id, dd_epoch))
            bar_count = c.fetchone()[0]
            if bar_count < 21:
                continue
            
            # Get 21-day median dollar volume up to decision date
            c.execute("""
                SELECT close, volume
                FROM bars
                WHERE symbol_id = ? AND tf = '1d' AND ts <= ?
                ORDER BY ts DESC
                LIMIT 21
            """, (symbol_id, dd_epoch))
            recent_bars = c.fetchall()
            if len(recent_bars) < 21:
                continue
            dollar_volumes = [close * vol for close, vol in recent_bars]
            median_vol = sorted(dollar_volumes)[len(dollar_volumes)//2]
            
            # Check entry conditions
            holder_drop = (prev_holders - cur_holders) / prev_holders
            agg_change = (cur_agg - prev_agg) / prev_agg if prev_agg != 0 else 0
            
            if holder_drop < 0.20:
                continue
            if agg_change > 0.05:
                continue
            
            # Get all 1d bars for forward return calculation
            c.execute("""
                SELECT ts, close
                FROM bars
                WHERE symbol_id = ? AND tf = '1d'
                ORDER BY ts ASC
            """, (symbol_id,))
            all_bars = c.fetchall()
            bar_dict = {ts: close for ts, close in all_bars}
            bar_timestamps = sorted(bar_dict.keys())
            
            # Find the bar on decision date
            decision_bar = None
            for ts in bar_timestamps:
                if ts >= dd_epoch:
                    decision_bar = ts
                    break
            if decision_bar is None:
                continue
            
            # Get forward return: 21 trading days after decision date
            idx = bar_timestamps.index(decision_bar)
            if idx + 21 >= len(bar_timestamps):
                continue
            forward_ts = bar_timestamps[idx + 21]
            forward_close = bar_dict[forward_ts]
            decision_close = bar_dict[decision_bar]
            fwd_return = (forward_close - decision_close) / decision_close
            hit = 1 if fwd_return < 0 else 0  # Short call, want negative return
            
            # Check if we're in bottom quintile of median volume - we'll filter after computing all medians
            opportunities.append({
                'symbol_id': symbol_id,
                'decision_date': dd,
                'qe': qe,
                'prev_holders': prev_holders,
                'cur_holders': cur_holders,
                'prev_agg': prev_agg,
                'cur_agg': cur_agg,
                'median_vol': median_vol,
                'fwd_return': fwd_return,
                'hit': hit
            })
    
    if not opportunities:
        print("INSUFFICIENT=1")
        conn.close()
        return
    
    # Compute median volume quintiles
    all_medians = [opp['median_vol'] for opp in opportunities]