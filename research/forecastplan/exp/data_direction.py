import argparse, hashlib, json, os
from datetime import datetime, timezone
import numpy as np
import pandas as pd

def _sha256_path(p):
    h = hashlib.sha256()
    with open(p, 'rb') as f:
        for chunk in iter(lambda: f.read(1<<20), b''):
            h.update(chunk)
    return h.hexdigest()

def load_inputs(panel_path, feat_path):
    panel = pd.read_parquet(panel_path)
    feat = pd.read_parquet(feat_path)
    if not isinstance(feat.index, pd.MultiIndex):
        feat = feat.set_index(['day','symbol_id'])
    feat = feat.astype('float32')
    return panel, feat

def realign(feat, days):
    days = pd.DatetimeIndex(days)
    idx = feat.index
    pos = days.get_indexer(idx.get_level_values('day'))
    mask = (pos >= 1) & (pos < len(days))
    new_day = days[pos[mask] - 1]
    new_idx = pd.MultiIndex.from_arrays([new_day, idx.get_level_values('symbol_id')[mask]],
                                        names=['day','symbol_id'])
    return feat.loc[mask].set_index(new_idx)

def eligibility(close, volume, feat_missing):
    c = close.astype('float64')
    v = volume.astype('float64')
    cond1 = c >= 5.0
    cond2 = (c * v).rolling(21, min_periods=21).mean() >= 1e7
    cond3 = c.notna().rolling(252, min_periods=252).sum() == 252
    cond4 = feat_missing.fillna(1.0) <= 0.2
    return cond1 & cond2 & cond3 & cond4

def labels(close, h):
    fwd = close.shift(-h) / close - 1
    up = (fwd > 0).astype(float)
    up = up.where(fwd.notna(), np.nan)
    return fwd, up

def build(panel_path, feat_path, out_dir):
    os.makedirs(out_dir, exist_ok=True)
    panel, feat = load_inputs(panel_path, feat_path)
    days = np.sort(panel['day'].unique())
    close_wide = panel.pivot(index='day', columns='symbol_id', values='close').sort_index()
    vol_wide = panel.pivot(index='day', columns='symbol_id', values='volume').sort_index()
    feat_realigned = realign(feat, days)
    miss = feat.isnull().mean(axis=1)
    miss_wide = miss.unstack(fill_value=np.nan)
    elig = eligibility(close_wide, vol_wide, miss_wide)
    fwd1, up1 = labels(close_wide, 1)
    fwd5, up5 = labels(close_wide, 5)
    df = feat_realigned.copy()
    df['eligible'] = elig.stack()
    df['fwd_1'] = fwd1.stack()
    df['up_1'] = up1.stack()
    df['fwd_5'] = fwd5.stack()
    df['up_5'] = up5.stack()
    df = df[df['eligible']].drop(columns='eligible')
    feat_cols = [c for c in df.columns if c not in {'fwd_1','fwd_5','up_1','up_5'}]
    df[feat_cols] = df[feat_cols].astype('float32')
    df[['fwd_1','fwd_5','up_1','up_5']] = df[['fwd_1','fwd_5','up_1','up_5']].astype('float64')
    out_par = os.path.join(out_dir, 'direction_dataset_v1.parquet')
    df.to_parquet(out_par)
    idx = df.index
    days_idx = idx.get_level_values('day')
    sym_idx = idx.get_level_values('symbol_id')
    summary = {
        'rows': int(len(df)),
        'days': int(days_idx.nunique()),
        'symbols': int(sym_idx.nunique()),
        'first_origin': days_idx.min().isoformat(),
        'last_origin': days_idx.max().isoformat(),
        'eligible_rows_by_year': {int(y): int(c) for y, c in days_idx.year.value_counts().sort_index().items()},
        'label_missing_share': {
            '1': float(df['fwd_1'].isna().mean()),
            '5': float(df['fwd_5'].isna().mean())
        },
        'feature_columns': feat_cols,
        'input_sha256': {
            'panel': _sha256_path(panel_path),
            'features': _sha256_path(feat_path)
        },
        'generated_at_utc': datetime.now(timezone.utc).isoformat(),
        'manifest_id': 'forecastplan-exp-v1'
    }
    out_json = os.path.join(out_dir, 'direction_dataset_v1.json')
    with open(out_json, 'w') as f:
        json.dump(summary, f, indent=2)
    print(f'DIRECTION DATA OK rows={summary["rows"]} days={summary["days"]} symbols={summary["symbols"]}')
    return summary

