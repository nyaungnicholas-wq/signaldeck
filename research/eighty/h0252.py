import sqlite3
import math
from collections import defaultdict
from datetime import datetime

DB_PATH = "file:data/signaldeck.db?mode=ro"

def main():
    try:
        conn = sqlite3.connect(DB_PATH, uri=True)
    except Exception:
        print("INSUFFICIENT=1")
        return

    try:
        # Get insider purchases
        cur = conn.execute(
            "SELECT symbol_id, tx_ts, filed_ts FROM insider_trades WHERE code='P'"
        )
        purchases = cur.fetchall()
        if not purchases:
            print("INSUFFICIENT=1")
            return

        # Group by (symbol_id, T=filed_ts)
        purchase_map = defaultdict(set)
        for sym, tx_ts, filed_ts in purchases:
            purchase_map[(sym, filed_ts)].add(tx_ts)

        # Get all symbols
        cur = conn.execute("SELECT id FROM symbols")
        valid_symbols = {row[0] for row in cur.fetchall()}

        # Collect all daily bars
        cur = conn.execute(
            "SELECT symbol_id, ts, close, volume FROM bars WHERE tf='1d' ORDER BY symbol_id, ts"
        )
        bars_by_sym = defaultdict(list)
        for sym, ts, close, vol in cur.fetchall():
            if sym in valid_symbols:
                bars_by_sym[sym].append((ts, close, vol))

        # Get prediction outcomes for horizon=20
        cur = conn.execute(
            "SELECT symbol_id, ts, up FROM prediction_outcomes WHERE horizon=20"
        )
        label_map = {(row[0], row[1]): row[2] for row in cur.fetchall()}

        # Generate decision points
        decisions = []
        for (sym, T), tx_timestamps in purchase_map.items():
            if sym not in bars_by_sym:
                continue
            bars = bars_by_sym[sym]
            if len(bars) < 253:
                continue

            # Find index of first bar >= T
            idx_T = None
            for i, (ts, _, _) in enumerate(bars):
                if ts >= T:
                    idx_T = i
                    break
            if idx_T is None:
                continue
            if idx_T < 252:
                continue

            close_T1 = bars[idx_T - 1][1]
            if close_T1 < 5.0:
                continue

            # Average daily dollar volume T-60..T-1
            start_idx = idx_T - 60
            if start_idx < 0:
                continue
            total_dvol = 0
            for i in range(start_idx, idx_T):
                close, vol = bars[i][1], bars[i][2]
                total_dvol += close * vol
            avg_dvol = total_dvol / 60
            if avg_dvol < 5_000_000:
                continue

            # 20-day return through T-1
            if idx_T < 20:
                continue
            close_T20 = bars[idx_T - 20][1]
            ret_20 = (close_T1 - close_T20) / close_T20
            if ret_20 > -0.10:
                continue

            # Check at least one trade in T-10..T-1
            qualifying = False
            for tx in tx_timestamps:
                if T - 10 * 86400 <= tx <= T - 86400:
                    qualifying = True
                    break
            if not qualifying:
                continue

            # 20-day realized volatility
            vol_ret = []
            for i in range(idx_T - 20, idx_T):
                prev = bars[i][1]
                next_ = bars[i + 1][1]
                vol_ret.append((next_ - prev) / prev)
            vol20 = math.sqrt(sum(r * r for r in vol_ret) / 20)

            decisions.append((sym, T, close_T1, vol20, idx_T, bars))

        if len(decisions) < 30:
            print("INSUFFICIENT=1")
            return

        # Cross-sectional volatility thresholds by T
        vol_by_T = defaultdict(list)
        for sym, T, close_T1, vol20, idx_T, bars in decisions:
            vol_by_T[T].append(vol20)
        vol_thresh = {}
        for T, vols in vol_by_T.items():
            vols_sorted = sorted(vols)
            idx90 = int(0.9 * len(vols_sorted))
            vol_thresh[T] = vols_sorted[idx90]

        # Filter and remove duplicates
        last_call = {}
        filtered = []
        for sym, T, close_T1, vol20, idx_T, bars in decisions:
            if vol20 > vol_thresh[T]:
                continue
            if sym in last_call:
                last_T = last_call[sym]
                # Count trading days between last_T and T
                cnt = 0
                for ts, _, _ in bars:
                    if last_T < ts < T:
                        cnt += 1
                        if cnt >= 20:
                            break
                if cnt < 20:
                    continue
            filtered.append((sym, T))
            last_call[sym] = T

        if len(filtered) < 30:
            print("INSUFFICIENT=1")
            return

        # Attach labels
        labeled = []
        for sym, T in filtered:
            key = (sym, T)
            if key in label_map:
                labeled.append((sym, T, label_map[key]))

        if len(labeled) < 30:
            print("INSUFFICIENT=1")
            return

        # Sort by time
        labeled.sort(key=lambda x: x[1])
        split_idx = int(0.8 * len(labeled))
        non_sealed = labeled[:split_idx]
        sealed = labeled[split_idx:]

        # Issue calls for non_sealed (all qualify by construction)
        issued = non_sealed
        hits = sum(1 for _, _, up in issued if up)
        issued_count = len(issued)
        opportunities = len(non_sealed)
        precision = hits / issued_count if issued_count else 0.0
        base_rate = precision  # base rate in issued subset is same as precision
        distinct_days = len({datetime.utcfromtimestamp(T).date() for _, T, _ in issued})

        # Design effect: cluster by day
        day_calls = defaultdict(int)
        for _, T, _ in issued:
            day_calls[datetime.utcfromtimestamp(T).date()] += 1
        if not day_calls:
            print("INSUFFICIENT=1")
            return
        avg_cluster = sum(day_calls.values()) / len(day_calls)
        # Estimate ICC from binary outcomes within days
        day_hits = defaultdict(int)
        day_total = defaultdict(int)
        for _, T, up in issued:
            d = datetime.utcfromtimestamp(T).date()
            day_total[d] += 1
            if up:
                day_hits[d] += 1
        p_overall = hits / issued_count
        # Compute variance between days
        var_between = 0.0
        for d, total in day_total.items():
            p_d = day_hits[d] / total
            var_between += (p_d - p_overall) ** 2
        if len(day_total) > 1:
            var_between /= (len(day_total) - 1)
        else:
            var_between = 0.0
        # Compute variance within days
        var_within = p_overall * (1 - p_overall)
        # ICC estimate
        if avg_cluster > 1:
            icc = var_between / (var_between + var_within) if var_within > 0 else 0.0
        else:
            icc = 0.0
        icc = max(0.0, min(icc, 1.0))
        design_effect = 1 + (avg_cluster - 1) * icc
        if design_effect <= 0:
            design_effect = 1.0
        effective_n = issued_count / design_effect

        # Sealed precision
        sealed_hits = sum(1 for _, _, up in sealed if up)
        sealed_prec = sealed_hits / len(sealed) if sealed else 0.0

        print(f"ISSUED={issued_count}")
        print(f"OPPORTUNITIES={opportunities}")
        print(f"PRECISION={precision}")
        print(f"BASE_RATE={base_rate}")
        print(f"DISTINCT_DAYS={distinct_days}")
        print(f"EFFECTIVE_N={effective_n}")
        print(f"SEALED_PRECISION={sealed_prec}")

    except Exception:
        print("INSUFFICIENT=1")
    finally:
        conn.close()

if __name__ == "__main__":
    main()