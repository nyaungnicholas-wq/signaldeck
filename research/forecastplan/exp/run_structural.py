"""Structural campaign (manifest forecastplan-exp-v1): can a model beat frozen persistence?

Targets are the resolver labels; the baseline is the persistence label frozen at the origin
(what regime_outcomes.naive_label stores live); the incumbent's own call is scored beside them.
Outer: 126-session blocks with purge 21 and embargo 21; inner: 3 folds select the (model,
framing) by log loss, but every (model, framing) is scored on every block (the Holm family).
Evaluation rows sit on a non-overlapping 21-session grid (offset 0 headline, 7 and 14 as
sensitivity); training uses every origin. 'transition' framing predicts P(actual != state).
"""
import argparse
import json
import os
import sys
import tempfile
import time
from datetime import datetime, timezone

import numpy as np
import pandas as pd

THIS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, THIS)
import splits as S  # noqa: E402
import metrics as M  # noqa: E402
import direction_lib as L  # noqa: E402

MANIFEST = "forecastplan-exp-v1"
POS = {"trend21": "uptrend", "liquidity21": "active", "vol21": "elevated"}
KIND_FEATS = {"trend21": ["dist_trend", "conv_trend21"], "liquidity21": ["rank_liquidity", "conv_liquidity21"],
              "vol21": ["rank_vol", "conv_vol21"]}
COMMON = ["z_trend", "sma_slope5", "vol_21", "vol_ratio_21_63", "mom_21", "mom_63", "cs_rank_vol_21",
          "mkt_mean_ret_21", "mkt_dispersion"]
MODELS = ["M1_c0.1", "M2_hgb_shallow"]
FRAMINGS = ["class", "transition"]
KEYS = [f"{m}|{f}" for m in MODELS for f in FRAMINGS]


def metrics_for(pred, p, y, n, c):
    hit, ph = (pred == y).astype(float), (n == y).astype(float)
    out = {"n": int(len(y)), "acc": float(hit.mean()), "persistence_acc": float(ph.mean()),
           "skill_pp": float(100 * (hit.mean() - ph.mean()))}
    has = np.isfinite(c)
    out["incumbent_acc"] = float((c[has] == y[has]).mean()) if has.any() else None
    out["n_incumbent"] = int(has.sum())
    out["balanced_accuracy"] = M.balanced_accuracy(p, y)
    out["persistence_balanced"] = M.balanced_accuracy(n, y)
    out["brier"] = M.brier(p, y)
    actual_tr, pred_tr = y != n, pred != n
    out["transition_recall"] = float((pred_tr & actual_tr).sum() / actual_tr.sum()) if actual_tr.sum() else None
    out["transition_precision"] = float((pred_tr & actual_tr).sum() / pred_tr.sum()) if pred_tr.sum() else None
    out["predicted_transition_share"] = float(pred_tr.mean())
    out["actual_transition_share"] = float(actual_tr.mean())
    out["confusion"] = [[int(((pred == 0) & (y == 0)).sum()), int(((pred == 1) & (y == 0)).sum())],
                        [int(((pred == 0) & (y == 1)).sum()), int(((pred == 1) & (y == 1)).sum())]]
    return out, hit - ph


def rec(**kw):
    kw.setdefault("family", "structural")
    kw.setdefault("started_utc", datetime.now(timezone.utc).isoformat())
    kw.setdefault("command", " ".join(sys.argv))
    return kw


