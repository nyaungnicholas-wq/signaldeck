"""Regression gate: the HMM vol labeller must be CAUSAL.

hmm_label_path() once took argmax of the SMOOTHED posterior over 50-bar chunks
while its docstring claimed "online, no lookahead". For 49 of every 50 bars the
label conditioned on bars after i -- including C[i+1], which is exactly what
fwd_abs(closes, i) grades it against. The label could see the answer, and it was
being scored against a strictly-causal tercile baseline.

DESIGNING THIS GATE IS THE HARD PART, so read before editing. The obvious tests
are vacuous:

  * Perturbing one future bar and asserting labels don't move does NOT work on a
    well-separated series -- the posterior is saturated near 0/1, so a shifted
    posterior never flips the discrete argmax. Measured: the buggy version
    passed that assertion cleanly.
  * Prefix stability at ends 300/500/700 does NOT work either: those are
    multiples of the 50-bar chunk size, so the buggy chunking aligns exactly and
    reproduces itself.

So the series below deliberately uses OVERLAPPING regimes (mu -4.0 vs -4.5, same
sd) where the posterior is genuinely uncertain and argmax is sensitive, and the
prefix ends are deliberately NOT multiples of 50. Verified 2026-08-13 to fail on
the smoothed implementation and pass on the filtered one.

Run: python research/test_volregime_causal.py
"""
import sys
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parent))

from volregime import _forward_backward, fit_hmm, hmm_label_path  # noqa: E402

# Overlapping two-regime series: the states must be genuinely confusable or the
# argmax is insensitive and every assertion below passes vacuously.
rng = np.random.default_rng(11)
N = 900
state = np.zeros(N, dtype=int)
for i in range(1, N):
    state[i] = state[i - 1] if rng.random() > 0.05 else 1 - state[i - 1]
x = np.where(state == 1, rng.normal(-4.5, 1.0, N), rng.normal(-4.0, 1.0, N))

fit = fit_hmm(x, n_states=2, seed=0)
assert fit is not None, "fit_hmm returned None on a 900-bar series"
mu, var, A, pi = fit

base = hmm_label_path(x, mu, var, A, pi)
fail = []

# ---- A. prefix stability (ends deliberately NOT multiples of the 50 chunk) ---
# A causal filter can never revise a label it already emitted. A smoothed
# labeller rewrites past labels as new data arrives.
for end in (313, 431, 527, 689):
    prefix = hmm_label_path(x[:end], mu, var, A, pi)
    if not np.array_equal(prefix, base[:end]):
        n = int((prefix != base[:end]).sum())
        fail.append(
            f"NOT PREFIX-STABLE: labels for x[:{end}] differ in {n} place(s) once "
            f"the series is extended -- a past label was revised by future data."
        )

# ---- B. the labeller must use the FILTERED posterior, not the smoothed one ---
# This is the direct statement of the defect. On this series the two disagree on
# ~140 of 900 bars, so switching back to gamma fails loudly rather than subtly.
gamma, _, _, filtered = _forward_backward(x, mu, var, A, pi)
if not np.array_equal(base, np.argmax(filtered, axis=1)):
    fail.append("hmm_label_path does not equal argmax of the FILTERED posterior")

smoothed_labels = np.argmax(gamma, axis=1)
if np.array_equal(base, smoothed_labels):
    fail.append(
        "filtered and smoothed labels are identical on this series, so this gate "
        "cannot discriminate -- retune the regime overlap above before trusting it"
    )

# ---- C. the filtered posterior is genuinely future-independent ---------------
if not np.allclose(filtered.sum(axis=1), 1.0):
    fail.append("filtered posterior rows do not sum to 1 -- not a normalised posterior")

CUT = 600
y = x.copy()
y[CUT + 1 :] = 0.0  # obliterate the entire future
_, _, _, filt2 = _forward_backward(y, mu, var, A, pi)
if not np.allclose(filtered[: CUT + 1], filt2[: CUT + 1]):
    fail.append("filtered posterior at/before bar 600 moved when the future changed")

# ---- D. sanity: still separates the regimes (not causal-by-being-useless) ----
hi_state = int(base[np.argmax(x)])
gap = x[base == hi_state].mean() - x[base != hi_state].mean()
if not gap > 0.20:
    fail.append(f"labeller no longer separates regimes: mean gap {gap:.3f} <= 0.20")

if fail:
    print("FAIL")
    for f in fail:
        print("  -", f)
    sys.exit(1)

print("OK: hmm_label_path is causal -- prefix-stable, filtered-not-smoothed,")
print(f"    future-independent, and still separates regimes (gap {gap:.3f}).")
print(f"    Discrimination check: filtered vs smoothed differ on "
      f"{int((base != smoothed_labels).sum())} of {N} bars.")
