import argparse, json, os, sys, time, datetime as dt, numpy as np, pandas as pd
THIS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, THIS)
import splits as S, metrics as M, direction_lib as L

def outer_eval(df, days_all, h, model, feature_set, transform, args):
    lab = df[df[f'up_{h}'].notna()].copy()
    y = lab[f'up_{h}'].astype(int).to_numpy()
    dint = np.searchsorted(days_all, lab.day.to_numpy())
    b0 = M.prequential_majority(dint, y)
    feature_cols = [c for c in df.columns if c not in ('day', 'symbol_id', 'fwd_1', 'fwd_5', 'up_1', 'up_5')]  # never a label of either horizon
    groups = L.feature_groups(feature_cols)
    cols = L.model_columns(model, groups, feature_set)
    X = lab[cols].to_numpy()
    outer = S.outer_splits(days_all, h, args.block_len, args.blocks, args.embargo)
    blk_results = []
    all_skill = []
    all_dint = []
    all_y = []
    all_p = []
    for blk in outer:
        tr_mask = (lab.day >= blk['train_start']) & (lab.day <= blk['train_end'])
        te_mask = (lab.day >= blk['test_start']) & (lab.day <= blk['test_end'])
        if not tr_mask.any() or not te_mask.any():
            continue
        Xtr, ytr = X[tr_mask], y[tr_mask]
        Xte, yte, dte, b0te = X[te_mask], y[te_mask], dint[te_mask], b0[te_mask]
        if transform == 'C1_shuffle_within_day':
            rng = np.random.default_rng(1)
            ytr = ytr.copy()
            day_tr = lab.day.to_numpy()[np.asarray(tr_mask)]
            for d in np.unique(day_tr):  # permute in place within each training day: day up-rate kept, per-symbol link destroyed
                pos = np.where(day_tr == d)[0]
                if len(pos) > 1:
                    ytr[pos] = rng.permutation(ytr[pos])
        elif transform == 'C2_lag21':
            lag_df = lab.groupby('symbol_id')[cols].shift(21)  # the same symbol's vector 21 rows earlier
            Xtr = lag_df.to_numpy()[np.asarray(tr_mask)]
            Xte = lag_df.to_numpy()[np.asarray(te_mask)]
            valid_tr = ~np.isnan(Xtr).any(axis=1)
            valid_te = ~np.isnan(Xte).any(axis=1)
            Xtr, ytr = Xtr[valid_tr], ytr[valid_tr]
            Xte, yte, dte, b0te = Xte[valid_te], yte[valid_te], dte[valid_te], b0te[valid_te]
        elif transform == 'C3_noise':
            rng = np.random.default_rng(1)
            Xtr = rng.standard_normal(Xtr.shape)
            Xte = rng.standard_normal(Xte.shape)
        elif transform != 'none':
            raise ValueError(f'unknown transform {transform}')
        p, n_used = L.fit_predict(model, Xtr, ytr, Xte, args.max_train_rows, seed=20260909)
        m = L.evaluate(p, yte, dte, b0te)
        skill = L.per_row_skill(p, yte, b0te)
        blk_results.append({
            'block': blk['block'],
            'n_train': int(tr_mask.sum()),
            'n_test': int(te_mask.sum()),
            'n_used': int(n_used),
            'metrics': m,
            # per-row skill and day arrays stay in memory for the bootstrap (all_skill/all_dint) and are NOT serialised: 85 MB per JSON otherwise
        })
        all_skill.append(skill)
        all_dint.append(dte)
        all_y.append(yte)
        all_p.append(p)
        L.ledger_write(os.path.join(args.out_dir, 'ledger.jsonl'), {
            'trial_id': f'dir-{transform}-{feature_set}-h{h}-b{blk["block"]}-{model}',
            'family': 'direction',
            'stage': 'control' if transform != 'none' else 'ablation',
            'model': model,
            'feature_set': feature_set,
            'params': {'transform': transform},
            'n_train': int(tr_mask.sum()),
            'n_used': int(n_used),
            'n_test': int(te_mask.sum()),
            'metrics': m,
            'duration_s': 0.0,
            'status': 'ok'
        })
    if not all_skill:
        return {'model': model, 'feature_set': feature_set, 'transform': transform,
                'blocks': [], 'pooled': {}, 'bootstrap21': {}}
    skill_all = np.concatenate(all_skill)
    dint_all = np.concatenate(all_dint)
    y_all = np.concatenate(all_y)
    p_all = np.concatenate(all_p)
    pooled = L.evaluate(p_all, y_all, dint_all, M.prequential_majority(dint_all, y_all))
    boot = M.block_bootstrap(100.0 * skill_all, dint_all, block_len=21, n_boot=2000, seed=20260909)  # percentage points
    return {
        'model': model,
        'feature_set': feature_set,
        'transform': transform,
        'blocks': blk_results,
        'pooled': pooled,
        'bootstrap21': boot
    }

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--data', default='research/forecastplan/exp/out/direction_dataset_v1.parquet')
    ap.add_argument('--out-dir', default='research/forecastplan/exp/out')
    ap.add_argument('--horizons', default='1,5')
    ap.add_argument('--model', default='M2_hgb_shallow')
    ap.add_argument('--ablation-model', default=None)
    ap.add_argument('--blocks', type=int, default=6)
    ap.add_argument('--block-len', type=int, default=126)
    ap.add_argument('--embargo', type=int, default=21)
    ap.add_argument('--max-train-rows', type=int, default=1500000)
    ap.add_argument('--what', default='controls,ablations')
    ap.add_argument('--selfcheck', action='store_true')
    args = ap.parse_args()
    if args.ablation_model is None:
        args.ablation_model = args.model
    os.makedirs(args.out_dir, exist_ok=True)
    what = set(w.strip() for w in args.what.split(','))
    horizons = [int(h) for h in args.horizons.split(',')]
    if args.selfcheck:
        # synthetic data
        np.random.seed(20260909)
        n_days = 900
        n_sym = 40
        days = np.repeat(np.arange(n_days), n_sym)
        syms = np.tile(np.arange(n_sym), n_days)
        mom_1 = np.random.randn(len(days)).astype(np.float32)
        mom_5 = np.random.randn(len(days)).astype(np.float32)
        mom_21 = np.random.randn(len(days)).astype(np.float32)
        cs_rank_mom_21 = np.random.randn(len(days)).astype(np.float32)
        rev_1 = np.random.randn(len(days)).astype(np.float32)
        mkt_mean_ret = np.random.randn(len(days)).astype(np.float32)
        mkt_mean_ret_5 = np.random.randn(len(days)).astype(np.float32)
        mkt_mean_ret_21 = np.random.randn(len(days)).astype(np.float32)
        mkt_dispersion = np.random.randn(len(days)).astype(np.float32)
        mkt_breadth = np.random.randn(len(days)).astype(np.float32)
        x_signal = np.random.randn(len(days)).astype(np.float32)
        up_1 = (0.8*x_signal + np.random.randn(len(days))) > 0
        up_5 = (0.8*x_signal + np.random.randn(len(days))) > 0
        fwd_1 = up_1 * 0.01 - 0.005
        fwd_5 = up_5 * 0.01 - 0.005
        df = pd.DataFrame({
            'day': days,
            'symbol_id': syms,
            'mom_1': mom_1, 'mom_5': mom_5, 'mom_21': mom_21,
            'cs_rank_mom_21': cs_rank_mom_21, 'rev_1': rev_1,
            'mkt_mean_ret': mkt_mean_ret, 'mkt_mean_ret_5': mkt_mean_ret_5,
            'mkt_mean_ret_21': mkt_mean_ret_21, 'mkt_dispersion': mkt_dispersion,
            'mkt_breadth': mkt_breadth, 'x_signal': x_signal,
            'fwd_1': fwd_1, 'fwd_5': fwd_5, 'up_1': up_1.astype(int), 'up_5': up_5.astype(int)
        })
        path = os.path.join(args.out_dir, 'synthetic.parquet')
        df.to_parquet(path)
        args.data = path
        args.horizons = '1'
        args.blocks = 3
        args.block_len = 80
        args.embargo = 5
        args.max_train_rows = 20000
        args.model = 'M1_c0.1'
        args.what = 'controls,ablations'
        tmp_out = os.path.join(args.out_dir, 'selfcheck')
        args.out_dir = tmp_out
        os.makedirs(tmp_out, exist_ok=True)
    df = pd.read_parquet(args.data).reset_index().sort_values(['day','symbol_id'])
    days_all = np.sort(df.day.unique())
    result = {'manifest_id': 'forecastplan-exp-v1',
              'generated_at_utc': dt.datetime.now(dt.timezone.utc).isoformat(),
              'args': vars(args),
              'horizons': {}}
    for h in horizons:
        controls = []
        ablations = []
        void_flag = False
        if 'controls' in what:
            for tr in ('C1_shuffle_within_day', 'C2_lag21', 'C3_noise'):
                res = outer_eval(df, days_all, h, args.model, 'all', tr, args)
                controls.append(res)
                pt = res['bootstrap21'].get('point', 0.0)
                lo = res['bootstrap21'].get('lo', 0.0)
                hi = res['bootstrap21'].get('hi', 0.0)
                if abs(pt) > 1.0 and (lo > 0 or hi < 0):
                    void_flag = True
        if 'ablations' in what:
            feature_sets = ['no_market', 'cross_sectional_only', 'momentum_only', 'all']
            ablation_p = {}
            for fs in feature_sets:
                res = outer_eval(df, days_all, h, args.ablation_model, fs, 'none', args)
                ablations.append(res)
                key = f'{fs}|h{h}'
                ablation_p[key] = res['bootstrap21'].get('p_le_0', 1.0)
            holm = M.holm(ablation_p) if ablation_p else {}
        else:
            holm = {}
        result['horizons'][str(h)] = {
            'controls': controls,
            'ablations': ablations,
            'void_flag': void_flag,
            'holm': holm
        }
        # markdown table
        md_path = os.path.join(args.out_dir, f'direction_controls_summary_h{h}.md')
        with open(md_path, 'w') as f:
            f.write(f'# Horizon {h}\n')
            f.write('kind | model | feature_set | transform | pooled acc | B0 acc | skill_pp | CI21 lo | CI21 hi | p_le_0\n')
            f.write('--- | --- | --- | --- | --- | --- | --- | --- | --- | ---\n')
            for kind, lst in [('control', controls), ('ablation', ablations)]:
                for r in lst:
                    po = r['pooled']
                    boot = r['bootstrap21']
                    f.write(f'{kind} | {r["model"]} | {r["feature_set"]} | {r["transform"]} | '
                            f'{po.get("acc",0):.4f} | {po.get("b0_acc",0):.4f} | {po.get("skill_pp",0):.2f} | '
                            f'{boot.get("lo",0):.4f} | {boot.get("hi",0):.4f} | {boot.get("p_le_0",1):.4f}\n')
        # json
    json_path = os.path.join(args.out_dir, 'direction_controls.json')
    with open(json_path, 'w') as f:
        json.dump(result, f, indent=2, default=lambda o: o.tolist() if hasattr(o, "tolist") else str(o))
    # summary across horizons
    if not args.selfcheck:
        print(f'DIRECTION CONTROLS OK horizons={horizons} controls={sum(len(result["horizons"][str(h)]["controls"]) for h in horizons)} ablations={sum(len(result["horizons"][str(h)]["ablations"]) for h in horizons)} void={any(result["horizons"][str(h)]["void_flag"] for h in horizons)}')
    else:
        # assertions
        ref = result['horizons']['1']['ablations'][0]  # 'all' first
        assert ref['pooled'].get('skill_pp',0) > 0, 'reference skill_pp not >0'
        for ctrl in result['horizons']['1']['controls']:
            assert abs(ctrl['pooled'].get('skill_pp',0)) < 3, f'control skill_pp too large: {ctrl}'
        assert os.path.exists(json_path)
        assert os.path.exists(os.path.join(args.out_dir, 'ledger.jsonl'))
        print('SELFCHECK OK')

if __name__ == '__main__':
    main()