def run_kind(df, days_all, kind, screen, a, ledger):
    t_start = time.time()
    sub = df if screen == "noscreen" else df[df["eligible"]]
    lab = sub[sub[kind].notna() & sub[f"naive_{kind}"].notna()].sort_values(["day", "symbol_id"]).reset_index(drop=True)
    y = (lab[kind] == POS[kind]).to_numpy(float)
    n = (lab[f"naive_{kind}"] == POS[kind]).to_numpy(float)
    call = lab[f"call_{kind}"]
    c = np.where(call.notna(), (call == POS[kind]).astype(float), np.nan)
    X = np.column_stack([lab[KIND_FEATS[kind] + COMMON].to_numpy(np.float32), n.astype(np.float32)])
    day = lab["day"].to_numpy()
    dint = np.searchsorted(days_all, day)
    anchor = int(np.searchsorted(days_all, np.datetime64("2019-07-26")))
    grids = {off: ((dint - anchor - off) % 21 == 0) & (dint >= anchor + off) for off in (0, 7, 14)}
    last = days_all[days_all <= lab["day"].max()][-1]
    outer = S.outer_splits(days_all, 21, a.block_len, a.blocks, a.embargo, last_origin=last)
    res = {k: {"blocks": [], "skill": [], "dint": [], "p": [], "pred": [], "y": [], "n": [], "c": [], "inner": [],
               "sens": {"7": [], "14": []}} for k in KEYS}
    winners, fits, processed = {}, 0, 0
    for blk in outer:
        tr = day <= blk["train_end"]
        te_all = (day >= blk["test_start"]) & (day <= blk["test_end"])
        if (te_all & grids[0]).sum() < 30 or tr.sum() < 1000:
            continue
        processed += 1
        folds = S.inner_folds(days_all, blk["train_end"], 21, a.block_len, a.inner_folds, a.embargo)
        inner = {}
        for m in MODELS:
            for f in FRAMINGS:
                key = f"{m}|{f}"
                target = y if f == "class" else (y != n).astype(float)
                lls = []
                for fo in folds:
                    ftr = day <= fo["train_end"]
                    fva = (day >= fo["val_start"]) & (day <= fo["val_end"]) & grids[0]
                    if fva.sum() < 30 or ftr.sum() < 1000:
                        continue
                    t0 = time.time()
                    p, nu = L.fit_predict(m, X[ftr], target[ftr], X[fva], a.max_train_rows)
                    fits += 1
                    lls.append(M.log_loss(p, target[fva]))
                    L.ledger_write(ledger, rec(trial_id=f"str-{kind}-{screen}-b{blk['block']}-f{fo['fold']}-{key}", kind_or_horizon=kind,
                                               model=m, params={"framing": f, "screen": screen}, feature_set="structural", calibration="none",
                                               stage="inner", block_or_fold=f"b{blk['block']}/f{fo['fold']}", n_train=int(nu), n_test=int(fva.sum()),
                                               metrics={"log_loss": lls[-1]}, duration_s=round(time.time() - t0, 2), status="ok", artifacts=[]))
                inner[key] = float(np.mean(lls)) if lls else float("inf")
                res[key]["inner"].append(inner[key])
                t0 = time.time()
                p_raw, nu = L.fit_predict(m, X[tr], target[tr], X[te_all], a.max_train_rows)
                fits += 1
                nt = n[te_all]
                p_state = p_raw if f == "class" else nt * (1 - p_raw) + (1 - nt) * p_raw
                pred = (p_state >= 0.5).astype(float)
                g0 = grids[0][te_all]
                mtr, skill = metrics_for(pred[g0], p_state[g0], y[te_all][g0], nt[g0], c[te_all][g0])
                mtr.update(block=blk["block"], n_train=int(nu), inner_log_loss=inner[key])
                r = res[key]
                r["blocks"].append(mtr)
                for name, val in (("skill", skill), ("dint", dint[te_all][g0]), ("p", p_state[g0]), ("pred", pred[g0]),
                                  ("y", y[te_all][g0]), ("n", nt[g0]), ("c", c[te_all][g0])):
                    r[name].append(val)
                for off in ("7", "14"):
                    gg = grids[int(off)][te_all]
                    if gg.sum() >= 30:
                        ms, _ = metrics_for(pred[gg], p_state[gg], y[te_all][gg], nt[gg], c[te_all][gg])
                        r["sens"][off].append({"block": blk["block"], "n": ms["n"], "acc": ms["acc"], "skill_pp": ms["skill_pp"]})
                L.ledger_write(ledger, rec(trial_id=f"str-{kind}-{screen}-b{blk['block']}-{key}", kind_or_horizon=kind, model=m,
                                           params={"framing": f, "screen": screen}, feature_set="structural", calibration="none", stage="outer",
                                           block_or_fold=f"b{blk['block']}", n_train=int(nu), n_test=int(g0.sum()), metrics=mtr,
                                           duration_s=round(time.time() - t0, 2), status="ok", artifacts=[]))
        winners[str(blk["block"])] = min(inner, key=lambda k: (inner[k], KEYS.index(k)))
    results = {}
    for key, r in res.items():
        if not r["blocks"]:
            continue
        cat = {k: np.concatenate(r[k]) for k in ("skill", "dint", "p", "pred", "y", "n", "c")}
        pooled, _ = metrics_for(cat["pred"], cat["p"], cat["y"], cat["n"], cat["c"])
        # grid rows sit every 21 sessions, so a 63-session block is 3 grid days and 126 is 6
        boot = {bl: M.block_bootstrap(100.0 * cat["skill"], cat["dint"], block_len=int(bl) // 21) for bl in ("63", "126")}
        ge = sum(1 for b in r["blocks"] if b["skill_pp"] >= 1.0)
        b63 = boot["63"]
        lo_ok = b63["lo"] is not None and b63["lo"] > 0
        bal_gain = (pooled["balanced_accuracy"] or 0) - (pooled["persistence_balanced"] or 0)
        mpui = bool((b63["point"] >= 1.0 and lo_ok and ge >= 4) or
                    (bal_gain >= 0.05 and pooled["acc"] >= pooled["persistence_acc"] - 0.005 and lo_ok))
        sens = {off: {"pooled_skill_pp": float(np.average([s["skill_pp"] for s in v], weights=[s["n"] for s in v])) if v else None,
                      "blocks": v} for off, v in r["sens"].items()}
        results[key] = {"model": key.split("|")[0], "framing": key.split("|")[1], "blocks": r["blocks"], "pooled": pooled, "bootstrap": boot,
                        "blocks_ge_1pp": ge, "mpui": mpui, "sensitivity": sens, "inner_log_loss_mean": float(np.mean(r["inner"]))}
    return {"kind": kind, "screen": screen, "n_rows": int(len(lab)), "n_grid_rows": int(grids[0].sum()), "blocks_processed": processed,
            "splits": [{k: (str(v)[:10] if k != "block" else v) for k, v in b.items() if k in ("block", "train_start", "train_end", "test_start", "test_end")} for b in outer],
            "inner_winners": winners, "results": results, "fits": fits, "duration_s": round(time.time() - t_start, 1)}


def summary_md(out):
    f = lambda x: "" if x is None else (f"{x:.4f}" if isinstance(x, float) else str(x))
    lines = [f"# Structural campaign {MANIFEST} — {out['generated_at_utc']}", ""]
    for kk, r in out["runs"].items():
        lines += [f"## {kk}: {r['n_rows']} rows ({r['n_grid_rows']} on the evaluation grid), {r['blocks_processed']} blocks, {r['fits']} fits, inner winners {r['inner_winners']}", "",
                  "| model | framing | acc | persistence acc | incumbent acc (n) | skill_pp | CI63 lo | CI63 hi | p_le_0 | Holm p_adj | balanced acc | persistence balanced | transition recall | pred transition share | blocks>=1pp | grid7 skill | grid14 skill | mpui |",
                  "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
        for key, v in r["results"].items():
            p, b = v["pooled"], v["bootstrap"]["63"]
            hp = out["holm"].get(f"{kk}|{key}", {}).get("p_adj")
            lines.append(f"| {v['model']} | {v['framing']} | {f(p['acc'])} | {f(p['persistence_acc'])} | {f(p['incumbent_acc'])} ({p['n_incumbent']}) | {f(p['skill_pp'])} | {f(b['lo'])} | {f(b['hi'])} | {f(b['p_le_0'])} | {f(hp)} | {f(p['balanced_accuracy'])} | {f(p['persistence_balanced'])} | {f(p['transition_recall'])} | {f(p['predicted_transition_share'])} | {v['blocks_ge_1pp']} | {f(v['sensitivity']['7']['pooled_skill_pp'])} | {f(v['sensitivity']['14']['pooled_skill_pp'])} | {v['mpui']} |")
        lines.append("")
    return "\n".join(lines)


def run(a):
    t0 = time.time()
    os.makedirs(a.out_dir, exist_ok=True)
    ledger = os.path.join(a.out_dir, "ledger.jsonl")
    df = pd.read_parquet(a.data)
    days_all = np.sort(df["day"].unique())
    out = {"manifest_id": MANIFEST, "generated_at_utc": datetime.now(timezone.utc).isoformat(), "args": vars(a), "runs": {}}
    for kind in a.kinds:
        for screen in a.screens:
            out["runs"][f"{kind}|{screen}"] = run_kind(df, days_all, kind, screen, a, ledger)
    fam = {f"{kk}|{key}": v["bootstrap"]["63"]["p_le_0"] for kk, r in out["runs"].items() for key, v in r["results"].items()
           if v["bootstrap"]["63"]["p_le_0"] is not None}
    out["holm"] = M.holm(fam)
    out["fits_total"] = sum(r["fits"] for r in out["runs"].values())
    out["duration_s"] = round(time.time() - t0, 1)
    with open(os.path.join(a.out_dir, "structural_results.json"), "w", encoding="utf-8") as fh:
        json.dump(out, fh, indent=1, default=str)
    with open(os.path.join(a.out_dir, "structural_summary.md"), "w", encoding="utf-8") as fh:
        fh.write(summary_md(out))
    return out


def selfcheck():
    rng = np.random.default_rng(0)
    days = pd.bdate_range("2019-06-03", periods=900)
    n_sym, rows = 40, 900 * 40
    df = pd.DataFrame({c: rng.standard_normal(rows).astype(np.float32) for c in KIND_FEATS["trend21"] + COMMON})
    df["day"] = np.repeat(days.to_numpy(), n_sym)
    df["symbol_id"] = np.tile(np.arange(n_sym), 900)
    df["eligible"] = True
    naive = np.where(rng.random(rows) < 0.5, "uptrend", "downtrend")
    p_tr = 1 / (1 + np.exp(1.2 - 1.5 * df["z_trend"].to_numpy(float)))
    flip = rng.random(rows) < p_tr
    other = np.where(naive == "uptrend", "downtrend", "uptrend")
    df["naive_trend21"] = naive
    df["trend21"] = np.where(flip, other, naive)
    df["call_trend21"] = naive
    df["conv_trend21"] = rng.random(rows).astype(np.float32)
    for k in ("liquidity21", "vol21"):
        df[k] = None
        df[f"naive_{k}"] = None
        df[f"call_{k}"] = None
    tmp = tempfile.mkdtemp()
    data = os.path.join(tmp, "s.parquet")
    df.to_parquet(data, index=False)
    a = argparse.Namespace(data=data, out_dir=tmp, kinds=["trend21"], screens=["screen"], blocks=3, block_len=80, embargo=5,
                           inner_folds=2, max_train_rows=20000)
    out = run(a)
    r = out["runs"]["trend21|screen"]
    best = max(r["results"].values(), key=lambda v: v["pooled"]["skill_pp"])
    assert best["pooled"]["skill_pp"] > 0 and best["bootstrap"]["63"]["lo"] > 0, best["bootstrap"]["63"]
    with open(os.path.join(tmp, "ledger.jsonl"), encoding="utf-8") as fh:
        outer = sum(1 for line in fh if '"stage": "outer"' in line)
    assert outer >= 12, outer
    print("SELFCHECK OK")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="research/forecastplan/exp/out/structural_dataset_v1.parquet")
    ap.add_argument("--out-dir", default="research/forecastplan/exp/out")
    ap.add_argument("--kinds", default="trend21,liquidity21,vol21")
    ap.add_argument("--screens", default="screen,noscreen")
    ap.add_argument("--blocks", type=int, default=6)
    ap.add_argument("--block-len", type=int, default=126)
    ap.add_argument("--embargo", type=int, default=21)
    ap.add_argument("--inner-folds", type=int, default=3)
    ap.add_argument("--max-train-rows", type=int, default=1500000)
    ap.add_argument("--selfcheck", action="store_true")
    a = ap.parse_args()
    if a.selfcheck:
        selfcheck()
        return
    a.kinds = a.kinds.split(",")
    a.screens = a.screens.split(",")
    out = run(a)
    print(f"STRUCTURAL RESEARCH OK kinds={a.kinds} blocks={a.blocks} fits={out['fits_total']}")


if __name__ == "__main__":
    main()
