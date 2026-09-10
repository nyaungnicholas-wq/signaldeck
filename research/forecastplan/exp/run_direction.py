"""Nested walk-forward direction campaign (manifest forecastplan-exp-v1).

Outer: chronological 126-session blocks, expanding training window purged by the horizon
and embargoed by 21 sessions. Inner: the last 3x126 sessions of each training window select
the candidate by log loss; isotonic calibration and the abstention threshold are fit on
those inner out-of-sample predictions only. Every candidate is scored on every outer block
(the whole family enters the Holm correction); the inner winner is only the headline.
Baseline B0 is the prequential majority over strictly earlier days, matched row by row.
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

LABEL_COLS = {"fwd_1", "fwd_5", "up_1", "up_5"}
MANIFEST = "forecastplan-exp-v1"


def load(path):
    df = pd.read_parquet(path)
    if isinstance(df.index, pd.MultiIndex):
        df = df.reset_index()
    df = df.sort_values(["day", "symbol_id"]).reset_index(drop=True)
    feats = [c for c in df.columns if c not in LABEL_COLS and c not in ("day", "symbol_id")]
    return df, feats


def between(day, a, b):
    return (day >= a) & (day <= b)


def rec(**kw):
    kw.setdefault("family", "direction")
    kw.setdefault("started_utc", datetime.now(timezone.utc).isoformat())
    kw.setdefault("command", " ".join(sys.argv))
    return kw


def run_horizon(df, feats, h, a, ledger):
    t_start = time.time()
    lab = df[df[f"up_{h}"].notna()].reset_index(drop=True)
    days_all = np.sort(df["day"].unique())
    day = lab["day"].to_numpy()
    dint = np.searchsorted(days_all, day)
    y = lab[f"up_{h}"].to_numpy(float)
    b0 = M.prequential_majority(dint, y)
    groups = L.feature_groups(feats)
    X_all = lab[feats].to_numpy(np.float32)
    col_idx = {c: [feats.index(k) for k in L.model_columns(c, groups)] for c in a.cands}
    mom = lab[f"mom_{h}"].to_numpy(float) if f"mom_{h}" in lab.columns else None
    outer = S.outer_splits(days_all, h, a.block_len, a.blocks, a.embargo)
    st = {c: {"stopped": False, "inner_ll": [], "base_ll": [], "res": {"none": [], "isotonic": []},
              "skill": {"none": [], "isotonic": []}, "p": {"none": [], "isotonic": []}, "dint": [], "y": [], "b0": []}
          for c in a.cands}
    baselines = {"B1_always_up": [], "B2_persistence": []}
    winners, preds, fits, processed = {}, [], 0, 0
    for blk in outer:
        tr = day <= blk["train_end"]
        te = between(day, blk["test_start"], blk["test_end"])
        if te.sum() == 0 or tr.sum() < 1000:
            continue
        processed += 1
        folds = S.inner_folds(days_all, blk["train_end"], h, a.block_len, a.inner_folds, a.embargo)
        inner, block_preds = {}, {}
        for c in a.cands:
            s = st[c]
            if s["stopped"]:
                continue
            Xc = X_all[:, col_idx[c]]
            p_oos, y_oos, base = [], [], []
            for f in folds:
                ftr = day <= f["train_end"]
                fva = between(day, f["val_start"], f["val_end"])
                if fva.sum() == 0 or ftr.sum() < 1000:
                    continue
                t0 = time.time()
                p, n_used = L.fit_predict(c, Xc[ftr], y[ftr], Xc[fva], a.max_train_rows)
                fits += 1
                p_oos.append(p)
                y_oos.append(y[fva])
                base.append(np.full(int(fva.sum()), float(y[ftr].mean())))
                L.ledger_write(ledger, rec(trial_id=f"dir-h{h}-b{blk['block']}-f{f['fold']}-{c}", kind_or_horizon=f"{h}",
                                           model=c, params={"horizon": h}, feature_set="all", calibration="none", stage="inner",
                                           block_or_fold=f"b{blk['block']}/f{f['fold']}", n_train=int(n_used), n_test=int(fva.sum()),
                                           metrics={"log_loss": M.log_loss(p, y[fva]), "acc": M.accuracy(p, y[fva])},
                                           duration_s=round(time.time() - t0, 2), status="ok", artifacts=[]))
            p_oos, y_oos, base = np.concatenate(p_oos), np.concatenate(y_oos), np.concatenate(base)
            ill, bll = M.log_loss(p_oos, y_oos), M.log_loss(base, y_oos)
            s["inner_ll"].append(ill)
            s["base_ll"].append(bll)
            inner[c] = ill
            t0 = time.time()
            p_raw, n_used = L.fit_predict(c, Xc[tr], y[tr], Xc[te], a.max_train_rows)
            fits += 1
            iso = L.fit_isotonic(p_oos, y_oos)
            p_cal = L.apply_cal(iso, p_raw)
            thr = {"none": L.abstention_threshold(p_oos), "isotonic": L.abstention_threshold(L.apply_cal(iso, p_oos))}
            for cal, p in (("none", p_raw), ("isotonic", p_cal)):
                m = L.evaluate(p, y[te], dint[te], b0[te], thr[cal])
                m.update(block=blk["block"], inner_log_loss=ill, base_rate_log_loss=bll, n_train=int(n_used), n_test=int(te.sum()))
                s["res"][cal].append(m)
                s["skill"][cal].append(L.per_row_skill(p, y[te], b0[te]))
                s["p"][cal].append(p)
                L.ledger_write(ledger, rec(trial_id=f"dir-h{h}-b{blk['block']}-{c}-{cal}", kind_or_horizon=f"{h}", model=c,
                                           params={"horizon": h, "threshold": float(thr[cal])}, feature_set="all", calibration=cal,
                                           stage="outer", block_or_fold=f"b{blk['block']}", n_train=int(n_used), n_test=int(te.sum()),
                                           metrics=m, duration_s=round(time.time() - t0, 2), status="ok", artifacts=[]))
            s["dint"].append(dint[te])
            s["y"].append(y[te])
            s["b0"].append(b0[te])
            block_preds[c] = (p_raw, p_cal)
        if inner:
            w = min(inner, key=lambda c: (inner[c], a.cands.index(c)))
            winners[str(blk["block"])] = w
            preds.append(pd.DataFrame({"day": day[te], "symbol_id": lab["symbol_id"].to_numpy()[te], "y": y[te], "b0": b0[te],
                                       "p_raw": block_preds[w][0], "p_cal": block_preds[w][1], "block": blk["block"], "candidate": w}))
        for name, guess in (("B1_always_up", np.ones(int(te.sum()))), ("B2_persistence", (mom[te] > 0).astype(float) if mom is not None else None)):
            if guess is None:
                continue
            m = L.evaluate(guess, y[te], dint[te], b0[te], None)
            m.update(block=blk["block"])
            baselines[name].append(m)
            L.ledger_write(ledger, rec(trial_id=f"dir-h{h}-b{blk['block']}-{name}", kind_or_horizon=f"{h}", model=name, params={},
                                       feature_set="none", calibration="none", stage="baseline", block_or_fold=f"b{blk['block']}",
                                       n_train=0, n_test=int(te.sum()), metrics=m, duration_s=0.0, status="ok", artifacts=[]))
        if processed == 3 and not a.no_stopping:
            for c in a.cands:
                s = st[c]
                if s["stopped"] or not s["res"]["none"]:
                    continue
                mean_skill = float(np.mean([m["skill_pp"] for m in s["res"]["none"] if m["skill_pp"] is not None]))
                if mean_skill <= 0 and float(np.mean(s["inner_ll"])) >= float(np.mean(s["base_ll"])):
                    s["stopped"] = True
                    L.ledger_write(ledger, rec(trial_id=f"dir-h{h}-stop-{c}", kind_or_horizon=f"{h}", model=c, params={"after_blocks": 3},
                                               feature_set="all", calibration="none", stage="outer", block_or_fold="stopped",
                                               n_train=0, n_test=0, metrics={"mean_skill_pp_blocks_1_3": mean_skill}, duration_s=0.0,
                                               status="stopped", artifacts=[]))
    results = {}
    for c in a.cands:
        s = st[c]
        for cal in ("none", "isotonic"):
            if not s["res"][cal]:
                continue
            skill, di = np.concatenate(s["skill"][cal]), np.concatenate(s["dint"])
            p, yy, bb = np.concatenate(s["p"][cal]), np.concatenate(s["y"]), np.concatenate(s["b0"])
            boot = {str(bl): M.block_bootstrap(skill, di, block_len=bl) for bl in (21, 5, 63)}
            pooled = L.evaluate(p, yy, di, bb, None)
            hc_n = sum(m.get("hc_n") or 0 for m in s["res"][cal])
            pooled["hc20_skill_pp_weighted"] = (sum((m.get("hc_skill_pp") or 0) * (m.get("hc_n") or 0) for m in s["res"][cal]) / hc_n) if hc_n else None
            pooled["hc20_coverage_mean"] = float(np.mean([m.get("hc_coverage") or 0 for m in s["res"][cal]]))
            ge = sum(1 for m in s["res"][cal] if m["skill_pp"] is not None and m["skill_pp"] >= 1.0)
            b21 = boot["21"]
            mpui = bool(b21["point"] is not None and b21["point"] >= 1.0 and b21["lo"] is not None and b21["lo"] > 0 and ge >= 4)
            results[f"{c}|{cal}"] = {"candidate": c, "calibration": cal, "blocks": s["res"][cal], "pooled": pooled, "bootstrap": boot,
                                     "blocks_ge_1pp": ge, "mpui_full": mpui, "stopped": s["stopped"],
                                     "inner_log_loss_mean": float(np.mean(s["inner_ll"])), "base_rate_log_loss_mean": float(np.mean(s["base_ll"]))}
    base_out = {k: {"blocks": v, "pooled_acc": float(np.mean([m["acc"] for m in v])) if v else None,
                    "pooled_skill_pp": float(np.mean([m["skill_pp"] for m in v if m["skill_pp"] is not None])) if v else None}
                for k, v in baselines.items()}
    return {"horizon": h, "n_rows": int(len(lab)), "n_days": int(len(np.unique(dint))), "blocks_processed": processed,
            "splits": [{k: (str(v)[:10] if k != "block" else v) for k, v in b.items() if k in ("block", "train_start", "train_end", "test_start", "test_end")} for b in outer],
            "inner_winners": winners, "results": results, "baselines": base_out, "fits": fits,
            "duration_s": round(time.time() - t_start, 1)}, (pd.concat(preds) if preds else None)


def summary_md(out):
    lines = [f"# Direction campaign {MANIFEST} — {out['generated_at_utc']}", ""]
    for h, r in out["horizons"].items():
        lines += [f"## horizon {h}: {r['n_rows']} rows, {r['n_days']} days, {r['blocks_processed']} blocks, {r['fits']} fits, inner winners {r['inner_winners']}", "",
                  "| candidate | cal | pooled acc | B0 acc | skill_pp | CI21 lo | CI21 hi | p_le_0 | Holm p_adj | blocks>=1pp | hc20 cov | hc20 skill_pp | within-day AUC | stopped |",
                  "|---|---|---|---|---|---|---|---|---|---|---|---|---|---|"]
        for key, v in r["results"].items():
            p, b = v["pooled"], v["bootstrap"]["21"]
            hp = out["holm"].get(f"{key}|h{h}", {}).get("p_adj")
            f = lambda x: "" if x is None else (f"{x:.4f}" if isinstance(x, float) else str(x))
            lines.append(f"| {v['candidate']} | {v['calibration']} | {f(p.get('acc'))} | {f(p.get('b0_acc'))} | {f(p.get('skill_pp'))} | {f(b.get('lo'))} | {f(b.get('hi'))} | {f(b.get('p_le_0'))} | {f(hp)} | {v['blocks_ge_1pp']} | {f(p.get('hc20_coverage_mean'))} | {f(p.get('hc20_skill_pp_weighted'))} | {f(p.get('auc_within_day_mean'))} | {v['stopped']} |")
        for k, v in r["baselines"].items():
            lines.append(f"| {k} | - | {'' if v['pooled_acc'] is None else round(v['pooled_acc'], 4)} | | {'' if v['pooled_skill_pp'] is None else round(v['pooled_skill_pp'], 3)} | | | | | | | | | |")
        lines.append("")
    return "\n".join(lines)


def run(args):
    t0 = time.time()
    os.makedirs(args.out_dir, exist_ok=True)
    ledger = os.path.join(args.out_dir, "ledger.jsonl")
    df, feats = load(args.data)
    out = {"manifest_id": MANIFEST, "generated_at_utc": datetime.now(timezone.utc).isoformat(), "args": vars(args), "horizons": {}}
    for h in args.horizons:
        r, preds = run_horizon(df, feats, h, args, ledger)
        out["horizons"][str(h)] = r
        if preds is not None:
            preds.to_parquet(os.path.join(args.out_dir, f"direction_predictions_h{h}.parquet"), index=False)
    fam = {f"{key}|h{h}": v["bootstrap"]["21"]["p_le_0"] for h, r in out["horizons"].items() for key, v in r["results"].items()
           if v["bootstrap"]["21"]["p_le_0"] is not None}
    out["holm"] = M.holm(fam)
    out["fits_total"] = sum(r["fits"] for r in out["horizons"].values())
    out["duration_s"] = round(time.time() - t0, 1)
    with open(os.path.join(args.out_dir, "direction_results.json"), "w", encoding="utf-8") as fh:
        json.dump(out, fh, indent=1, default=str)
    with open(os.path.join(args.out_dir, "direction_summary.md"), "w", encoding="utf-8") as fh:
        fh.write(summary_md(out))
    return out


def selfcheck():
    rng = np.random.default_rng(0)
    days = pd.bdate_range("2019-01-01", periods=1300)
    n_sym = 50
    rows = len(days) * n_sym
    names = ["mom_1", "mom_5", "mkt_mean_ret", "mkt_mean_ret_5", "mkt_mean_ret_21", "mkt_dispersion", "mkt_breadth", "x_signal"]
    X = rng.standard_normal((rows, len(names))).astype(np.float32)
    df = pd.DataFrame(X, columns=names)
    df["day"] = np.repeat(days.to_numpy(), n_sym)
    df["symbol_id"] = np.tile(np.arange(n_sym), len(days))
    for h in (1, 5):
        up = (0.8 * X[:, -1] + rng.standard_normal(rows) > 0).astype(float)
        df[f"up_{h}"] = up
        df[f"fwd_{h}"] = up * 0.01 - 0.005
    tmp = tempfile.mkdtemp()
    data = os.path.join(tmp, "d.parquet")
    df.to_parquet(data, index=False)
    args = argparse.Namespace(data=data, out_dir=tmp, horizons=[1], cands=["M1_c0.1", "M2_hgb_shallow"], blocks=3, block_len=100,
                              embargo=5, inner_folds=2, max_train_rows=20000, no_stopping=False)
    out = run(args)
    r = out["horizons"]["1"]
    assert r["blocks_processed"] == 3, r["blocks_processed"]
    m1 = r["results"]["M1_c0.1|none"]
    assert m1["bootstrap"]["21"]["point"] > 0 and m1["bootstrap"]["21"]["lo"] > 0, m1["bootstrap"]["21"]
    assert os.path.exists(os.path.join(tmp, "direction_results.json")) and os.path.exists(os.path.join(tmp, "direction_summary.md"))
    with open(os.path.join(tmp, "ledger.jsonl"), encoding="utf-8") as fh:
        outer = [1 for line in fh if '"stage": "outer"' in line]
    assert len(outer) >= 6, len(outer)
    print("SELFCHECK OK")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--data", default="research/forecastplan/exp/out/direction_dataset_v1.parquet")
    ap.add_argument("--out-dir", default="research/forecastplan/exp/out")
    ap.add_argument("--horizons", default="1,5")
    ap.add_argument("--candidates", default=",".join(L.CANDIDATES))
    ap.add_argument("--blocks", type=int, default=6)
    ap.add_argument("--block-len", type=int, default=126)
    ap.add_argument("--embargo", type=int, default=21)
    ap.add_argument("--inner-folds", type=int, default=3)
    ap.add_argument("--max-train-rows", type=int, default=1500000)
    ap.add_argument("--no-stopping", action="store_true")
    ap.add_argument("--selfcheck", action="store_true")
    a = ap.parse_args()
    if a.selfcheck:
        selfcheck()
        return
    a.horizons = [int(x) for x in a.horizons.split(",")]
    a.cands = [c for c in a.candidates.split(",") if c]
    out = run(a)
    print(f"DIRECTION RESEARCH OK horizons={a.horizons} blocks={a.blocks} fits={out['fits_total']}")


if __name__ == "__main__":
    main()
