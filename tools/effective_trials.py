"""Estimate effective independent trials in a grid of 48 rule evaluations.

Effective_n_trials from purgedcv assumes sequential conditioning: trial k is
drawn given trial k-1, as in Optuna TPE or CMA-ES.  A fixed rule grid lacks
that dependence, so the autocorrelation estimator yields order-dependent,
meaningless results for this static set.  The correct estimators are those that
use the correlation spectrum (eigenvalues) of the rule-return panel, which are
invariant to row/column permutation and reflect the true dimensionality of the
signal space.
"""

import numpy as np
from spa_ledger import build_panel
from purgedcv import effective_n_trials

SEED = 20260803
DIVISOR = 384
ALPHA = 0.05

def li_ji(eig):
    """Li & Ji (2005) effective number of independent tests."""
    eig = np.asarray(eig)
    c = np.where(np.abs(eig) >= 1, 1, 0)
    frac = np.abs(eig) - np.floor(np.abs(eig))
    return float(np.sum(c + frac))

def cheverud_nyholt(eig):
    """Cheverud (2001) / Nyholt (2004) effective number of independent tests."""
    eig = np.asarray(eig)
    m = len(eig)
    var_eig = np.var(eig, ddof=1)
    return float(1 + (m - 1) * (1 - var_eig / m))

def participation_ratio(eig):
    """Participation ratio of the eigenvalue spectrum."""
    eig = np.asarray(eig)
    return float((np.sum(eig) ** 2) / np.sum(eig ** 2))

def main():
    # Step 0
    R, meta = build_panel()
    m = R.shape[1]
    sharpes = (R.mean() / R.std(ddof=1) * np.sqrt(52)).values
    print(f"Panel shape: {R.shape[0]} rows x {m} columns")
    print(f"Sharpe stats: min={sharpes.min():.3f}, "
          f"median={np.median(sharpes):.3f}, max={sharpes.max():.3f}")

    # Step 1
    ent_n = effective_n_trials(sharpes, method="autocorr")
    print(f"\nEffective n_trials (natural order): {ent_n} / {m}")

    rng = np.random.default_rng(SEED)
    perm_n = np.array([effective_n_trials(sharpes[rng.permutation(m)])
                       for _ in range(200)])
    print(f"Effective n_trials (200 permutations): "
          f"min={int(perm_n.min())}, median={int(np.median(perm_n))}, "
          f"max={int(perm_n.max())}")
    print("The large spread proves effective_n_trials is order-dependent "
          "and thus invalid for a fixed grid.")

    # Step 2
    C = np.corrcoef(R.to_numpy(), rowvar=False)
    eig = np.linalg.eigvalsh(C)[::-1]

    upper_tri = np.triu_indices(m, 1)
    mean_abs_corr = np.abs(C[upper_tri]).mean()
    print(f"\nMean absolute pairwise correlation: {mean_abs_corr:.3f}")

    var_total = eig.sum()
    var_top5 = eig[:5].sum()
    pct_top5 = eig[:5] / var_total * 100
    print(f"Top 5 eigenvalue variance (% of total): "
          f"{', '.join(f'{p:.1f}' for p in pct_top5)}")
    print(f"Cumulative top 5: {var_top5/var_total*100:.1f}%")

    n_liji = li_ji(eig)
    n_chevn = cheverud_nyholt(eig)
    n_par = participation_ratio(eig)

    # Step 3
    print("\nComparison table:")
    print(f"{'Estimator':<30} {'n_eff':<10} {'divisor':<10} {'alpha':<12}")
    print("-" * 62)
    print(f"{'LIVE (grid x searches)':<30} {'--':<10} {DIVISOR:<10} "
          f"{ALPHA/DIVISOR:.3e}")
    print(f"{'Naive grid size':<30} {m:<10} {m:<10} {ALPHA/m:.3e}")
    print(f"{'Li & Ji':<30} {n_liji:<10.1f} {max(1.0, n_liji):<10.1f} "
          f"{ALPHA/max(1.0, n_liji):.3e}")
    print(f"{'Cheverud-Nyholt':<30} {n_chevn:<10.1f} {max(1.0, n_chevn):<10.1f} "
          f"{ALPHA/max(1.0, n_chevn):.3e}")
    print(f"{'Participation ratio':<30} {n_par:<10.1f} {max(1.0, n_par):<10.1f} "
          f"{ALPHA/max(1.0, n_par):.3e}")

    n_vals = [n_liji, n_chevn, n_par]
    low, high = min(n_vals), max(n_vals)
    print(f"\nEffective independent trials range: {low:.1f} to {high:.1f} "
          f"(vs {m} nominal, {DIVISOR} live)")
    print(f"On the grid dimension alone the live divisor ({DIVISOR}) is "
          f"{DIVISOR/high:.1f}x to {DIVISOR/low:.1f}x too harsh.")

if __name__ == "__main__":
    main()