# TARGETS: new file -> drafts/research_tests/test_research_math.py
# APPLY:   .venv/Scripts/python.exe -m pytest drafts/research_tests/ -q  (repo root)
#
# UNDER TEST: the pure math inside research/eighty/*.py. Functions are pulled
# out with conftest.load_defs(), which execs only defs/imports/constants -- the
# scripts' module-level sqlite3 work never runs and the live DB is never opened.
#
# Files covered (10): h0209 h0221 h0241 h0248 h0257 h0270 h0203 h0233 h0268 h0269
# Chosen because each computes a volatility / percentile / entry-gate number that
# lands in that hypothesis's printed PRECISION or verdict line.

import math

import pytest


# =========================================================================
# realized volatility -- h0209, h0241, h0203, h0270
# =========================================================================

def test_h0209_volatility_is_scale_invariant(hyp, gbm):
    """sigma of log returns must not change when every price is multiplied by k.

    Catches an accidental level-dependence (e.g. simple returns where log
    returns were intended, or a stray absolute price term).
    """
    f = hyp("h0209").compute_20d_volatility
    closes = gbm(n=120, seed=3)
    bars_a = [(i, 0.0, c, 1e6) for i, c in enumerate(closes)]
    bars_b = [(i, 0.0, c * 137.0, 1e6) for i, c in enumerate(closes)]
    for idx in (25, 60, 119):
        a, b = f(bars_a, idx), f(bars_b, idx)
        assert a is not None
        assert a == pytest.approx(b, rel=1e-9), f"idx={idx}: {a} vs {b}"


def test_h0209_volatility_of_constant_series_is_zero(hyp):
    f = hyp("h0209").compute_20d_volatility
    bars = [(i, 0.0, 50.0, 1e6) for i in range(60)]
    assert f(bars, 40) == pytest.approx(0.0, abs=1e-12)


def test_h0209_volatility_grows_with_dispersion(hyp, gbm):
    """A test that would pass on a constant return value must not exist:
    a noisier series must score strictly higher."""
    f = hyp("h0209").compute_20d_volatility
    quiet = [(i, 0.0, c, 1e6) for i, c in enumerate(gbm(n=80, sigma=0.005, seed=11))]
    loud = [(i, 0.0, c, 1e6) for i, c in enumerate(gbm(n=80, sigma=0.05, seed=11))]
    assert f(loud, 60) > f(quiet, 60) * 5


def test_h0209_volatility_rejects_nonpositive_price(hyp):
    """EDGE CASE: a zero/negative close would make log() explode."""
    bars = [(i, 0.0, 100.0, 1e6) for i in range(60)]
    bars[45] = (45, 0.0, 0.0, 1e6)
    assert hyp("h0209").compute_20d_volatility(bars, 50) is None


def test_h0209_volatility_short_history_is_none(hyp):
    f = hyp("h0209").compute_20d_volatility
    bars = [(i, 0.0, 100.0 + i, 1e6) for i in range(60)]
    assert f(bars, 19) is None
    assert f(bars, 0) is None


def test_h0241_compute_volatility_returns_a_number(hyp, gbm):
    """FINDING (expected to FAIL): h0241.compute_volatility is unreachable.

    research/eighty/h0241.py:59-69 builds
        closes = [bars[i][1] for i in range(end_idx-19, end_idx+1)]   -> 20 closes
        log_returns = [... for i in range(1, len(closes))]            -> 19 returns
    then gates on `if len(log_returns) < 20: return None`.
    19 < 20 always, so the function returns None for EVERY input. At
    h0241.py:150 the caller does `if vol is None: continue`, so `calls` can
    never be non-empty, `process_era` returns None, and the script prints
    INSUFFICIENT=1 unconditionally. h0241's verdict reflects a slice bug, not
    the data. Off-by-one: the guard should read `< 19`, or the slice should
    start at end_idx-20.
    """
    f = hyp("h0241").compute_volatility
    bars = [(i, c) for i, c in enumerate(gbm(n=120, seed=5))]
    assert f(bars, 100) is not None, (
        "compute_volatility returns None for every input: 20 closes yield 19 "
        "log returns but the guard demands 20")


def test_h0203_and_h0270_volatility_agree_on_dispersion(hyp, gbm):
    """Two hypotheses' independently-written rolling_std must rank the same
    series the same way, even if they differ in ddof or annualisation."""
    f270 = hyp("h0270").rolling_std
    rets270 = hyp("h0270").compute_daily_returns
    quiet = rets270(gbm(n=100, sigma=0.004, seed=21))
    loud = rets270(gbm(n=100, sigma=0.04, seed=21))
    assert f270(loud, 20) > f270(quiet, 20)


