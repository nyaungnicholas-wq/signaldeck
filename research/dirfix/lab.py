"""Walk-forward equity direction-prediction study.

Every design choice here exists because this repo already shipped the opposite:
  - features are strictly trailing            (lookahead)
  - labels keep NaN, never coerce to "down"   (silent label corruption)
  - folds embargo `horizon` days              (label-overlap leakage)
  - one row per (symbol, day)                 (pseudo-replication)
  - intervals are day-clustered bootstraps    (naive binomial CIs)
  - the null is prequential-majority          (hindsight-oracle nulls)
"""
import numpy as np
import pandas as pd
from sklearn.ensemble import HistGradientBoostingClassifier
from sklearn.linear_model import LogisticRegression
from sklearn.impute import SimpleImputer


def load(path="panel.parquet") -> dict:
    df = pd.read_parquet(path)
    meta = df[["symbol_id", "symbol", "market", "delisted_at"]].drop_duplicates("symbol_id")
    pivot = lambda c: df.pivot(index="day", columns="symbol_id", values=c).sort_index()
    return {"close": pivot("close"), "high": pivot("high"), "low": pivot("low"),
            "open": pivot("open"), "volume": pivot("volume"), "meta": meta}


def make_features(px, cfg=None) -> pd.DataFrame:
    close, high, low, volume = px["close"], px["high"], px["low"], px["volume"]
    idx, cols = close.index, close.columns
    wide = lambda s: pd.DataFrame(np.repeat(np.asarray(s, float)[:, None], len(cols), axis=1),
                                  index=idx, columns=cols)

    log_ret = np.log(close).diff()
    dollar_vol = close * volume
    log_dv = np.log(dollar_vol.where(dollar_vol > 0))
    feats = {}

    # momentum -- trailing, shifted so day t uses information through t-1
    for h in (1, 2, 3, 5, 10, 21, 63, 126, 252):
        feats[f"mom_{h}"] = close.pct_change(h).shift(1)

    # short-term reversal, ranked across the day's cross-section
    for h in (1, 5):
        feats[f"rev_{h}"] = close.pct_change(h).shift(1).rank(axis=1, pct=True)

    # volatility
    for w in (5, 21, 63):
        feats[f"vol_{w}"] = log_ret.rolling(w).std().shift(1)
    feats["vol_ratio_21_63"] = feats["vol_21"] / feats["vol_63"].where(feats["vol_63"] > 0)

    # volume
    feats["log_dollar_vol_21"] = log_dv.rolling(21).mean().shift(1)
    dv_m = log_dv.rolling(63).mean().shift(1)
    dv_s = log_dv.rolling(63).std().shift(1)
    feats["dollar_vol_z_63"] = (log_dv.shift(1) - dv_m) / dv_s.where(dv_s > 0)
    feats["dollar_vol_ratio_5_21"] = (log_dv.rolling(5).mean().shift(1)
                                      / feats["log_dollar_vol_21"].where(feats["log_dollar_vol_21"] != 0))

    # range position within the trailing 21d channel
    lo21 = low.rolling(21).min().shift(1)
    hi21 = high.rolling(21).max().shift(1)
    feats["range_pos_21"] = (close.shift(1) - lo21) / (hi21 - lo21).where(hi21 > lo21)

    # true range -- ELEMENTWISE max of three day x symbol frames. A
    # concat(...).max(axis=1) here silently collapses to one value per day.
    prev_close = close.shift(1)
    tr = pd.DataFrame(np.maximum.reduce([(high - low).to_numpy(float),
                                         (high - prev_close).abs().to_numpy(float),
                                         (low - prev_close).abs().to_numpy(float)]),
                      index=idx, columns=cols)
    feats["tr_close_14"] = (tr / close).rolling(14).mean().shift(1)

    # distance from trailing MA in units of the symbol's own vol. Must be a
    # RETURN over a return-vol; a price difference over a log-return vol is
    # dimensionally wrong and makes the feature price-level dependent.
    for w in (21, 63):
        ma = close.rolling(w).mean().shift(1)
        v = feats["vol_21"]
        feats[f"dist_ma_{w}"] = (close.shift(1) / ma - 1.0) / v.where(v > 0)

    # market context -- cross-sectional at day t, known at t's close
    daily_ret = close.pct_change().shift(1)
    mkt = daily_ret.mean(axis=1)
    feats["mkt_mean_ret"] = wide(mkt)
    feats["mkt_mean_ret_5"] = wide(mkt.rolling(5).mean())
    feats["mkt_mean_ret_21"] = wide(mkt.rolling(21).mean())
    feats["mkt_dispersion"] = wide(daily_ret.std(axis=1))
    feats["mkt_breadth"] = wide((daily_ret > 0).sum(axis=1) / daily_ret.notna().sum(axis=1))

    # cross-sectional ranks
    for c in ("mom_21", "mom_5", "vol_21", "log_dollar_vol_21"):
        feats[f"cs_rank_{c}"] = feats[c].rank(axis=1, pct=True)

    out = pd.concat(feats, axis=1).replace([np.inf, -np.inf], np.nan)
    out = out.stack()                      # -> index (day, symbol_id); no swaplevel
    out.index.names = ["day", "symbol_id"]
    return out.sort_index()


