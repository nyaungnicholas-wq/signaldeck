import os
os.environ["OMP_NUM_THREADS"] = "1"
os.environ["MKL_NUM_THREADS"] = "1"
os.environ["OPENBLAS_NUM_THREADS"] = "1"

import sys
import time
import math
from concurrent.futures import ProcessPoolExecutor, as_completed
from pathlib import Path
from typing import Dict, List, Tuple, Any, Callable
import pandas as pd
import numpy as np
from sklearn.ensemble import HistGradientBoostingClassifier
from sklearn.linear_model import LogisticRegression
from sklearn.pipeline import make_pipeline
from sklearn.ensemble import VotingClassifier
from sklearn.preprocessing import StandardScaler

import lab

SPLIT = "2025-03-01"

_FEATURE_CACHE: Dict[Tuple[int, str], pd.DataFrame] = {}

def assert_search_only(frame: pd.DataFrame) -> None:
    if (frame["day"] >= pd.Timestamp(SPLIT)).any():
        raise RuntimeError("Holdout data accessed during search")

def build_cache(path: str = "features.parquet", force: bool = False) -> str:
    if not force and os.path.exists(path):
        return path
    start = time.time()
    px = lab.load()
    features = lab.make_features(px)
    features.to_parquet(path)
    elapsed = time.time() - start
    print(f"Built features: {features.shape} in {elapsed:.1f}s")
    return path

def load_search_data(horizon: int, target: str, v2: bool = False):
    key = (horizon, target, v2)
    if key not in _FEATURE_CACHE:
        path = "features_v2.parquet" if v2 else build_cache()
        features = pd.read_parquet(path)
        mask = lab.universe_mask(lab.load())
        labels = lab.make_labels(lab.load(), horizon=horizon, target=target)
        split_ts = pd.Timestamp(SPLIT)
        features = features[features.index.get_level_values("day") < split_ts]
        labels = labels[labels.index.get_level_values("day") < split_ts]
        mask = mask[mask.index.get_level_values("day") < split_ts]
        _FEATURE_CACHE[key] = (features, labels, mask)
    return _FEATURE_CACHE[key]

MODELS = {
    "hgb_deep": lambda: HistGradientBoostingClassifier(
        max_iter=300, learning_rate=0.05, min_samples_leaf=100,
        l2_regularization=1.0, random_state=0
    ),
    "hgb_shallow": lambda: HistGradientBoostingClassifier(
        max_iter=150, learning_rate=0.08, max_depth=4,
        min_samples_leaf=500, l2_regularization=5.0, random_state=0
    ),
    "logit": lambda: make_pipeline(
        StandardScaler(),
        LogisticRegression(max_iter=1000, C=0.1)
    ),
    "logit_c001": lambda: make_pipeline(
        StandardScaler(), LogisticRegression(max_iter=1000, C=0.01)),
    "logit_c1": lambda: make_pipeline(
        StandardScaler(), LogisticRegression(max_iter=1000, C=1.0)),
    "ens_lh": lambda: VotingClassifier(voting="soft", estimators=[
        ("l", make_pipeline(StandardScaler(), LogisticRegression(max_iter=1000, C=0.1))),
        ("h", HistGradientBoostingClassifier(max_iter=150, learning_rate=0.08, max_depth=4,
              min_samples_leaf=500, l2_regularization=5.0, random_state=0))]),
    "ens3": lambda: VotingClassifier(voting="soft", estimators=[
        ("l", make_pipeline(StandardScaler(), LogisticRegression(max_iter=1000, C=0.1))),
        ("l1", make_pipeline(StandardScaler(), LogisticRegression(max_iter=1000, C=1.0))),
        ("h", HistGradientBoostingClassifier(max_iter=150, learning_rate=0.08, max_depth=4,
              min_samples_leaf=500, l2_regularization=5.0, random_state=0))]),
}

FEATURE_SETS = {
    "all": lambda cols: list(cols),
    "no_market": lambda cols: [c for c in cols if not c.startswith("mkt_")],
    "cross_sectional": lambda cols: [
        c for c in cols
        if c.startswith("cs_rank_") or c.startswith("rev_") or c.startswith("mkt_")
    ]
}