def test_h0270_rolling_std_uses_only_the_trailing_window(hyp):
    """LOOK-AHEAD GUARD. rolling_std(values, window) reads values[-window:],
    so anything before that tail must be irrelevant. Perturb the head; the
    answer must not move."""
    f = hyp("h0270").rolling_std
    vals = [0.01 * ((-1) ** i) for i in range(100)]
    a = f(vals, 20)
    poisoned = [999.0] * 80 + vals[80:]
    assert f(poisoned, 20) == pytest.approx(a, rel=1e-12)


def test_h0270_returns_handles_zero_price(hyp):
    """EDGE CASE: a zero prior close must not raise ZeroDivisionError."""
    r = hyp("h0270").compute_daily_returns([100.0, 0.0, 50.0, 55.0])
    assert len(r) == 3
    assert all(math.isfinite(x) for x in r)


def test_h0270_rolling_std_short_input_is_none(hyp):
    assert hyp("h0270").rolling_std([0.1, 0.2], 20) is None
    assert hyp("h0270").rolling_std([], 5) is None


def test_h0270_rolling_std_of_constant_is_zero(hyp):
    assert hyp("h0270").rolling_std([0.02] * 50, 20) == pytest.approx(0.0, abs=1e-12)


# =========================================================================
# percentile / rank -- h0221, h0268, h0269, h0270
# =========================================================================

def test_h0221_percentile_endpoints_and_median(hyp):
    p = hyp("h0221").percentile
    v = [1.0, 2.0, 3.0, 4.0, 5.0]
    assert p(v, 0) == 1.0
    assert p(v, 100) == 5.0
    assert p(v, 50) == 3.0


def test_h0221_percentile_is_monotone_in_p(hyp, rng):
    p = hyp("h0221").percentile
    v = sorted(float(x) for x in rng.normal(size=200))
    out = [p(v, q) for q in range(0, 101, 5)]
    assert all(b >= a - 1e-12 for a, b in zip(out, out[1:])), out


def test_h0221_percentile_stays_within_the_data_range(hyp, rng):
    p = hyp("h0221").percentile
    v = sorted(float(x) for x in rng.normal(size=137))
    for q in range(0, 101):
        assert v[0] - 1e-12 <= p(v, q) <= v[-1] + 1e-12


def test_h0221_percentile_empty_is_none(hyp):
    assert hyp("h0221").percentile([], 50) is None


def test_h0221_percentile_all_ties(hyp):
    assert hyp("h0221").percentile([7.0] * 10, 50) == 7.0
    assert hyp("h0221").percentile([7.0] * 10, 99) == 7.0


def test_h0270_percentile_of_is_monotone_and_bounded(hyp, rng):
    f = hyp("h0270").percentile_of
    s = sorted(float(x) for x in rng.normal(size=500))
    probe = sorted(float(x) for x in rng.normal(size=50))
    out = [f(s, x) for x in probe]
    assert all(0.0 <= x <= 1.0 for x in out)
    assert all(b >= a for a, b in zip(out, out[1:]))


def test_h0270_percentile_of_empty_is_half(hyp):
    assert hyp("h0270").percentile_of([], 1.0) == 0.5


def test_h0268_percentile_rank_bounds(hyp, rng):
    """h0268.percentile_rank is on a 0-100 scale while h0270.percentile_of,
    h0257.rolling_percentile_rank and h0221.percentile-derived ranks are on
    0-1. Same concept, four names, two scales, no shared module -- pinning the
    scale here so a future edit cannot quietly switch it."""
    f = hyp("h0268").percentile_rank
    s = sorted(float(x) for x in rng.normal(size=300))
    for x in rng.normal(size=40):
        assert 0.0 <= f(s, float(x)) <= 100.0


def test_h0268_percentile_rank_empty_returns_neutral(hyp):
    assert hyp("h0268").percentile_rank([], 1.0) == 50.0


def test_h0257_rolling_percentile_rank_is_bounded_and_backward_looking(hyp, rng):
    """LOOK-AHEAD GUARD on a windowed rank.

    rolling_percentile_rank(values, window, i) reads values[i-window:i] plus
    values[i]. Every index strictly greater than i must be irrelevant: rewrite
    the whole future and the answer must be identical.
    """
    f = hyp("h0257").rolling_percentile_rank
    vals = [float(x) for x in rng.normal(size=200)]
    i, w = 120, 60
    a = f(vals, w, i)
    assert a is not None and 0.0 <= a <= 1.0
    poisoned = vals[:i + 1] + [1e9] * (len(vals) - i - 1)
    assert f(poisoned, w, i) == a, "future values changed a rank computed as-of i"


