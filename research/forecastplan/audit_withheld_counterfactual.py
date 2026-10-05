#!/usr/bin/env python3
"""
Counterfactual on SignalDeck prediction rows that were never pre-registered for grading.
This script answers whether the publication gate discards informative days, not whether to publish.

Outcome rule (exact replication):
  H1D = 86400 ; DST_SLACK = 6*3600
  base   = last 1d bar with bar.ts <= prediction.ts
           (if none -> "no_base"; if base.close <= 0 -> "bad_base")
  target = base.ts + H1D - DST_SLACK
  fwd    = first 1d bar with bar.ts >= target
           (if none -> "no_fwd")
  if fwd.ts - target > 3*H1D -> status "hole"
  SETTLED ONLY: a bar strictly after fwd must exist, else status "unsettled"
  ret    = fwd.close / base.close - 1 ; up = ret > 0 ; status "ok"
"""

import sqlite3
import sys
import bisect
import math
import statistics

H1D = 86400
DST_SLACK = 6 * 3600
# Ceiling on DIRECTION disagreement with the Go-resolved record. Deliberately not
# zero: bars are revised after an outcome is stored, so a recomputed label drifts
# on a minority of rows. Measured 1.98% on 2026-09-19.
PARITY_MAX_DIRECTION_PCT = 3.0

def load_bars(con, symbol_ids):
    """Return dict symbol_id -> (ts_list, close_list) for tf='1d'."""
    bars = {}
    if not symbol_ids:
        return bars
    chunk_size = 800
    ids = list(symbol_ids)
    for i in range(0, len(ids), chunk_size):
        chunk = ids[i:i+chunk_size]
        placeholders = ','.join('?' for _ in chunk)
        cur = con.execute(
            f"SELECT symbol_id, ts, close FROM bars "
            f"WHERE tf='1d' AND symbol_id IN ({placeholders}) "
            f"ORDER BY symbol_id, ts",
            chunk
        )
        for symbol_id, ts, close in cur:
            if symbol_id not in bars:
                bars[symbol_id] = ([], [])
            bars[symbol_id][0].append(ts)
            bars[symbol_id][1].append(close)
    return bars

def resolve(bars, sid, pts):
    """Implement the outcome rule. Returns (ret, up, status)."""
    ts_close = bars.get(sid)
    if ts_close is None:
        return (None, None, "no_bars")
    ts_list, close_list = ts_close
    idx = bisect.bisect_right(ts_list, pts) - 1
    if idx < 0:
        return (None, None, "no_base")
    base_ts = ts_list[idx]
    base_close = close_list[idx]
    if base_close is None or base_close <= 0:
        return (None, None, "bad_base")
    target = base_ts + H1D - DST_SLACK
    idx2 = bisect.bisect_left(ts_list, target)
    if idx2 >= len(ts_list):
        return (None, None, "no_fwd")
    fwd_ts = ts_list[idx2]
    fwd_close = close_list[idx2]
    if fwd_ts - target > 3 * H1D:
        return (None, None, "hole")
    if idx2 + 1 >= len(ts_list):
        return (None, None, "unsettled")
    ret = fwd_close / base_close - 1.0
    up = ret > 0.0
    return (ret, up, "ok")

def within_day_auc(probs, ups):
    """Mann-Whitney AUC with tie correction (average ranks)."""
    n1 = sum(ups)
    n0 = len(probs) - n1
    if n1 == 0 or n0 == 0:
        return None
    paired = list(zip(probs, ups))
    paired.sort(key=lambda x: x[0])
    sum_ranks_pos = 0
    rank = 1
    i = 0
    n = len(paired)
    while i < n:
        j = i
        while j < n and paired[j][0] == paired[i][0]:
            j += 1
        g = j - i
        avg_rank = rank + (g - 1) / 2.0
        if paired[i][1] == 1:
            sum_ranks_pos += g * avg_rank
        rank += g
        i = j
    auc = (sum_ranks_pos - n1 * (n1 + 1) / 2.0) / (n1 * n0)
    return auc