def make_features_v2(px, cfg=None) -> pd.DataFrame:
    """Base features plus the classical cross-sectional anomalies.

    These are PRE-REGISTERED known predictors (momentum 12-1, idiosyncratic
    vol, beta, Amihud illiquidity, the MAX lottery effect, 52-week-high
    proximity, return skew), not data-mined ones -- adding them is a genuine
    attempt at signal, not another draw from the search distribution.
    """
    close, high, low, volume = px["close"], px["high"], px["low"], px["volume"]
    idx, cols = close.index, close.columns
    r = close.pct_change()
    rm = r.mean(axis=1)                       # equal-weight market, known at t
    rm_w = pd.DataFrame(np.repeat(rm.to_numpy(float)[:, None], len(cols), axis=1),
                        index=idx, columns=cols)
    dollar_vol = close * volume
    feats = {}

    # momentum 12-1: skips the most recent month. mom_252 INCLUDES it, which
    # mixes the momentum premium with the 1-month reversal that opposes it.
    feats["mom_12_1"] = (close.shift(21) / close.shift(252) - 1.0).shift(1)
    feats["mom_6_1"] = (close.shift(21) / close.shift(126) - 1.0).shift(1)

    # one-factor beta and idiosyncratic vol over a trailing year / quarter
    for w, tag in ((252, "252"), (63, "63")):
        cov = (r * rm_w).rolling(w).mean() - r.rolling(w).mean() * rm_w.rolling(w).mean()
        var_m = rm_w.rolling(w).var()
        beta = cov / var_m.where(var_m > 0)
        feats[f"beta_{tag}"] = beta.shift(1)
        resid_var = r.rolling(w).var() - (beta ** 2) * var_m
        feats[f"idiovol_{tag}"] = np.sqrt(resid_var.clip(lower=0)).shift(1)

    # Amihud illiquidity: |return| per dollar traded
    feats["amihud_21"] = np.log1p((r.abs() / dollar_vol.where(dollar_vol > 0))
                                  .rolling(21).mean() * 1e9).shift(1)

    # MAX effect -- lottery demand; the best single day in the past month is a
    # robust NEGATIVE predictor of the next
    feats["max_ret_21"] = r.rolling(21).max().shift(1)
    feats["min_ret_21"] = r.rolling(21).min().shift(1)
    feats["skew_63"] = r.rolling(63).skew().shift(1)

    # proximity to the 52-week high
    feats["near_52w_high"] = (close / close.rolling(252).max()).shift(1)
    feats["near_52w_low"] = (close / close.rolling(252).min()).shift(1)

    # turnover shock
    tv = volume.rolling(5).mean() / volume.rolling(63).mean()
    feats["turnover_shock"] = np.log(tv.where(tv > 0)).shift(1)

    for c in ("mom_12_1", "idiovol_63", "amihud_21", "max_ret_21", "near_52w_high"):
        feats[f"cs_rank_{c}"] = feats[c].rank(axis=1, pct=True)

    extra = pd.concat(feats, axis=1).replace([np.inf, -np.inf], np.nan).stack()
    extra.index.names = ["day", "symbol_id"]
    base = make_features(px, cfg)
    return base.join(extra.sort_index(), how="left")