def test_h0257_rolling_percentile_rank_insufficient_history(hyp, rng):
    f = hyp("h0257").rolling_percentile_rank
    vals = [float(x) for x in rng.normal(size=200)]
    assert f(vals, 60, 59) is None
    assert f(vals, 60, 0) is None


def test_h0257_rolling_percentile_rank_handles_none_holes(hyp):
    """EDGE CASE: research series carry None for unresolved days."""
    vals = [None] * 5 + [float(i) for i in range(50)] + [None]
    f = hyp("h0257").rolling_percentile_rank
    assert f(vals, 20, 40) is not None
    vals2 = [None] * 60
    assert f(vals2, 20, 40) is None


# =========================================================================
# RSI -- h0257
# =========================================================================

def test_h0257_rsi_is_bounded_0_100(hyp, gbm):
    rsi = hyp("h0257").compute_rsi(gbm(n=300, seed=9))
    vals = [v for v in rsi if v is not None]
    assert vals
    assert all(0.0 <= v <= 100.0 for v in vals), (min(vals), max(vals))


def test_h0257_rsi_preserves_series_length(hyp, gbm):
    closes = gbm(n=137, seed=4)
    assert len(hyp("h0257").compute_rsi(closes)) == len(closes)


def test_h0257_rsi_monotone_rise_is_100(hyp):
    """A strictly rising series has zero losses -> RSI pinned at 100."""
    rsi = hyp("h0257").compute_rsi([100.0 + i for i in range(60)])
    assert rsi[-1] == pytest.approx(100.0)


def test_h0257_rsi_monotone_fall_is_0(hyp):
    rsi = hyp("h0257").compute_rsi([200.0 - i for i in range(60)])
    assert rsi[-1] == pytest.approx(0.0, abs=1e-9)


def test_h0257_rsi_short_input_is_all_none(hyp):
    out = hyp("h0257").compute_rsi([1.0, 2.0, 3.0], period=14)
    assert out == [None, None, None]


def test_h0257_returns_first_element_is_none(hyp, gbm):
    """The first bar has no predecessor; a 0.0 there would be a fabricated
    return and would drag every downstream mean toward zero."""
    r = hyp("h0257").compute_returns(gbm(n=50, seed=2))
    assert r[0] is None
    assert all(v is not None for v in r[1:])


# =========================================================================
# entry gates -- h0209, h0248
# =========================================================================

def test_h0248_compute_metrics_reads_nothing_at_or_after_the_as_of_bar(hyp, bars):
    """LOOK-AHEAD GUARD -- the primary one the brief asks for.

    h0248.compute_metrics(bars_list, target_date) computes T-1/T-21/T-60 features
    for decision date target_date. Every input at index >= idx(target_date) is
    future information. Overwrite the entire tail with absurd values; a correct
    as-of implementation returns bit-identical metrics.
    """
    f = hyp("h0248").compute_metrics
    rows = bars(n=400, shape="ts_close_vol", seed=13)
    idx = 300
    target = rows[idx][0]

    clean = f(rows, target)
    assert clean is not None, "fixture must reach a computable date"

    poisoned = rows[:idx] + [(ts, 1e9, 1e15) for ts, _, _ in rows[idx:]]
    got = f(poisoned, target)
    assert got is not None
    for k in ("ret_20d", "avg_vol", "vol_20d", "close_t_minus1"):
        assert got[k] == pytest.approx(clean[k], rel=1e-12), (
            f"{k} moved when only bars at/after the as-of index changed: "
            f"{clean[k]} -> {got[k]} (look-ahead)")


def test_h0248_compute_metrics_requires_252_sessions(hyp, bars):
    f = hyp("h0248").compute_metrics
    rows = bars(n=400, shape="ts_close_vol", seed=13)
    assert f(rows, rows[100][0]) is None
    assert f(rows, rows[251][0]) is None
    assert f(rows, rows[300][0]) is not None


def test_h0248_compute_metrics_unknown_date_is_none(hyp, bars):
    rows = bars(n=400, shape="ts_close_vol", seed=13)
    assert hyp("h0248").compute_metrics(rows, 1) is None


def test_h0248_ret_20d_matches_its_definition(hyp, bars):
    """Recompute the headline feature from the raw series independently."""
    f = hyp("h0248").compute_metrics
    rows = bars(n=400, shape="ts_close_vol", seed=13)
    idx = 300
    m = f(rows, rows[idx][0])
    expected = rows[idx - 1][1] / rows[idx - 21][1] - 1
    assert m["ret_20d"] == pytest.approx(expected, rel=1e-12)


