import argparse
import json
import numpy as np
from collections import defaultdict

import rv_forecast_backtest as bt


def hansen_spa(per_day_losses, h):
    """
    Attack 1: Hansen's SPA test for multiple comparisons.
    Tests whether the HAR model is outperformed by any of the three null models
    (RW, EWMA, FLAT) after accounting for the fact that three models were tried.
    A high p-value indicates that we cannot reject the null hypothesis that
    HAR is not outperformed by any of the three models.
    """
    try:
        from arch.bootstrap import SPA
        benchmark = np.array(per_day_losses["har"])
        models = np.column_stack((
            per_day_losses["rw"],
            per_day_losses["ewma"],
            per_day_losses["flat"]
        ))
        spa = SPA(benchmark, models, reps=5000, bootstrap="stationary", seed=20260903)
        spa.compute()
        pvalues = spa.pvalues

        # THE REVERSED TEST IS THE ONE THAT SUPPORTS THE CLAIM.
        #
        # SPA(benchmark=HAR, models=nulls) tests "is HAR beaten by anything?".
        # A high p there only says nothing beat HAR, which is weaker than what
        # is being asserted. To claim HAR BEATS the null that matters, the
        # benchmark has to be that null and HAR the challenger: a LOW p then
        # says EWMA is outperformed, with the multiplicity of the comparison
        # charged by the bootstrap rather than assumed away.
        rev = SPA(np.array(per_day_losses["ewma"]),
                  np.array(per_day_losses["har"]).reshape(-1, 1),
                  reps=5000, bootstrap="stationary", seed=20260903)
        rev.compute()
        rp = rev.pvalues

        return {
            "pvalues": {
                "lower": float(pvalues['lower']),
                "consistent": float(pvalues['consistent']),
                "upper": float(pvalues['upper'])
            },
            "reversed_benchmark_ewma": {
                "lower": float(rp['lower']),
                "consistent": float(rp['consistent']),
                "upper": float(rp['upper']),
                "meaning": ("benchmark = EWMA, challenger = HAR. A LOW p-value "
                            "means EWMA is outperformed by HAR. This is the "
                            "direction that supports the headline claim."),
            },
            "reps": 5000,
            "interpretation": "A high p-value (above 0.05) indicates that we cannot reject the null hypothesis that HAR is not outperformed by any of the three models, after accounting for multiple comparisons. A low p-value suggests that at least one of the null models outperforms HAR."
        }
    except Exception as e:
        return {"error": str(e)}


def refit_sensitivity(con, symbols, h, cadences=(5, 21, 63)):
    """
    Attack 2: Sensitivity to refitting frequency.
    Tests whether the HAR's advantage over EWMA depends on refitting often.
    If the advantage disappears with infrequent refitting, it may be tracking noise.
    """
    original_refit = bt.REFIT_EVERY
    results = []
    try:
        # Precompute symbol-specific data independent of REFIT_EVERY and h
        symbol_data = {}
        for symbol_id in symbols:
            bars = bt.bars_for(con, symbol_id)
            ts_list, rv_list, _ = bt.rv_series(bars)
            symbol_data[symbol_id] = (ts_list, rv_list)
        
        for cadence in cadences:
            bt.REFIT_EVERY = cadence
            try:
                day_to_har = defaultdict(list)
                day_to_ewma = defaultdict(list)
                for symbol_id, (ts_list, rv_list) in symbol_data.items():
                    records = bt.walk_symbol(ts_list, rv_list, h)
                    for record in records:
                        day = bt.day_key(record['ts'])
                        actual = record['actual']
                        har_loss = bt.qlike(actual, record['har'])
                        ewma_loss = bt.qlike(actual, record['ewma'])
                        if har_loss is not None:
                            day_to_har[day].append(har_loss)
                        if ewma_loss is not None:
                            day_to_ewma[day].append(ewma_loss)
                # Days with data for both models
                days = sorted(set(day_to_har.keys()) & set(day_to_ewma.keys()))
                if len(days) < 2:
                    results.append({
                        "refit_every": cadence,
                        "days": len(days),
                        "mean": None,
                        "t": None
                    })
                    continue
                har_losses = [np.mean(day_to_har[d]) for d in days]
                ewma_losses = [np.mean(day_to_ewma[d]) for d in days]
                d = [h - e for h, e in zip(har_losses, ewma_losses)]
                n = len(d)
                lag = max(bt.newey_west_lag(n), h)
                dm_result = bt.diebold_mariano(d, lag)
                if dm_result is None:
                    mean_val = None
                    t_val = None
                else:
                    mean_val = dm_result['mean']
                    t_val = dm_result['t']
                results.append({
                    "refit_every": cadence,
                    "days": n,
                    "mean": mean_val,
                    "t": t_val
                })
            finally:
                bt.REFIT_EVERY = original_refit
        interpretation = "If HAR's advantage over EWMA diminishes with infrequent refitting, the original result may be driven by overfitting to recent noise rather than a stable relationship."
        return {"cadences": results, "interpretation": interpretation}
    except Exception as e:
        bt.REFIT_EVERY = original_refit
        return {"error": str(e)}