def make_labels(px, horizon, target) -> pd.Series:
    """1.0/0.0/NaN. NaN is load-bearing: coercing it to 0.0 labels every
    unlisted or unresolved symbol-day as a real DOWN move."""
    close = px["close"]
    fwd = (close.shift(-horizon) / close - 1.0).stack()
    fwd.index.names = ["day", "symbol_id"]
    ok = fwd.notna()

    if target == "absolute":
        return (fwd > 0).astype(float).where(ok)

    g = fwd.groupby(level="day")
    if target == "relative":
        cut = g.transform("median")
        y = (fwd > cut).astype(float).where(ok & cut.notna())
    elif target.startswith("extremes"):
        # top q% vs bottom q%, middle withheld -- a separable problem with a 0.5
        # null, which is where a real 60% can live. "extremes" == extremes30.
        # A narrower q pushes the classes further apart and raises attainable
        # accuracy, but narrows what is being predicted: "which tail", given
        # that the move is a tail move. That must be disclosed, not buried.
        q = int(target[8:]) / 100.0 if target[8:] else 0.30
        hi = g.transform("quantile", 1.0 - q)
        lo = g.transform("quantile", q)
        y = pd.Series(np.nan, index=fwd.index)
        y[ok & (fwd >= hi)] = 1.0
        y[ok & (fwd <= lo)] = 0.0
    else:
        raise ValueError(f"unknown target: {target}")

    return y.where(g.transform("count") >= 20)


def universe_mask(px, cfg=None) -> pd.DataFrame:
    close, volume = px["close"], px["volume"]
    dv = (close * volume).rolling(21).mean()
    m = (close >= 2.0) & (dv >= 1e6) & (close.notna().rolling(252).sum() >= 252)
    return m.shift(1, fill_value=False).astype(bool)


def wf_splits(days, horizon, retrain_every=63, min_train_days=252):
    days = pd.DatetimeIndex(days)
    n = len(days)
    if n < min_train_days + horizon + 1:
        return []
    # start past the embargo, else the very first fold drops below min_train_days
    out, start = [], min_train_days + horizon
    while start + retrain_every <= n:
        end = min(start + retrain_every, n)
        # embargo: a train row at day d resolves at d+horizon, which must not
        # reach the first test day. Drop positions until the calendar gap holds.
        te = start - horizon
        while te > 0 and (days[start] - days[te - 1]).days < horizon:
            te -= 1
        if te < min_train_days:
            break
        out.append((days[:te], days[start:end]))
        start = end
    return out


def walkforward(X, y, horizon, model_fn=None, retrain_every=63, min_train_days=252,
                mask=None, max_train_days=None, y_eval=None) -> pd.DataFrame:
    """y trains the model; y_eval (default y) is what gets scored.

    Passing y_eval matters for tail targets. `extremes10` labels ONLY rows that
    turned out to be decile movers, so scoring against it conditions on a future
    outcome -- live, you pick names without knowing which will be tail movers.
    Train on the clean tail target, grade on a label defined for every row.
    """
    if model_fn is None:
        model_fn = lambda: HistGradientBoostingClassifier(
            max_iter=200, learning_rate=0.06, max_depth=None,
            min_samples_leaf=200, l2_regularization=1.0, random_state=0)

    xdays = X.index.get_level_values("day")
    days = xdays.unique().sort_values()
    splits = wf_splits(days, horizon, retrain_every, min_train_days)

    keep = np.ones(len(X), dtype=bool)
    if mask is not None:                       # stack ONCE, not per fold
        ml = mask.stack()
        ml.index.names = ["day", "symbol_id"]
        keep = ml.reindex(X.index, fill_value=False).fillna(False).to_numpy(bool)

    yv = y.reindex(X.index)
    ye = yv if y_eval is None else y_eval.reindex(X.index)
    train_ok = yv.notna().to_numpy()
    eval_ok = ye.notna().to_numpy()
    folds = []
    for tr_days, te_days in splits:
        tr = xdays.isin(tr_days) & keep & train_ok
        te = xdays.isin(te_days) & keep & eval_ok
        if max_train_days is not None:
            tr = tr & xdays.isin(tr_days[-int(max_train_days):])
        if not tr.any() or not te.any():
            continue
        Xtr, ytr = X[tr], yv[tr]
        Xte, yte = X[te], ye[te]

        imp = SimpleImputer(strategy="median")          # fitted on TRAIN only
        m = model_fn()
        m.fit(imp.fit_transform(Xtr), ytr.to_numpy())
        prob = m.predict_proba(imp.transform(Xte))[:, 1]

        i = Xte.index
        folds.append(pd.DataFrame({"day": i.get_level_values("day"),
                                   "symbol_id": i.get_level_values("symbol_id"),
                                   "prob": prob, "y": yte.to_numpy()}))
    if not folds:
        return pd.DataFrame(columns=["day", "symbol_id", "prob", "y"])
    return pd.concat(folds, ignore_index=True)


