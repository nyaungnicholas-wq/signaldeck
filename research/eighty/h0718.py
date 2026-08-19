# SELECTION HISTORY -- written by ops/eighty-loop.ps1, do not edit by hand.
# corpus_size_at_generation: 717
# cycle_index: 44
# Any multiplicity correction applied downstream MUST use
# corpus_size_at_generation, not the size of the family this is
# promoted into.

import sqlite3
import math
from collections import defaultdict
from datetime import datetime, timezone
import bisect

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
conn.row_factory = sqlite3.Row

cur = conn.execute("""
    SELECT accession, symbol_id, insider, code, shares, price, value, tx_ts, filed_ts
    FROM insider_trades
    WHERE code = 'P'
    ORDER BY filed_ts
""")
purchases = [dict(row) for row in cur]

if not purchases:
    print("INSUFFICIENT=1")
    exit(0)

symbol_ids = set(p['symbol_id'] for p in purchases)
placeholders = ','.join('?' * len(symbol_ids))

cur = conn.execute(f"""
    SELECT symbol_id, ts, open, high, low, close, volume
    FROM bars
    WHERE tf = '1d' AND symbol_id IN ({placeholders})
    ORDER BY symbol_id, ts
""", list(symbol_ids))
bars_rows = [dict(row) for row in cur]

bars_by_symbol = defaultdict(list)
for row in bars_rows:
    bars_by_symbol[row['symbol_id']].append(row)

symbol_data = {}
for sid, bars in bars_by_symbol.items():
    bars.sort(key=lambda x: x['ts'])
    dates = []
    closes = []
    typical_prices = []
    log_rets = []
    for i, bar in enumerate(bars):
        dt = datetime.fromtimestamp(bar['ts'], tz=timezone.utc).date()
        dates.append(dt)
        closes.append(bar['close'])
        tp = (bar['high'] + bar['low'] + bar['close']) / 3.0
        typical_prices.append(tp)
        if i > 0:
            log_rets.append(math.log(bar['close'] / bars[i-1]['close']))
        else:
            log_rets.append(None)
    
    vol_20 = [None] * len(bars)
    for i in range(19, len(bars)):
        window = log_rets[i-19:i+1]
        if None not in window:
            mean = sum(window) / 20.0
            var = sum((x - mean) ** 2 for x in window) / 20.0
            vol_20[i] = math.sqrt(var)
    
    median_vol_252 = [None] * len(bars)
    for i in range(270, len(bars)):
        window = vol_20[i-251:i+1]
        valid = [v for v in window if v is not None]
        if len(valid) >= 200:
            valid.sort()
            mid = len(valid) // 2
            median_vol_252[i] = valid[mid] if len(valid) % 2 == 1 else (valid[mid-1] + valid[mid]) / 2.0
    
    fwd_ret_21 = [None] * len(bars)
    for i in range(len(bars) - 21):
        fwd_ret_21[i] = closes[i+21] / closes[i] - 1.0
    
    date_to_idx = {d: i for i, d in enumerate(dates)}
    symbol_data[sid] = {
        'dates': dates,
        'closes': closes,
        'typical_prices': typical_prices,
        'vol_20': vol_20,
        'median_vol_252': median_vol_252,
        'fwd_ret_21': fwd_ret_21,
        'date_to_idx': date_to_idx,
    }

purchases_by_insider = defaultdict(list)
for p in purchases:
    purchases_by_insider[p['insider']].append(p)

for insider, plist in purchases_by_insider.items():
    plist.sort(key=lambda x: x['filed_ts'])

for insider, plist in purchases_by_insider.items():
    for i, p in enumerate(plist):
        p['track_record_qualifies'] = False
        p['prior_trades'] = []
        for j in range(i):
            prior = plist[j]
            if p['filed_ts'] - prior['filed_ts'] <= 730 * 86400:
                p['prior_trades'].append(prior)

