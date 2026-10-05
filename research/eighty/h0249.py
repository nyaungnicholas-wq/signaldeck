#!/usr/bin/env python3
import sqlite3
import math
from datetime import datetime, timezone

def main():
    try:
        db = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True, timeout=30)
    except Exception:
        print("INSUFFICIENT=1")
        return

    cur = db.cursor()

    # Load symbols
    try:
        cur.execute("SELECT id, symbol FROM symbols")
        sym_map = {r[0]: r[1] for r in cur.fetchall()}
    except Exception:
        print("INSUFFICIENT=1"); db.close(); return

    # Load all daily bars
    try:
        cur.execute("""
            SELECT symbol_id, ts, open, high, low, close, volume
            FROM bars WHERE tf='1d' ORDER BY symbol_id, ts
        """)
        raw = cur.fetchall()
    except Exception:
        print("INSUFFICIENT=1"); db.close(); return
    db.close()

    if not raw:
        print("INSUFFICIENT=1"); return

    # Group by symbol
    bars_by_sym = {}
    for sid, ts, o, h, lo, cl, vol in raw:
        bars_by_sym.setdefault(sid, []).append((ts, o, h, lo, cl, vol))

    LB = 252
    VL = 60
    FW = 20
    MIN_DV = 5_000_000
    VOL_M = 2.5
    RET_TH = -0.01
    QUART = 0.75
    MIN_REM = 30

    # First pass: compute all candidate opportunities with volatility
    opps = []
    for sid, bars in bars_by_sym.items():
        n = len(bars)
        if n < LB + FW + 1:
            continue

        closes = [b[4] for b in bars]
        highs = [b[2] for b in bars]
        lows = [b[3] for b in bars]
        volumes = [b[5] for b in bars]
        times = [b[0] for b in bars]

        # Precompute returns
        rets = [None] * n
        for i in range(1, n):
            if closes[i-1] > 0:
                rets[i] = closes[i] / closes[i-1] - 1

        # Precompute dollar volume
        dvol = [closes[i] * volumes[i] for i in range(n)]

        for i in range(LB, n - FW):
            remaining = n - i - 1
            if remaining < MIN_REM:
                continue

            t_cl = closes[i]
            t_h = highs[i]
            t_lo = lows[i]
            t_vol = volumes[i]
            t_ts = times[i]

            # Price check
            if t_cl < 5.0:
                continue

            # Average dollar volume T-60..T-1
            avg_dv = sum(dvol[i-VL:i]) / VL
            if avg_dv < MIN_DV:
                continue

            # New 251-session low
            min_prior = min(lows[i-LB+1:i])
            if t_lo >= min_prior:
                continue

            # Volume spike >= 2.5x average
            avg_vol = sum(volumes[i-VL:i]) / VL
            if avg_vol <= 0 or t_vol < VOL_M * avg_vol:
                continue

            # Close in top quartile of range
            rng = t_h - t_lo
            if rng <= 0:
                continue
            if t_cl < t_lo + QUART * rng:
                continue

            # Return >= -1%
            if closes[i-1] <= 0:
                continue
            ret_i = (t_cl - closes[i-1]) / closes[i-1]
            if ret_i < RET_TH:
                continue

            # 20-day realized volatility
            vr = [rets[j] for j in range(i-19, i+1) if rets[j] is not None]
            if len(vr) < 10:
                continue
            vm = sum(vr) / len(vr)
            vv = sum((r-vm)**2 for r in vr) / max(1, len(vr)-1)
            vol20 = math.sqrt(vv)

            # Label
            fwd_cl = closes[i+FW]
            if t_cl <= 0:
                continue
            fwd_ret = (fwd_cl - t_cl) / t_cl
            hit = fwd_ret > 0

            dt = datetime.fromtimestamp(t_ts, tz=timezone.utc).date()
            opps.append((sid, i, t_ts, dt, t_cl, vol20, hit, fwd_ret))

    if not opps:
        print("INSUFFICIENT=1"); return

    # Cross-sectional volatility decile per date
    by_date = {}
    for o in opps:
        by_date.setdefault(o[3], []).append(o)

    vol_thresh = {}
    for d, lst in by_date.items():
        vols = sorted([o[5] for o in lst])
        if len(vols) >= 10:
            vol_thresh[d] = vols[int(0.9 * len(vols))]
        else:
            vol_thresh[d] = float('inf')

    # Filter out top volatility decile
    filtered = [o for o in opps if o[5] < vol_thresh.get(o[3], float('inf'))]
    if not filtered:
        print("INSUFFICIENT=1"); return

    # Sort by timestamp
    filtered.sort(key=lambda x: x[2])

    # Apply cooldown per symbol (20 trading days)
    last_call = {}
    issued = []
    for o in filtered:
        sid, idx = o[0], o[1]
        if sid in last_call and idx - last_call[sid] < FW:
            continue
        issued.append(o)
        last_call[sid] = idx

    if not issued:
        print("INSUFFICIENT=1"); return

    n_issued = len(issued)
    n_opps = len(opps)
    n_hits = sum(1 for o in issued if o[6])
    precision = n_hits / n_issued

    # Base rate = overall UP rate across all opportunities
    total_hits = sum(1 for o in opps if o[6])
    base_rate = total_hits / n_opps if n_opps > 0 else 0

    # Distinct days among issued
    dist_days = len(set(o[3] for o in issued))

    # Design effect via ICC
    calls_by_day = {}
    for o in issued:
        calls_by_day.setdefault(o[3], []).append(o)

    n_days = len(calls_by_day)
    if n_days > 1 and n_issued > 1:
        p = n_hits / n_issued
        s2b = sum(len(c)*(sum(1 for x in c if x[6])/len(c)-p)**2
                  for c in calls_by_day.values()) / (n_days-1)
        s2w_num = sum(sum(1 for x in c if x[6])/len(c)*(1-sum(1 for x in c if x[6])/len(c))*len(c)
                      for c in calls_by_day.values())
        s2w = s2w_num / (n_issued - n_days) if (n_issued - n_days) > 0 else 1e-9
        m0 = (n_issued - sum(len(c)**2 for c in calls_by_day.values())/n_issued) / (n_days-1)
        denom = s2b + (m0-1)*s2w if (s2b + (m0-1)*s2w) > 0 else 1e-9
        icc = (s2b - s2w) / denom
        icc = max(0, min(icc, 1))
        de = 1 + (m0-1)*icc
        de = max(de, 1.01)
    else:
        de = max(n_issued / 1.01, 1.01)

    eff_n = n_issued / de

    # Sealed era: most recent 20% of issued calls
    n_sealed = max(1, int(round(0.2 * n_issued)))
    sealed = issued[-n_sealed:]
    sealed_hits = sum(1 for o in sealed if o[6])
    sealed_prec = sealed_hits / len(sealed) if sealed else 0

    print(f"ISSUED={n_issued}")
    print(f"OPPORTUNITIES={n_opps}")
    print(f"PRECISION={precision:.4f}")
    print(f"BASE_RATE={base_rate:.4f}")
    print(f"DISTINCT_DAYS={dist_days}")
    print(f"EFFECTIVE_N={eff_n:.2f}")
    print(f"SEALED_PRECISION={sealed_prec:.4f}")

if __name__ == "__main__":
    main()