def symbol_clustered(records_by_symbol, h):
    """
    Attack 3: Symbol clustering.
    Tests whether the HAR's advantage is driven by a few symbols.
    If dropping the top 5 extreme symbols changes the conclusion, the result is fragile.
    """
    try:
        per_symbol_means = []
        symbol_means = {}
        for symbol_id, records in records_by_symbol.items():
            diffs = []
            for record in records:
                actual = record['actual']
                har_loss = bt.qlike(actual, record['har'])
                ewma_loss = bt.qlike(actual, record['ewma'])
                if har_loss is None or ewma_loss is None:
                    continue
                diffs.append(har_loss - ewma_loss)
            if diffs:
                mean_diff = np.mean(diffs)
                per_symbol_means.append(mean_diff)
                symbol_means[symbol_id] = mean_diff
        n_symbols = len(per_symbol_means)
        if n_symbols == 0:
            stats = {
                "n_symbols": 0,
                "share_negative": 0.0,
                "mean": 0.0,
                "median": 0.0,
                "t_stat": None
            }
        else:
            share_neg = np.mean([x < 0 for x in per_symbol_means])
            mean_val = np.mean(per_symbol_means)
            median_val = np.median(per_symbol_means)
            if n_symbols > 1:
                std_val = np.std(per_symbol_means, ddof=1)
                if std_val > 0:
                    t_stat = mean_val / (std_val / np.sqrt(n_symbols))
                else:
                    t_stat = 0.0 if mean_val == 0 else np.inf * np.sign(mean_val)
            else:
                t_stat = None
            stats = {
                "n_symbols": n_symbols,
                "share_negative": float(share_neg),
                "mean": float(mean_val),
                "median": float(median_val),
                "t_stat": float(t_stat) if t_stat is not None else None
            }
        # Top 5 symbols by absolute mean
        if n_symbols > 0:
            sorted_symbols = sorted(symbol_means.items(), key=lambda x: abs(x[1]), reverse=True)
            top5 = sorted_symbols[:5]
            top5_ids = [sid for sid, _ in top5]
            # Recompute without top 5
            remaining_means = [m for sid, m in sorted_symbols[5:]]
            n_remaining = len(remaining_means)
            if n_remaining == 0:
                stats_remaining = {
                    "n_symbols": 0,
                    "share_negative": 0.0,
                    "mean": 0.0,
                    "median": 0.0,
                    "t_stat": None
                }
            else:
                share_neg_rem = np.mean([x < 0 for x in remaining_means])
                mean_rem = np.mean(remaining_means)
                median_rem = np.median(remaining_means)
                if n_remaining > 1:
                    std_rem = np.std(remaining_means, ddof=1)
                    if std_rem > 0:
                        t_stat_rem = mean_rem / (std_rem / np.sqrt(n_remaining))
                    else:
                        t_stat_rem = 0.0 if mean_rem == 0 else np.inf * np.sign(mean_rem)
                else:
                    t_stat_rem = None
                stats_remaining = {
                    "n_symbols": n_remaining,
                    "share_negative": float(share_neg_rem),
                    "mean": float(mean_rem),
                    "median": float(median_rem),
                    "t_stat": float(t_stat_rem) if t_stat_rem is not None else None
                }
        else:
            top5_ids = []
            stats_remaining = stats
        return {
            "n_symbols": stats["n_symbols"],
            "share_negative": stats["share_negative"],
            "mean": stats["mean"],
            "median": stats["median"],
            "t_stat": stats["t_stat"],
            "top5_abs": top5_ids,
            "remaining": stats_remaining,
            "interpretation": "If dropping the 5 most extreme symbols changes the conclusion (e.g., mean becomes positive or t-stat loses significance), the original result is driven by a few symbols."
        }
    except Exception as e:
        return {"error": str(e)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--db', default='data/signaldeck.db')
    parser.add_argument('--limit', type=int, default=150)
    parser.add_argument('--companies-only', action='store_true')
    parser.add_argument('--json', default='tools/rv_adversarial_result.json')
    args = parser.parse_args()
    # Hardcode companies_only=True as per instruction
    companies_only = True
    
    con = bt.open_db(args.db)
    symbols = bt.universe(con, args.limit, companies_only)
    
    result = {}
    for h in bt.HORIZONS:
        # Precompute symbol-specific data for this horizon (independent of REFIT_EVERY)
        symbol_data = {}
        for symbol_id in symbols:
            bars = bt.bars_for(con, symbol_id)
            ts_list, rv_list, _ = bt.rv_series(bars)
            symbol_data[symbol_id] = (ts_list, rv_list)
        
        # Compute records for original REFIT_EVERY (needed for attack1 and attack3)
        records_by_symbol = {}
        per_day_losses = {
            'har': defaultdict(list),
            'rw': defaultdict(list),
            'ewma': defaultdict(list),
            'flat': defaultdict(list)
        }
        for symbol_id, (ts_list, rv_list) in symbol_data.items():
            records = bt.walk_symbol(ts_list, rv_list, h)
            records_by_symbol[symbol_id] = records
            for record in records:
                day = bt.day_key(record['ts'])
                actual = record['actual']
                for model in ['har', 'rw', 'ewma', 'flat']:
                    forecast = record[model]
                    loss = bt.qlike(actual, forecast)
                    if loss is not None:
                        per_day_losses[model][day].append(loss)
        # Days with data for all four models
        all_days = set(per_day_losses['har'].keys())
        for m in ['rw', 'ewma', 'flat']:
            all_days &= set(per_day_losses[m].keys())
        sorted_days = sorted(all_days)
        per_day_losses_final = {}
        for model in ['har', 'rw', 'ewma', 'flat']:
            per_day_losses_final[model] = [
                np.mean(per_day_losses[model][day]) for day in sorted_days
            ]
        
        # Run attacks
        attack1 = hansen_spa(per_day_losses_final, h)
        attack2 = refit_sensitivity(con, symbols, h)
        attack3 = symbol_clustered(records_by_symbol, h)
        
        result[h] = {
            "hansen_spa": attack1,
            "refit_sensitivity": attack2,
            "symbol_clustered": attack3
        }
    
    with open(args.json, 'w') as f:
        json.dump(result, f, indent=2)
    
    # Print summary
    print(f"Adversarial results written to {args.json}")
    for h in bt.HORIZONS:
        print(f"\nHorizon {h}:")
        spa = result[h]['hansen_spa']
        if 'error' in spa:
            print(f"  SPA: ERROR - {spa['error']}")
        else:
            p = spa['pvalues']
            print(f"  SPA benchmark=HAR  (is HAR beaten?)      consistent p={p['consistent']:.4f}"
                  f"   [high = nothing beat HAR]")
            rv = spa_res.get('reversed_benchmark_ewma')
            if rv:
                # The direction that supports the claim. Printed because a
                # result that only exists in a JSON file is a result nobody
                # reads.
                print(f"  SPA benchmark=EWMA (does HAR beat it?)   consistent p={rv['consistent']:.4f}"
                      f"   [LOW = HAR outperforms EWMA]")
        sens = result[h]['refit_sensitivity']
        if 'error' in sens:
            print(f"  Refit sensitivity: ERROR - {sens['error']}")
        else:
            for c in sens['cadences']:
                print(f"  Refit every {c['refit_every']:2d}: days={c['days']:3d}, mean={c['mean']:.6f}, t={c['t']:.4f}")
        clust = result[h]['symbol_clustered']
        if 'error' in clust:
            print(f"  Symbol clustering: ERROR - {clust['error']}")
        else:
            print(f"  Symbols: {clust['n_symbols']}, share negative: {clust['share_negative']:.2%}")
            print(f"  Mean diff: {clust['mean']:.6f}, median: {clust['median']:.6f}, t: {clust['t_stat']:.4f}")
            print(f"  Top 5 extreme symbols: {clust['top5_abs']}")
            rem = clust['remaining']
            if rem.get('t_stat') is None:
                print(f"  After removing top 5: {rem['n_symbols']} symbols - too few to compute a t")
            else:
                print(f"  After removing top 5: {rem['n_symbols']} symbols, "
                      f"mean diff: {rem['mean']:.6f}, t: {rem['t_stat']:.4f}")
    print("\nADVERSARIAL CHECKS COMPLETE")


if __name__ == '__main__':
    main()