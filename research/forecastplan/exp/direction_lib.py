import os, sys, json, numpy as np
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import metrics as M
from sklearn.pipeline import Pipeline
from sklearn.impute import SimpleImputer
from sklearn.preprocessing import StandardScaler
from sklearn.linear_model import LogisticRegression
from sklearn.ensemble import HistGradientBoostingClassifier
from sklearn.isotonic import IsotonicRegression

CANDIDATES = ['M1_c0.01','M1_c0.1','M1_c1','M4_market','M2_hgb_shallow','M3_hgb_medium']

def feature_groups(cols):
    return {
        'all': list(cols),
        'no_market': [c for c in cols if not c.startswith('mkt_')],
        'cross_sectional_only': [c for c in cols if c.startswith('cs_rank_') or c.startswith('rev_')],
        'momentum_only': [c for c in cols if c.startswith('mom_')],
        'market_only': [c for c in ['mkt_mean_ret','mkt_mean_ret_5','mkt_mean_ret_21','mkt_dispersion','mkt_breadth'] if c in cols]
    }

def make_model(name):
    if name.startswith('M1_'):
        c_val = float(name.split('_')[1][1:])
        clf = LogisticRegression(C=c_val, max_iter=1000)
    elif name == 'M4_market':
        clf = LogisticRegression(C=0.1, max_iter=1000)
    elif name == 'M2_hgb_shallow':
        return HistGradientBoostingClassifier(max_depth=3, max_iter=150, learning_rate=0.06,
                                              min_samples_leaf=500, l2_regularization=1.0, random_state=0)
    elif name == 'M3_hgb_medium':
        return HistGradientBoostingClassifier(max_depth=6, max_iter=300, learning_rate=0.05,
                                              min_samples_leaf=200, l2_regularization=1.0, random_state=0)
    else:
        raise ValueError(name)
    return Pipeline([('imp', SimpleImputer(strategy='median')),
                     ('sc', StandardScaler()),
                     ('clf', clf)])

def model_columns(name, groups, feature_set='all'):
    if name == 'M4_market':
        return groups['market_only']
    return groups[feature_set]

def fit_predict(name, Xtr, ytr, Xte, max_rows=1500000, seed=0):
    if name.startswith('M2') or name.startswith('M3'):
        n = Xtr.shape[0]
        if n > max_rows:
            rng = np.random.default_rng(seed)
            idx = rng.choice(n, max_rows, replace=False)
            Xtr, ytr = Xtr[idx], ytr[idx]
    model = make_model(name)
    model.fit(Xtr, ytr)
    p = model.predict_proba(Xte)[:, 1].astype(np.float64)
    return p, Xtr.shape[0]

def fit_isotonic(p, y):
    iso = IsotonicRegression(out_of_bounds='clip', y_min=0.0, y_max=1.0)
    iso.fit(p, y)
    return iso

def apply_cal(iso, p):
    return iso.predict(p)

def abstention_threshold(p, keep=0.2):
    return np.quantile(np.abs(p - 0.5), 1 - keep)

def per_row_skill(p, y, b0):
    skill = M.hits(p, y) - (b0 == y)
    skill[np.isnan(b0)] = np.nan
    return skill.astype(float)

