"""Pick the config on SEARCH-period evidence only, then freeze it to JSON.

Selection rule is fixed before looking: highest search Sharpe among configs
whose Newey-West t clears 2.0 and whose max drawdown is no worse than -35%.
Ranking on Sharpe alone would happily pick a config that made its return in one
quarter and then bled.
"""
import json, pandas as pd
d = pd.read_csv("ablation.csv")
d = d[d.sharpe.notna()]
elig = d[(d.t_nw.abs() >= 2.0) & (d.max_dd >= -0.35)]
pool, rule = (elig, "t_NW>=2 and maxDD>=-35%") if len(elig) else (d, "FALLBACK: no config cleared the filter")
b = pool.sort_values("sharpe", ascending=False).iloc[0]
cfg = dict(h=int(b.h), target="extremes10", model="ens_lh", k=int(b.k),
           weighting=str(b.weighting), beta_neutral=bool(b.beta_neutral),
           vol_target=float(b.vol_target) or 0.0)
json.dump(cfg, open("best_config.json", "w"), indent=1)
print("selection rule:", rule, "| eligible %d of %d" % (len(elig), len(d)))
print("chosen:", json.dumps(cfg))
print("search: Sharpe %.2f  ann %+.2f%%  vol %.2f%%  maxDD %.1f%%  t_NW %.2f  worst day %.2f%%"
      % (b.sharpe, 100*b.ann_ret, 100*b.ann_vol, 100*b.max_dd, b.t_nw, 100*b.worst_day))