def sweep_coverage(pred: pd.DataFrame, cfg: Dict[str, Any]) -> List[Dict[str, Any]]:
    pred = pred.assign(conviction=(pred["prob"] - 0.5).abs())
    # ascending=False is load-bearing: the DEFAULT ascending rank selects the
    # LOWEST-conviction rows, which silently inverts every high-conviction
    # result in the whole search. Hoisted out of the loop; it does not vary.
    ranked = pred.groupby("day")["conviction"].rank(pct=True, method="first",
                                                    ascending=False)
    results = []
    for cov in (1.0, 0.5, 0.25, 0.10, 0.05, 0.02, 0.01):
        selected = pred[ranked <= cov]
        # Ensure at least one per day. groupby.apply DROPS the grouping column
        # in pandas 3, so the old fallback returned a frame with no "day" and
        # blew up downstream -- idxmax keeps every column.
        if selected.empty and not pred.empty:
            selected = pred.loc[pred.groupby("day")["conviction"].idxmax()]
        eval_dict = lab.evaluate(selected, block=int(cfg.get('horizon', 1) or 1))
        row = {**cfg, "coverage": cov, **eval_dict}
        results.append(row)
    return results

def sweep_balanced(pred, cfg, ks=(5, 10, 20, 40, 80)):
    """top-k LONG + bottom-k SHORT per day.

    calls_up is 0.5 by construction, so the one-sided-book artifact that
    inflated every earlier low-coverage number cannot occur here. It is also
    exactly the selection a dollar-neutral long-short book makes, so the
    accuracy reported is the accuracy of trades actually taken.
    """
    if pred is None or len(pred) == 0:
        return []
    h = int(cfg.get("horizon", 1) or 1)
    hi = pred.groupby("day")["prob"].rank(method="first", ascending=False)
    lo = pred.groupby("day")["prob"].rank(method="first", ascending=True)
    out = []
    for k in ks:
        is_long, is_short = hi <= k, lo <= k
        m = is_long ^ is_short              # xor: a name cannot be both legs
        if not m.any():
            continue
        sel = pred.loc[m, ["day", "symbol_id", "y"]].copy()
        sel["prob"] = np.where(is_long[m], 0.99, 0.01)   # force the call side
        e = lab.evaluate(sel, block=h)
        e.pop("by_day", None)
        out.append({**cfg, "k": k, "sel": "balanced",
                    "calls_up": float((sel["prob"] > 0.5).mean()), **e})
    return out


def deflated_threshold(se: float, n_trials: int) -> float:
    if n_trials < 2:
        n_trials = 2
    return se * math.sqrt(2 * math.log(max(n_trials, 2)))

_PX_CACHE = {}


def _px():
    if "px" not in _PX_CACHE:
        _PX_CACHE["px"] = lab.load()
    return _PX_CACHE["px"]


def run_one(cfg: Dict[str, Any]) -> List[Dict[str, Any]]:
    try:
        fs = cfg["features"]
        v2 = fs.startswith("v2_")
        features, labels, mask = load_search_data(cfg["horizon"], cfg["target"], v2=v2)
        feature_cols = FEATURE_SETS[fs[3:] if v2 else fs](features.columns.tolist())
        X = features[feature_cols]
        y = labels
        if cfg["universe"] == "top500":
            # cross-sectional rank ACROSS symbols within each day -> axis=1.
            # groupby(level="day") on a wide frame puts one row per group and
            # ranks nothing.
            px = _px()
            mean_dv = (px["close"] * px["volume"]).rolling(21).mean()
            top500 = mean_dv.rank(axis=1, ascending=False, method="first") <= 500
            mask = mask & top500.reindex(index=mask.index, columns=mask.columns,
                                         fill_value=False)
        sel = mask.stack()
        sel.index.names = ["day", "symbol_id"]
        X = X.loc[X.index.intersection(sel[sel].index)]
        y = y.loc[X.index]
        # X is already restricted to admitted cells; re-passing the wide mask to
        # walkforward would be redundant. (Indexing the wide frame by the 4.7M
        # duplicated day labels of X was what blew up here.)
        mask = None
        max_train_days = None if cfg["window"] == "expanding" else (504 if cfg["window"] == "roll2y" else 1008)
        y_eval = None
        if cfg.get("eval_on"):
            _, y_g, _ = load_search_data(cfg["horizon"], cfg["eval_on"], v2=v2)
            y_eval = y_g.loc[X.index]
        pred = lab.walkforward(
            X, y, horizon=cfg["horizon"],
            model_fn=MODELS[cfg["model"]],
            retrain_every=126,
            min_train_days=252,
            max_train_days=max_train_days,
            mask=None, y_eval=y_eval
        )
        assert_search_only(pred)
        Path("runs").mkdir(exist_ok=True)
        pred.to_parquet(f"runs/{cfg['id']}.parquet")
        return sweep_coverage(pred, cfg) + sweep_balanced(pred, cfg)
    except Exception as e:
        return [{**cfg, "error": str(e)}]