def evaluate(p, y, day, b0, thr=None):
    n = len(y)
    out = {'n': n}
    out['acc'] = _r(M.accuracy(p, y))
    mask_b0 = ~np.isnan(b0)
    if mask_b0.any():
        acc_m = M.accuracy(p[mask_b0], y[mask_b0])
        b0_acc = M.accuracy(b0[mask_b0], y[mask_b0])
        out['b0_acc'] = _r(b0_acc)
        out['skill_pp'] = _r(100 * (acc_m - b0_acc))
        out['n_matched'] = int(mask_b0.sum())
    else:
        out['b0_acc'] = None
        out['skill_pp'] = None
        out['n_matched'] = 0
    out['always_up_acc'] = _r(np.mean(y))
    out['balanced_accuracy'] = _r(M.balanced_accuracy(p, y))
    out['brier'] = _r(M.brier(p, y))
    out['log_loss'] = _r(M.log_loss(p, y))
    out['ece'] = _r(M.ece(p, y, bins=10))
    out['auc_pooled'] = _r(M.auc(p, y))
    auc_mean, auc_se, auc_k = M.within_day_auc(p, y, day)
    out['auc_within_day_mean'] = _r(auc_mean)
    out['auc_within_day_se'] = _r(auc_se)
    out['n_days_auc'] = int(auc_k)
    if thr is not None:
        mask = np.abs(p - 0.5) >= thr
        out['hc_coverage'] = _r(mask.mean())
        out['hc_n'] = int(mask.sum())
        if mask.any():
            out['hc_acc'] = _r(M.accuracy(p[mask], y[mask]))
            b0_mask = mask & (~np.isnan(b0))
            if b0_mask.any():
                hc_acc_m = M.accuracy(p[b0_mask], y[b0_mask])
                b0_acc_m = M.accuracy(b0[b0_mask], y[b0_mask])
                out['hc_skill_pp'] = _r(100 * (hc_acc_m - b0_acc_m))
            else:
                out['hc_skill_pp'] = None
        else:
            out['hc_acc'] = None
            out['hc_skill_pp'] = None
        risk = []
        for c in (0.1, 0.2, 0.3, 0.5, 1.0):
            thr_c = np.quantile(np.abs(p - 0.5), 1 - c)
            mask_c = np.abs(p - 0.5) >= thr_c
            n_c = mask_c.sum()
            acc_c = _r(M.accuracy(p[mask_c], y[mask_c])) if n_c else None
            skill_c = None
            if n_c:
                b0_mask_c = mask_c & (~np.isnan(b0))
                if b0_mask_c.any():
                    acc_m_c = M.accuracy(p[b0_mask_c], y[b0_mask_c])
                    b0_acc_c = M.accuracy(b0[b0_mask_c], y[b0_mask_c])
                    skill_c = _r(100 * (acc_m_c - b0_acc_c))
            risk.append({'coverage': c, 'n': int(n_c), 'acc': acc_c, 'skill_pp': skill_c})
        out['risk_coverage'] = risk
    return out

def _r(x):
    return None if x is None else round(float(x), 6)

def ledger_write(path, record):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    if 'logged_utc' not in record:
        from datetime import datetime, timezone
        record['logged_utc'] = datetime.now(timezone.utc).isoformat()
    with open(path, 'a', encoding='utf-8') as f:
        f.write(json.dumps(record, default=str) + '\n')

def selfcheck():
    rng = np.random.default_rng(0)
    N = 4000
    X = rng.standard_normal((N, 8)).astype(np.float32)
    day = np.arange(N) // 40
    y = (X[:,0] + 0.5*X[:,1] + 0.8*rng.standard_normal(N) > 0).astype(int)
    Xtr, ytr = X[:3000], y[:3000]
    Xte, yte = X[3000:], y[3000:]
    day_tr, day_te = day[:3000], day[3000:]
    day_all = np.concatenate([day_tr, day_te])
    y_all = np.concatenate([ytr, yte])
    b0_all = M.prequential_majority(day_all, y_all)
    b0_te = b0_all[3000:]
    for name in CANDIDATES:
        p, n_used = fit_predict(name, Xtr, ytr, Xte, max_rows=1500000, seed=0)
        ev = evaluate(p, yte, day_te, b0_te)
        assert ev['acc'] > 0.6, f"{name} acc {ev['acc']}"
        assert ev['skill_pp'] > 0, f"{name} skill_pp {ev['skill_pp']}"
    iso = fit_isotonic(p, yte)
    p_cal = apply_cal(iso, p)
    thr = abstention_threshold(p_cal, keep=0.2)
    ev_cal = evaluate(p_cal, yte, day_te, b0_te, thr=thr)
    assert 0.15 <= ev_cal['hc_coverage'] <= 0.25, f"hc_coverage {ev_cal['hc_coverage']}"
    p2, n2 = fit_predict('M2_hgb_shallow', Xtr, ytr, Xte, max_rows=1000, seed=0)
    assert n2 == 1000, f"n_used {n2}"
    print('SELFCHECK OK')

if __name__ == '__main__':
    selfcheck()