def test_h0209_is_valid_entry_is_as_of_only(hyp, bars):
    """LOOK-AHEAD GUARD on the entry gate.

    is_valid_entry(symbol_bars, idx, delisted_at) may read up to idx. Nothing
    after idx may change the decision.
    """
    f = hyp("h0209").is_valid_entry
    rows = bars(n=500, shape="ts_o_c_v", seed=17)
    decisions = [(i, f(rows, i, None)) for i in range(300, 340)]

    poisoned = list(rows)
    for i in range(340, 500):
        ts, o, c, v = poisoned[i]
        poisoned[i] = (ts, o * 10, c * 10, v * 100)
    for i, want in decisions:
        assert f(poisoned, i, None) == want, f"idx={i} decision flipped on future bars"


def test_h0209_is_valid_entry_respects_delisting(hyp, bars):
    """SURVIVORSHIP: no entry may be issued at or after the delisting stamp."""
    f = hyp("h0209").is_valid_entry
    rows = bars(n=500, shape="ts_o_c_v", seed=17)
    idx = 300
    ts_T = rows[idx][0]
    assert f(rows, idx, ts_T) is False
    assert f(rows, idx, ts_T + 1) == f(rows, idx, None)


def test_h0209_is_valid_entry_rejects_insufficient_history(hyp, bars):
    f = hyp("h0209").is_valid_entry
    rows = bars(n=500, shape="ts_o_c_v", seed=17)
    assert f(rows, 0, None) is False
    assert f(rows, 251, None) is False


def test_h0209_is_valid_entry_rejects_penny_stock(hyp, bars):
    """Price gate: a sub-$5 close must never pass, whatever else is true."""
    f = hyp("h0209").is_valid_entry
    rows = bars(n=500, shape="ts_o_c_v", seed=17)
    idx = 300
    ts, o, c, v = rows[idx]
    cheap = list(rows)
    cheap[idx] = (ts, o * 0.01, 4.99, v)
    assert f(cheap, idx, None) is False


def test_h0209_median_volume_gate_uses_a_true_median(hyp, bars):
    """h0209.py:79-81 takes sorted(volumes_20)[10] and calls it 'median of 20
    numbers'. The median of an even-sized sample is the mean of elements 9 and
    10; index 10 alone is the upper median, which biases the 2x-volume gate
    upward and silently rejects borderline entries."""
    rows = bars(n=500, shape="ts_o_c_v", seed=17)
    idx = 300
    vols = sorted(rows[i][3] for i in range(idx - 20, idx))
    upper = vols[10]
    true_median = (vols[9] + vols[10]) / 2
    assert upper == pytest.approx(true_median, rel=1e-9), (
        f"gate uses upper median {upper} where the true median is "
        f"{true_median} (bias {upper - true_median:+.1f})")


# =========================================================================
# design effect / pseudoreplication -- h0221
# =========================================================================

def test_h0221_design_effect_is_at_least_one(hyp, rng):
    """DEFF < 1 would NARROW an interval -- the opposite of what clustering does.
    It is the failure mode that manufactures significance."""
    import datetime as dt
    f = hyp("h0221").compute_design_effect
    for _ in range(200):
        calls = [{"date": dt.date(2024, int(rng.integers(1, 13)), int(rng.integers(1, 28))),
                  "hit": int(rng.integers(0, 2))} for _ in range(int(rng.integers(2, 80)))]
        assert f(calls) >= 1.0


def test_h0221_design_effect_is_monotone_in_within_cluster_agreement(hyp):
    """FINDING (expected to FAIL): DEFF collapses to 1.0 at PERFECT clustering.

    The design effect must rise with within-cluster agreement -- that is its
    entire job: inflate the variance when calls inside a month are not
    independent. Measured (12 months x 20 calls):

        agreement 80/20  -> deff  7.61
        agreement 90/10  -> deff 13.20
        agreement 100/0  -> deff  1.00   <-- collapse

    research/eighty/h0221.py:166-167 computes
        wss += m * p_cluster * (1 - p_cluster)
    which is exactly 0 when every cluster is unanimous, and the next line
        if wss == 0: return 1.0
    then reports NO clustering. So the single most pseudoreplicated case -- one
    outcome repeated across a whole month -- receives the most permissive
    correction available. effective_n = issued / deff is then the raw count,
    and the interval built on it is as narrow as if the calls were independent.

    The wss == 0 guard exists to avoid a div-by-zero in msw = wss/(n-k); the
    correct value there is ICC = 1, i.e. deff = mean_cluster_size.
    """
    import datetime as dt
    f = hyp("h0221").compute_design_effect

    def mk(flip_after):
        return [{"date": dt.date(2024, m, d),
                 "hit": (m % 2) if d <= flip_after else 1 - (m % 2)}
                for m in range(1, 13) for d in range(1, 21)]

    d80, d90, d100 = f(mk(16)), f(mk(18)), f(mk(20))
    assert d90 > d80, (d80, d90)          # holds
    assert d100 >= d90, (
        f"deff collapses from {d90:.2f} at 90% agreement to {d100:.2f} at 100% "
        "-- maximal clustering gets the minimal correction")


