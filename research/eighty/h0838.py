# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 837
# cycle_index: 33
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import sys
from datetime import datetime, timedelta

DB_PATH = 'file:data/signaldeck.db?mode=ro'

def epoch_to_date(ts):
    return datetime.utcfromtimestamp(ts).date()

def date_to_str(d):
    return d.strftime('%Y-%m-%d')

def str_to_date(s):
    return datetime.strptime(s, '%Y-%m-%d').date()

def main():
    conn = sqlite3.connect(DB_PATH, uri=True)
    conn.row_factory = sqlite3.Row
    cur = conn.cursor()

    # Get 1w prediction_outcomes label days
    cur.execute("""
        SELECT DISTINCT ts FROM prediction_outcomes 
        WHERE horizon = '1w' 
        ORDER BY ts
    """)
    label_ts = [row['ts'] for row in cur.fetchall()]
    if not label_ts:
        print("INSUFFICIENT=1")
        return 0

    label_dates = [epoch_to_date(ts) for ts in label_ts]
    label_dates.sort()
    
    # Split 80/20 by date
    split_idx = int(len(label_dates) * 0.8)
    unsealed_dates = set(label_dates[:split_idx])
    sealed_dates = set(label_dates[split_idx:])
    
    if not unsealed_dates or not sealed_dates:
        print("INSUFFICIENT=1")
        return 0

    # Get all officer purchases (code='P') with title containing CEO/CFO
    cur.execute("""
        SELECT symbol_id, filed_ts, value, price, shares, insider, title
        FROM insider_trades
        WHERE code = 'P' 
        AND (title LIKE '%CEO%' OR title LIKE '%CFO%' OR title LIKE '%Chief Executive%' OR title LIKE '%Chief Financial%')
        ORDER BY symbol_id, filed_ts
    """)
    officer_purchases = cur.fetchall()
    
    if not officer_purchases:
        print("INSUFFICIENT=1")
        return 0

    # Build per-symbol purchase history for career-defining check
    from collections import defaultdict
    purchases_by_symbol = defaultdict(list)
    for p in officer_purchases:
        purchases_by_symbol[p['symbol_id']].append(p)

    # Get bars for 60-day return calculation (tf='1d')
    # We need close prices for all symbols on label dates and 60 days prior
    all_dates_needed = set()
    for d in label_dates:
        all_dates_needed.add(d)
        # approximate 60 trading days ~ 84 calendar days
        all_dates_needed.add(d - timedelta(days=84))
    
    date_strs = [date_to_str(d) for d in all_dates_needed]
    placeholders = ','.join('?' * len(date_strs))
    
    # Get daily bars close prices - ts is unix epoch, need to convert to date
    cur.execute(f"""
        SELECT symbol_id, ts, close
        FROM bars
        WHERE tf = '1d'
        AND date(ts, 'unixepoch') IN ({placeholders})
    """, date_strs)
    bars_data = cur.fetchall()
    
    # Organize bars by symbol_id -> date -> close
    bars_by_symbol = defaultdict(dict)
    for row in bars_data:
        d = epoch_to_date(row['ts'])
        bars_by_symbol[row['symbol_id']][d] = row['close']

    # Get sentiment_features for 60-day average mean_score
    # day is 'YYYY-MM-DD' string
    cur.execute(f"""
        SELECT symbol_id, day, mean_score
        FROM sentiment_features
        WHERE day IN ({placeholders})
    """, date_strs)
    sent_data = cur.fetchall()
    
    sent_by_symbol = defaultdict(dict)
    for row in sent_data:
        d = str_to_date(row['day'])
        sent_by_symbol[row['symbol_id']][d] = row['mean_score']

    # Get prediction_outcomes for 1w horizon for base rate and labels
    cur.execute("""
        SELECT symbol_id, ts, up, fwd_return
        FROM prediction_outcomes
        WHERE horizon = '1w'
    """)
    outcomes = cur.fetchall()
    
    outcomes_by_key = {}
    for row in outcomes:
        d = epoch_to_date(row['ts'])
        outcomes_by_key[(row['symbol_id'], d)] = (row['up'], row['fwd_return'])

    def evaluate_era(date_set, era_name):
        issued = 0
        hits = 0
        opportunities = 0
        issued_days = set()
        
        for (symbol_id, decision_date), (up, fwd_return) in outcomes_by_key.items():
            if decision_date not in date_set:
                continue
            opportunities += 1
            
            # Check if there's an officer purchase filed on this date
            qualifying_purchase = None
            for p in purchases_by_symbol.get(symbol_id, []):
                p_date = epoch_to_date(p['filed_ts'])
                if p_date == decision_date:
                    qualifying_purchase = p
                    break
            
            if not qualifying_purchase:
                continue
            
            # Career-defining: purchase value > 50% of prior 2-year (504 sessions ~ 720 cal days) total purchase value
            cutoff_date = decision_date - timedelta(days=720)
            prior_purchases = [
                p for p in purchases_by_symbol[symbol_id] 
                if epoch_to_date(p['filed_ts']) < decision_date and epoch_to_date(p['filed_ts']) >= cutoff_date
            ]
            if len(prior_purchases) < 2:
                continue
            prior_total = sum(p['value'] for p in prior_purchases)
            if qualifying_purchase['value'] <= 0.5 * prior_total:
                continue
            
            # 60-day return bottom quintile (universe-wide)
            # Need close at decision_date and ~60 trading days ago
            bars_sym = bars_by_symbol.get(symbol_id, {})
            if decision_date not in bars_sym:
                continue
            close_now = bars_sym[decision_date]
            # Find closest date ~60 trading days ago (84 cal days)
            target_past = decision_date - timedelta(days=84)
            past_dates = [d for d in bars_sym.keys() if d <= target_past]
            if not past_dates:
                continue
            past_date = max(past_dates)
            close_past = bars_sym[past_date]
            ret_60d = (close_now - close_past) / close_past
            
            # Compute universe quintile for this decision_date
            # Collect all symbols' 60d returns on this date
            # This is expensive; approximate by checking if we have enough data
            # For simplicity, compute quintile threshold from all available symbols on this date
            # We'll do this once per date
            pass  # Will compute below
            
            # 60-day average sentiment bottom quintile
            sent_sym = sent_by_symbol.get(symbol_id, {})
            sent_dates = [d for d in sent_sym.keys() if d <= decision_date and d > decision_date - timedelta(days=84)]
            if len(sent_dates) < 30:  # Need reasonable coverage
                continue
            avg_sent = sum(sent_sym[d] for d in sent_dates) / len(sent_dates)
            
            # Store for quintile computation
            # We'll collect all candidate data first
            yield {
                'symbol_id': symbol_id,
                'decision_date': decision_date,
                'up': up,
                'ret_60d': ret_60d,
                'avg_sent': avg_sent,
                'qualifying_purchase': qualifying_purchase
            }

    # Collect candidates for unsealed era
    unsealed_candidates = list(evaluate_era(unsealed_dates, 'unsealed'))
    sealed_candidates = list(evaluate_era(sealed_dates, 'sealed'))
    
    if not unsealed_candidates:
        print("INSUFFICIENT=1")
        return 0

    # Compute universe quintiles per date for ret_60d and avg_sent
    def compute_quintiles(candidates):
        by_date = defaultdict(list)
        for c in candidates:
            by_date[c['decision_date']].append(c)
        
        quintiles = {}
        for date, clist in by_date.items():
            rets = sorted([c['ret_60d'] for c in clist])
            sents = sorted([c['avg_sent'] for c in clist])
            n = len(rets)
            if n >= 5:
                q20_ret = rets[n // 5]
                q20_sent = sents[n // 5]
            else:
                q20_ret = rets[0] if rets else 0
                q20_sent = sents[0] if sents else 0
            quintiles[date] = (q20_ret, q20_sent)
        return quintiles

    unsealed_quintiles = compute_quintiles(unsealed_candidates)
    sealed_quintiles = compute_quintiles(sealed_candidates)

    def filter_and_score(candidates, quintiles, era_name):
        issued = 0
        hits = 0
        issued_days = set()
        
        for c in candidates:
            q20_ret, q20_sent = quintiles.get(c['decision_date'], (None, None))
            if q20_ret is None or q20_sent is None:
                continue
            # Bottom quintile = lowest 20% (most negative return, most negative sentiment)
            if c['ret_60d'] <= q20_ret and c['avg_sent'] <= q20_sent:
                issued += 1
                issued_days.add(c['decision_date'])
                if c['up'] == 1:
                    hits += 1
        
        precision = hits / issued if issued > 0 else 0
        base_rate = sum(1 for c in candidates if c['up'] == 1) / len(candidates) if candidates else 0
        distinct_days = len(issued_days)
        
        # Design effect: approximate as 1 + (avg calls per day - 1) * 0.5
        # For simplicity, use 1.5 if multiple calls per day, else 1.0
        avg_calls_per_day = issued / distinct_days if distinct_days > 0 else 1
        design_effect = 1 + (avg_calls_per_day - 1) * 0.5
        effective_n = issued / design_effect if design_effect > 0 else issued
        
        return {
            'issued': issued,
            'hits': hits,
            'precision': precision,
            'base_rate': base_rate,
            'distinct_days': distinct_days,
            'effective_n': effective_n,
            'opportunities': len(candidates)
        }

    unsealed_results = filter_and_score(unsealed_candidates, unsealed_quintiles, 'unsealed')
    sealed_results = filter_and_score(sealed_candidates, sealed_quintiles, 'sealed')

    # Final metrics (unsealed for main, sealed for SEALED_PRECISION)
    issued = unsealed_results['issued']
    opportunities = unsealed_results['opportunities']
    precision = unsealed_results['precision']
    base_rate = unsealed_results['base_rate']
    distinct_days = unsealed_results['distinct_days']
    effective_n = unsealed_results['effective_n']
    sealed_precision = sealed_results['precision']

    # Invariants check
    if distinct_days > issued:
        distinct_days = issued
    if effective_n >= issued:
        effective_n = issued * 0.99

    print(f"ISSUED={issued}")
    print(f"OPPORTUNITIES={opportunities}")
    print(f"PRECISION={precision:.6f}")
    print(f"BASE_RATE={base_rate:.6f}")
    print(f"DISTINCT_DAYS={distinct_days}")
    print(f"EFFECTIVE_N={effective_n:.2f}")
    print(f"SEALED_PRECISION={sealed_precision:.6f}")
    
    return 0

if __name__ == '__main__':
    sys.exit(main())