def quantile(sorted_list, q):
    """Linear interpolation quantile, 0<=q<=1."""
    n = len(sorted_list)
    if n == 0:
        return None
    if q <= 0.0:
        return sorted_list[0]
    if q >= 1.0:
        return sorted_list[-1]
    idx = q * (n - 1)
    low = int(math.floor(idx))
    high = int(math.ceil(idx))
    if low == high:
        return sorted_list[low]
    return sorted_list[low] + (sorted_list[high] - sorted_list[low]) * (idx - low)

def main():
    DB = "data/signaldeck.db"
    con = sqlite3.connect("file:" + DB + "?mode=ro", uri=True, timeout=60)
    con.execute("PRAGMA busy_timeout=60000")

    # ---------- STEP A: PARITY ----------
    cur = con.execute(
        "SELECT symbol_id, ts, fwd_return FROM prediction_outcomes "
        "WHERE horizon='1d' AND fwd_return IS NOT NULL ORDER BY ts DESC LIMIT 4000"
    )
    rows = cur.fetchall()
    symbol_ids = set(r[0] for r in rows)
    bars = load_bars(con, symbol_ids)

    comparable = 0
    mismatches = 0
    sign_mismatches = 0
    max_diff = 0.0
    for symbol_id, ts, fwd_ret in rows:
        ret, up, status = resolve(bars, symbol_id, ts)
        if status == "ok":
            comparable += 1
            diff = abs(ret - fwd_ret)
            if diff > max_diff:
                max_diff = diff
            if diff > 1e-9:
                mismatches += 1
            if (1 if ret > 0 else 0) != (1 if fwd_ret > 0 else 0):
                sign_mismatches += 1
    if comparable < 500:
        print("PARITY INCONCLUSIVE: %d comparable rows" % comparable)
        return 2
    vpct = 100.0 * mismatches / comparable
    spct = 100.0 * sign_mismatches / comparable
    print("PARITY: %d rows compared, %d VALUE mismatches (%.2f%%), max abs diff %.3e" %
          (comparable, mismatches, vpct, max_diff))
    print("PARITY: %d DIRECTION mismatches (%.2f%%) -- the property this "
          "counterfactual counts" % (sign_mismatches, spct))
    # The RULE is reproduced; the residual is DATA, not logic. Stored outcomes were
    # computed against bars as they stood at resolution time, and bars are revised
    # afterwards (corrections, split repair), so recomputing from today's bars
    # drifts on a minority of rows. Measured 2026-09-19: every value mismatch came
    # from 9 symbols of 115 sampled, all sharing one prediction ts, and the stored
    # settle_ts equalled the base bar this code picks -- the base agrees, the
    # forward CLOSE moved.
    #
    # Gate on DIRECTION, because direction is what is counted below. State the
    # residual instead of hiding it: a ~2% label error is the SAME ORDER as the
    # effect being looked for, so every accuracy and AUC below is an
    # order-of-magnitude read and NEVER evidence.
    if spct > PARITY_MAX_DIRECTION_PCT:
        print("PARITY FAILED -- direction disagrees on %.2f%% of rows, above the "
              "%.1f%% ceiling. Nothing below would be trustworthy." %
              (spct, PARITY_MAX_DIRECTION_PCT))
        return 1

    # ---------- STEP B: COUNTERFACTUAL ----------
    cur = con.execute(
        "WITH dedup AS ("
        "  SELECT symbol_id, ts, cal_prob, date(ts,'unixepoch') d,"
        "         ROW_NUMBER() OVER (PARTITION BY symbol_id, date(ts,'unixepoch')"
        "                            ORDER BY ts DESC) rn"
        "  FROM predictions"
        "  WHERE horizon='1d' AND n_used=0 AND raw_prob<>0.5"
        "    AND date(ts,'unixepoch') BETWEEN '2026-09-10' AND '2026-09-18')"
        "SELECT d, symbol_id, ts, cal_prob FROM dedup WHERE rn=1"
    )
    withheld = cur.fetchall()
    con.close()

    loaded_symbols = set(bars.keys())
    needed_symbols = set(row[1] for row in withheld) - loaded_symbols
    if needed_symbols:
        con = sqlite3.connect("file:" + DB + "?mode=ro", uri=True, timeout=60)
        con.execute("PRAGMA busy_timeout=60000")
        more_bars = load_bars(con, needed_symbols)
        bars.update(more_bars)
        con.close()

    by_day = {}
    for d, symbol_id, ts, cal_prob in withheld:
        by_day.setdefault(d, []).append((symbol_id, ts, cal_prob))

    days_sorted = sorted(by_day.keys())
    print("%-10s %6s %9s %6s %8s %8s %8s" %
          ("day", "n", "resolved", "acc", "up_rate", "AUC_in", "spread"))
    aucs = []
    total_resolved = 0
    total_correct = 0
    total_ups = 0
    for day in days_sorted:
        rows = by_day[day]
        n = len(rows)
        resolved_list = []
        for (symbol_id, ts, cal_prob) in rows:
            ret, up, status = resolve(bars, symbol_id, ts)
            if status == "ok":
                resolved_list.append((cal_prob, up))
        resolved = len(resolved_list)
        if resolved == 0:
            acc_str = "-"
            up_rate_str = "-"
            auc_str = "-"
            spread_str = "-"
        else:
            correct = sum(1 for prob, up in resolved_list if (prob >= 0.5) == (up == 1))
            acc = correct / resolved
            acc_str = "%.4f" % acc
            ups = sum(up for _, up in resolved_list)
            up_rate = ups / resolved
            up_rate_str = "%.4f" % up_rate
            total_resolved += resolved
            total_correct += correct
            total_ups += ups
            probs = [prob for prob, _ in resolved_list]
            ups_list = [up for _, up in resolved_list]
            auc_in = within_day_auc(probs, ups_list)
            if auc_in is None:
                auc_str = "-"
            else:
                auc_str = "%.4f" % auc_in
                aucs.append(auc_in)
            sorted_probs = sorted(probs)
            q95 = quantile(sorted_probs, 0.95)
            q05 = quantile(sorted_probs, 0.05)
            spread = q95 - q05
            spread_str = "%.4f" % spread
        print("%-10s %6d %9d %6s %8s %8s %8s" %
              (day, n, resolved, acc_str, up_rate_str, auc_str, spread_str))

    if total_resolved > 0:
        pooled_acc = total_correct / total_resolved
        pooled_up_rate = total_ups / total_resolved
    else:
        pooled_acc = 0.0
        pooled_up_rate = 0.0
    print("POOLED: days=%d n=%d acc=%.4f null=%.4f" %
          (len(days_sorted), total_resolved, pooled_acc, pooled_up_rate))

    if aucs:
        mean_auc = statistics.mean(aucs)
        if len(aucs) >= 2:
            sd_auc = statistics.stdev(aucs)
        else:
            sd_auc = 0.0
        se = sd_auc / math.sqrt(len(aucs))
        t = (mean_auc - 0.5) / se if se != 0 else 0.0
        print("AUC_MEAN: mean=%.4f sd=%.4f days=%d t=%.4f" %
              (mean_auc, sd_auc, len(aucs), t))
        smallest_detectable = 0.5 + 2.80 * sd_auc / math.sqrt(len(aucs))
        if smallest_detectable < 0.5:
            smallest_detectable = 0.5
        print("POWER: smallest detectable AUC at 80%% power = %.4f; smaller is UNRESOLVED, not absent" %
              smallest_detectable)
    else:
        print("AUC_MEAN: mean=None sd=None days=0 t=None")
        print("POWER: smallest detectable AUC at 80% power = 0.5000; smaller is UNRESOLVED, not absent")

    print("Reminder: these rows were never pre-registered for grading. This answers")
    print("whether the gate discards informative days, NOT whether to publish.")
    print("COUNTERFACTUAL OK")
    return 0

if __name__ == "__main__":
    sys.exit(main())