def test_h0221_design_effect_iid_clusters_score_about_one(hyp):
    import datetime as dt
    f = hyp("h0221").compute_design_effect
    mixed = [{"date": dt.date(2024, m, d), "hit": d % 2}
             for m in range(1, 13) for d in range(1, 21)]
    assert f(mixed) == pytest.approx(1.0, abs=0.35)


def test_h0221_design_effect_degenerate_inputs(hyp):
    import datetime as dt
    f = hyp("h0221").compute_design_effect
    assert f([]) == 1.0
    assert f([{"date": dt.date(2024, 1, 1), "hit": 1}]) == 1.0
    allhit = [{"date": dt.date(2024, m, 1), "hit": 1} for m in range(1, 13)]
    assert f(allhit) == 1.0  # p_overall == 1, ICC undefined -> no inflation


def test_h0221_design_effect_single_cluster_is_one(hyp):
    import datetime as dt
    f = hyp("h0221").compute_design_effect
    calls = [{"date": dt.date(2024, 3, d), "hit": d % 2} for d in range(1, 20)]
    assert f(calls) == 1.0


# =========================================================================
# feature builder -- h0221.compute_rolling
# =========================================================================

def test_h0221_compute_rolling_features_are_strictly_backward_looking(hyp, bars):
    """LOOK-AHEAD GUARD on the feature builder.

    Every feature at row i (sma_200, vol_20d, median_vol_20, sentiment_5d,
    ret_1d) must depend only on indices <= i. The LABEL fwd_return_20d is
    allowed to read i+20 -- that is the point of a label. So: perturb bars
    strictly after i and assert the FEATURES are unchanged while confirming the
    label DOES move (otherwise the test proves nothing).
    """
    ns = hyp("h0221")
    rows = bars(n=400, shape="d_o_h_l_c_v", seed=23)
    sentiment = {ts: 0.1 * (k % 7) for k, (ts, *_rest) in enumerate(rows)}

    clean = ns.compute_rolling(rows, sentiment)
    assert clean, "fixture must produce rows"

    cut = clean[0]["date"]
    cut_i = [r[0] for r in rows].index(cut)

    poisoned = list(rows)
    for j in range(cut_i + 1, len(rows)):
        ts, o, h, lo, c, v = poisoned[j]
        poisoned[j] = (ts, o, h, lo, c * 3.0, v * 5.0)
    after = ns.compute_rolling(poisoned, sentiment)

    a, b = clean[0], after[0]
    assert a["date"] == b["date"]
    for k in ("close", "sentiment_5d", "ret_1d", "sma_200", "volume",
              "median_vol_20", "vol_20d"):
        assert a[k] == pytest.approx(b[k], rel=1e-12), (
            f"feature {k} at the as-of row moved when only FUTURE bars changed")
    assert a["fwd_return_20d"] != pytest.approx(b["fwd_return_20d"]), (
        "the forward label did not move -- the perturbation was ineffective, "
        "so the look-ahead check above proved nothing")


def test_h0221_compute_rolling_short_history_is_empty(hyp, bars):
    ns = hyp("h0221")
    rows = bars(n=100, shape="d_o_h_l_c_v", seed=23)
    assert ns.compute_rolling(rows, {}) == []


def test_h0221_compute_rolling_never_indexes_past_the_end(hyp, bars):
    """fwd_close = closes[i+20] must stay in range for every emitted row."""
    ns = hyp("h0221")
    rows = bars(n=300, shape="d_o_h_l_c_v", seed=29)
    sentiment = {ts: 0.05 for ts, *_ in rows}
    out = ns.compute_rolling(rows, sentiment)  # must not raise IndexError
    dates = [r[0] for r in rows]
    for r in out:
        assert dates.index(r["date"]) + 20 < len(rows)