qualifying_insiders_by_symbol = defaultdict(set)
for insider, plist in purchases_by_insider.items():
    for p in plist:
        if len(p['prior_trades']) >= 5:
            hits = 0
            for prior in p['prior_trades']:
                sid = prior['symbol_id']
                if sid not in symbol_data:
                    continue
                sd = symbol_data[sid]
                prior_date = datetime.fromtimestamp(prior['filed_ts'], tz=timezone.utc).date()
                idx = sd['date_to_idx'].get(prior_date)
                if idx is not None and idx < len(sd['fwd_ret_21']) and sd['fwd_ret_21'][idx] is not None:
                    if sd['fwd_ret_21'][idx] > 0:
                        hits += 1
            if hits / len(p['prior_trades']) >= 0.55:
                p['track_record_qualifies'] = True
                qualifying_insiders_by_symbol[p['symbol_id']].add(insider)

calls = []
opportunities = 0
for p in purchases:
    if not p['track_record_qualifies']:
        continue
    sid = p['symbol_id']
    if len(qualifying_insiders_by_symbol[sid]) < 3:
        continue
    if sid not in symbol_data:
        continue
    sd = symbol_data[sid]
    decision_date = datetime.fromtimestamp(p['filed_ts'], tz=timezone.utc).date()
    trade_date = datetime.fromtimestamp(p['tx_ts'], tz=timezone.utc).date()
    
    idx = sd['date_to_idx'].get(decision_date)
    if idx is None:
        continue
    if idx == 0:
        continue
    vol_idx = idx - 1
    if sd['vol_20'][vol_idx] is None or sd['median_vol_252'][vol_idx] is None:
        continue
    if sd['vol_20'][vol_idx] >= sd['median_vol_252'][vol_idx]:
        continue
    
    trade_idx = sd['date_to_idx'].get(trade_date)
    if trade_idx is None:
        continue
    vwap_proxy = sd['typical_prices'][trade_idx]
    if p['price'] > vwap_proxy:
        continue
    
    if idx >= len(sd['fwd_ret_21']) or sd['fwd_ret_21'][idx] is None:
        continue
    
    opportunities += 1
    label = 1 if sd['fwd_ret_21'][idx] > 0 else 0
    calls.append({
        'decision_date': decision_date,
        'label': label,
        'filed_ts': p['filed_ts'],
    })

if not calls:
    print("INSUFFICIENT=1")
    exit(0)

calls.sort(key=lambda x: x['filed_ts'])
n = len(calls)
sealed_start = int(n * 0.8)
main_calls = calls[:sealed_start]
sealed_calls = calls[sealed_start:]

issued = n
hits = sum(c['label'] for c in calls)
precision = hits / issued if issued else 0.0
base_rate = precision

distinct_days = len(set(c['decision_date'] for c in calls))

day_counts = defaultdict(int)
for c in calls:
    day_counts[c['decision_date']] += 1
cluster_sizes = list(day_counts.values())
avg_cluster = sum(cluster_sizes) / len(cluster_sizes) if cluster_sizes else 1

day_labels = defaultdict(list)
for c in calls:
    day_labels[c['decision_date']].append(c['label'])

between_var = 0.0
within_var = 0.0
overall_mean = base_rate
for day, labels in day_labels.items():
    m = len(labels)
    day_mean = sum(labels) / m
    between_var += m * (day_mean - overall_mean) ** 2
    for lbl in labels:
        within_var += (lbl - day_mean) ** 2

k = len(day_labels)
if k > 1 and within_var > 0:
    msb = between_var / (k - 1)
    msw = within_var / (issued - k)
    icc = (msb - msw) / (msb + (avg_cluster - 1) * msw) if (msb + (avg_cluster - 1) * msw) > 0 else 0
    icc = max(0.0, icc)
else:
    icc = 0.0

design_effect = 1 + (avg_cluster - 1) * icc
if design_effect <= 1.0:
    design_effect = 1.01
effective_n = issued / design_effect

sealed_precision = 0.0
if sealed_calls:
    sealed_hits = sum(c['label'] for c in sealed_calls)
    sealed_precision = sealed_hits / len(sealed_calls)

print(f"ISSUED={issued}")
print(f"OPPORTUNITIES={opportunities}")
print(f"PRECISION={precision:.6f}")
print(f"BASE_RATE={base_rate:.6f}")
print(f"DISTINCT_DAYS={distinct_days}")
print(f"EFFECTIVE_N={effective_n:.2f}")
print(f"SEALED_PRECISION={sealed_precision:.6f}")