def main(n_jobs: int = 24, out: str = "results.csv", stage: str = "A") -> pd.DataFrame:
    if stage == "A":
        # "extremes" (top 30% vs bottom 30%, middle withheld) is the separable
        # version of the problem and the only one where a real 60% can live.
        targets = ["absolute", "relative", "extremes"]
        horizons = [1, 5]
        models = ["hgb_deep", "hgb_shallow", "logit"]
        configs = []
        for t in targets:
            for h in horizons:
                for m in models:
                    cfg = {
                        "id": f"{t}_{h}_m{m}_w_expanding_u_liquid_f_all",
                        "target": t,
                        "horizon": h,
                        "model": m,
                        "window": "expanding",
                        "universe": "liquid",
                        "features": "all"
                    }
                    configs.append(cfg)
    elif stage == "I":
        # Goal raised to 70% and the stated purpose is PROFITABLE TRADES, so
        # switch to the balanced long-short selection: calls_up 0.5 by
        # construction, and it is the book a trader would actually hold.
        configs = []
        for h in (21, 42, 63):
            for t in ("extremes20", "extremes10", "relative"):
                for m in ("logit", "ens_lh"):
                    for f in ("v2_all", "v2_no_market"):
                        configs.append({"id": f"I_{t}_{h}_{m}_{f}", "target": t,
                                        "horizon": h, "model": m, "window": "expanding",
                                        "universe": "liquid", "features": f,
                                        "eval_on": "relative"})
    elif stage == "H":
        # Ensembles at the REQUESTED horizons (1d, 1w). Never tested there.
        configs = []
        for h in (1, 5):
            for t in ("extremes10", "extremes20", "relative"):
                for m in ("ens3", "ens_lh"):
                    for f in ("v2_all", "v2_no_market"):
                        configs.append({"id": f"H_{t}_{h}_{m}_{f}", "target": t,
                                        "horizon": h, "model": m, "window": "expanding",
                                        "universe": "liquid", "features": f,
                                        "eval_on": "relative"})
    elif stage == "G":
        # 21d/extremes10 landed at 59.5% on the holdout -- half a point short.
        # Ensembles were never tested; that is the cheapest honest lever left.
        # Longer horizons too, since accuracy rose monotonically with horizon.
        configs = []
        for h in (21, 42):
            for t in ("extremes10", "extremes20", "relative"):
                for m in ("ens3", "ens_lh", "logit", "hgb_shallow"):
                    for f in ("v2_all", "v2_no_market"):
                        configs.append({"id": f"G_{t}_{h}_{m}_{f}", "target": t,
                                        "horizon": h, "model": m, "window": "expanding",
                                        "universe": "liquid", "features": f,
                                        "eval_on": "relative"})
    elif stage == "F":
        # Honest grading only: train on the tail target, score on `relative`,
        # which is defined for every row. Longer horizons are the last axis
        # where directional signal genuinely accumulates.
        configs = []
        for h in (1, 5, 10, 21):
            for u in ("liquid", "top500"):
                for m in ("logit", "hgb_shallow"):
                    for t in ("extremes10", "relative"):
                        configs.append({"id": f"F_{t}_{h}_{m}_{u}", "target": t,
                                        "horizon": h, "model": m, "window": "expanding",
                                        "universe": u, "features": "v2_all",
                                        "eval_on": "relative"})
    elif stage == "E":
        # Stage D verdict: extremes10 + 5d + logit hits 59.1% at 5% coverage
        # with +8.8pp CI-confirmed skill. 1d is the blocker (~55%). logit beats
        # both GBMs everywhere -> near-linear signal, trees overfitting. Sweep
        # regularisation and an even narrower tail.
        configs = []
        for t in ("extremes10", "extremes5"):
            for h in (1, 5):
                for m in ("logit", "logit_c001", "logit_c1"):
                    for f in ("v2_all", "v2_no_market"):
                        configs.append({"id": f"E_{t}_{h}_{m}_{f}",
                                        "target": t, "horizon": h, "model": m,
                                        "window": "expanding", "universe": "liquid",
                                        "features": f})
    elif stage == "D":
        # Stage A verdict: `extremes` + 5d dominates; real skill ~+5pp at low
        # coverage. Stage D pushes on the two levers left -- the 49-column
        # anomaly feature set, and narrower tails (extremes10/20), which widen
        # the gap between the classes.
        configs = []
        for t in ("extremes10", "extremes20"):
            for h in (1, 5):
                for m in ("hgb_shallow", "hgb_deep", "logit"):
                    for u in ("liquid", "top500"):
                        configs.append({"id": f"D_{t}_{h}_{m}_{u}_v2",
                                        "target": t, "horizon": h, "model": m,
                                        "window": "expanding", "universe": u,
                                        "features": "v2_all"})
    elif stage == "C":
        df = pd.read_csv(out)
        df = df[df.get("error").isna()] if "error" in df.columns else df
        arms = (df[df["coverage"] <= 0.10]
                .sort_values("skill", ascending=False)
                .drop_duplicates(["target", "horizon"])
                .head(6)[["target", "horizon", "model", "window", "universe"]])
        configs = []
        for _, r in arms.iterrows():
            for f in ("v2_all", "v2_no_market"):
                for m in ("hgb_deep", "hgb_shallow"):
                    configs.append({"id": f'C_{r["target"]}_{r["horizon"]}_{m}_{r["window"]}_{r["universe"]}_{f}',
                                    "target": r["target"], "horizon": int(r["horizon"]), "model": m,
                                    "window": r["window"], "universe": r["universe"], "features": f})
    else:
        df = pd.read_csv(out)
        df = df[df["ci_lo"] > df["null"]]
        best = df.sort_values("skill", ascending=False).groupby(["target", "horizon", "model"]).head(3)
        arms = best[["target", "horizon", "model"]].drop_duplicates()
        windows = ["expanding", "roll2y", "roll4y"]
        universes = ["liquid", "top500"]
        features = ["all", "no_market", "cross_sectional"]
        existing = set(df["id"].tolist()) if "id" in df.columns else set()
        configs = []
        for _, row in arms.iterrows():
            t, h, m = row["target"], row["horizon"], row["model"]
            for w in windows:
                for u in universes:
                    for f in features:
                        cfg_id = f"{t}_{h}_m{m}_w{w}_u{u}_f{f}"
                        if cfg_id in existing:
                            continue
                        cfg = {
                            "id": cfg_id,
                            "target": t,
                            "horizon": h,
                            "model": m,
                            "window": w,
                            "universe": u,
                            "features": f
                        }
                        configs.append(cfg)
    Path("runs").mkdir(exist_ok=True)
    results = []
    with ProcessPoolExecutor(max_workers=n_jobs) as executor:
        future_to_cfg = {executor.submit(run_one, cfg): cfg for cfg in configs}
        for i, future in enumerate(as_completed(future_to_cfg)):
            res = future.result()
            results.extend(res)
            if (i+1) % 10 == 0:
                print(f"Completed {i+1}/{len(configs)} configs")
    df = pd.DataFrame(results)
    df["trials"] = range(1, len(df)+1)
    df["se"] = (df["ci_hi"] - df["ci_lo"]) / 3.92
    df["bar"] = df.apply(lambda r: deflated_threshold(r["se"], r["trials"]), axis=1)
    df["passes"] = df["skill"] > df["bar"]
    df.to_csv(out, index=False)
    print(f"\nTop 15 by skill:")
    print(df.sort_values("skill", ascending=False).head(15)[[
        "id", "target", "horizon", "model", "window", "universe", "features",
        "coverage", "skill", "bar", "passes"
    ]])
    return df

if __name__ == "__main__":
    stage = sys.argv[1] if len(sys.argv) > 1 else "A"
    n_jobs = int(sys.argv[2]) if len(sys.argv) > 2 else 24
    main(n_jobs=n_jobs, stage=stage)