def evaluate(pred, block=1) -> dict:
    """block>1 resamples non-overlapping `block`-day groups instead of single
    days. Consecutive days' h-day forward returns overlap by (h-1)/h, so at
    h=21 a day-clustered bootstrap treats ~95%-correlated rows as independent
    and reported an interval 2.1x too narrow. Pass block=horizon."""
    empty = {"n": 0, "days": 0, "acc": np.nan, "null": np.nan, "skill": np.nan,
             "ci_lo": np.nan, "ci_hi": np.nan, "deff": np.nan, "agreement": np.nan,
             "day_acc": np.nan, "by_day": pd.DataFrame()}
    if pred is None or len(pred) == 0:
        return empty

    p = pred.loc[:, ["day", "symbol_id", "prob", "y"]].copy()
    p["call"] = (p["prob"] > 0.5).astype(float)
    p["hit"] = (p["call"] == p["y"]).astype(float)
    n, acc = len(p), float(p["hit"].mean())

    g = p.groupby("day", sort=True)
    s, c, h = g["y"].sum(), g["y"].count(), g["hit"].sum()
    nd = len(c)

    # prequential majority: day d is scored by the majority over days STRICTLY
    # before d. Day 0 has no prior, so it is credited the model's own hits and
    # contributes exactly zero lift.
    prior_mean = s.cumsum().shift(1) / c.cumsum().shift(1)
    maj_up = (prior_mean >= 0.5).to_numpy()
    null_hits = np.where(maj_up, s.to_numpy(), (c - s).to_numpy()).astype(float)
    null_hits[0] = float(h.iloc[0])
    null = float(null_hits.sum() / c.sum())

    day_acc_arr = g["hit"].mean().to_numpy()
    w = c.to_numpy(float)
    rng = np.random.default_rng(0)
    if block and block > 1:
        # collapse consecutive days into non-overlapping blocks first
        bid = np.arange(nd) // int(block)
        bh = np.bincount(bid, weights=g["hit"].sum().to_numpy())
        bn = np.bincount(bid, weights=w)
        units_hit, units_n = bh, bn
    else:
        units_hit, units_n = g["hit"].sum().to_numpy(), w
    nu = len(units_n)
    pick = rng.integers(0, nu, size=(2000, nu))
    boot = units_hit[pick].sum(1) / units_n[pick].sum(1)
    ci_lo, ci_hi = (float(x) for x in np.percentile(boot, [2.5, 97.5]))

    naive_var = acc * (1 - acc) / n
    deff = float(boot.var() / naive_var) if naive_var > 0 else np.nan

    frac_up = g["call"].mean()
    agreement = float(np.maximum(frac_up, 1 - frac_up).mean())
    day_acc = float(((frac_up > 0.5).astype(int) == (g["y"].mean() > 0.5).astype(int)).mean())

    by_day = pd.DataFrame({"n": c, "acc": g["hit"].mean(), "frac_up": frac_up,
                           "y_up": g["y"].mean()}).reset_index()
    return {"n": int(n), "days": int(nd), "blocks": int(nu), "acc": acc,
            "null": null, "skill": acc - null,
            "ci_lo": ci_lo, "ci_hi": ci_hi, "deff": deff, "agreement": agreement,
            "day_acc": day_acc, "by_day": by_day}