def selfcheck():
    days = pd.bdate_range('2020-01-01', periods=60)
    symbols = ['AAA','BBB','CCC']
    panel_list = []
    for s, sym in enumerate(symbols):
        close = 100 * np.cumprod(1 + 0.01 * np.sin(np.arange(len(days))*0.7 + s))
        df = pd.DataFrame({
            'day': days,
            'symbol_id': sym,
            'close': close,
            'open': close*0.99,
            'high': close*1.01,
            'low': close*0.98,
            'volume': 1e6,
            'symbol': sym,
            'market': 'US',
            'name': f'Name{sym}',
            'delisted_at': pd.NaT
        })
        panel_list.append(df)
    panel = pd.concat(panel_list, ignore_index=True)
    feat_list = []
    for sym in symbols:
        sub = panel[panel.symbol_id==sym].set_index('day').sort_index()
        mom = sub['close'].pct_change(1).shift(1)
        feat_list.append(pd.DataFrame({'mom_1': mom}, index=sub.index).assign(symbol_id=sym).reset_index())
    feat = pd.concat(feat_list, ignore_index=True)
    os.makedirs('tmp_selfcheck', exist_ok=True)
    panel_path = 'tmp_selfcheck/panel.parquet'
    feat_path = 'tmp_selfcheck/feat.parquet'
    panel.to_parquet(panel_path)
    feat.to_parquet(feat_path)
    panel_df, feat_df = load_inputs(panel_path, feat_path)
    days_cal = np.sort(panel_df['day'].unique())
    close_w = panel_df.pivot(index='day', columns='symbol_id', values='close')
    feat_r = realign(feat_df, days_cal)
    for i in range(2, len(days_cal)):  # origin i-1 needs a prior close, so start at 2
        day_origin = days_cal[i-1]
        day_feat = days_cal[i]
        for sym in symbols:
            mask = (feat_df.index.get_level_values('day') == day_feat) & (feat_df.index.get_level_values('symbol_id') == sym)
            val_feat = feat_df.loc[mask, 'mom_1'].values
            if len(val_feat)==0: continue
            val_real = feat_r.loc[(day_origin, sym), 'mom_1']
            expected = close_w.loc[day_origin, sym]/close_w.loc[days_cal[i-2], sym] - 1  # mom_1 at the ORIGIN: its own close over the prior close
            assert abs(val_real - expected) < 1e-6, f'realign mismatch {sym} {day_origin}'
    fwd1, up1 = labels(close_w, 1)
    for i in range(len(days_cal)-1):
        day0 = days_cal[i]
        day1 = days_cal[i+1]
        for sym in symbols:
            exp_up = float(close_w.loc[day1, sym] > close_w.loc[day0, sym])
            got = up1.loc[day0, sym]
            assert not np.isnan(got), f'up1 NaN unexpectedly {sym} {day0}'
            assert abs(got - exp_up) < 1e-12, f'up1 mismatch {sym} {day0}'
    days_long = pd.bdate_range('2020-01-01', periods=300)
    panel_long_list = []
    for sym in symbols:
        close = 100 * np.cumprod(1 + 0.0001 * np.sin(np.arange(len(days_long))*0.7))
        df = pd.DataFrame({
            'day': days_long,
            'symbol_id': sym,
            'close': close,
            'volume': 1e6
        })
        panel_long_list.append(df)
    panel_long = pd.concat(panel_long_list, ignore_index=True)
    close_w_long = panel_long.pivot(index='day', columns='symbol_id', values='close')
    vol_w_long = panel_long.pivot(index='day', columns='symbol_id', values='volume')
    miss_w = pd.DataFrame(False, index=close_w_long.index, columns=close_w_long.columns).astype(float)
    elig_long = eligibility(close_w_long, vol_w_long, miss_w)
    assert not elig_long.iloc[:251].any().any(), 'eligibility should be False before 252 bars'
    print('SELFCHECK OK')

def main():
    p = argparse.ArgumentParser()
    p.add_argument('--panel', default='research/dirfix/panel.parquet')
    p.add_argument('--features', default='research/dirfix/features_v2.parquet')
    p.add_argument('--out-dir', default='research/forecastplan/exp/out')
    p.add_argument('--selfcheck', action='store_true')
    args = p.parse_args()
    if args.selfcheck:
        selfcheck()
    else:
        build(args.panel, args.features, args.out_dir)

if __name__ == '__main__':
    main()