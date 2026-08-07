"""Check for ops/accuracy_baseline.py — the overnight loop's success criterion.

Synthetic DB with hand-computed answers. The teeth here are the anti-gaming
rules: accuracy must be deduped to independent symbol-days, measured on the
ISSUED subset, and any gain that came from abstaining more must not count.

Run: python ops/test_accuracy_baseline.py
"""
import json
import os
import sqlite3
import sys
import tempfile

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import accuracy_baseline as ab  # noqa: E402

DAY1 = 1783036800          # 2026-07-02 00:00 UTC
DAY2 = DAY1 + 86400

SCHEMA = """
create table prediction_outcomes (symbol_id int, horizon text, ts int, prob real,
  up int, fwd_return real, resolved_at int, basis_epoch int);
create table regime_outcomes (id integer primary key, symbol_id int, kind text, ts int,
  day text, horizon_days int, regime text, conviction real, historical_accuracy real,
  rank int, resolved_at int, actual text, correct int, naive_label text, revision int,
  basis_epoch int, superseded_by int);
"""

# 1d: symbol 1 on DAY1 is re-scored 3x -- dedup must collapse it to ONE observation
# with the mean prob (0.8). A row-counting implementation sees 6 obs, not 4, and
# gets a different accuracy; that is the pseudo-replication bug this guards.
PRED_1D = [
    (1, "1d", DAY1, 0.9, 1), (1, "1d", DAY1 + 60, 0.8, 1), (1, "1d", DAY1 + 120, 0.7, 1),
    (2, "1d", DAY1, 0.2, 1),
    (1, "1d", DAY2, 0.6, 0),
    (2, "1d", DAY2, 0.3, 0),
    (3, "1d", DAY1, 0.5, 1),          # prob == 0.5 is an abstention, not a down-call
]
PRED_1W = [
    (1, "1w", DAY1, 0.9, 1),
    (1, "1w", DAY2, 0.1, 1),
]

# Deduped 1d issued set: (0.8,up=1) hit, (0.2,up=1) miss, (0.6,up=0) miss, (0.3,up=0) hit
EXPECTED = {
    "acc_1d": 0.5,
    "naive_1d": 0.5,                                   # issued up-rate 0.5, folded
    "lift_1d": 0.0,
    "brier_1d": (0.04 + 0.64 + 0.36 + 0.09) / 4,       # 0.2825
    "coverage_1d": 4 / 5,                              # 4 issued of 5 eligible
    "n_1d": 4.0,
    "acc_1w": 0.5,
    "naive_1w": 1.0,                                   # issued up-rate 1.0, folded
    "lift_1w": -0.5,
    "brier_1w": (0.01 + 0.81) / 2,                     # 0.41
    "coverage_1w": 1.0,
    "n_1w": 2.0,
    "structural_graded_frac": 0.5,
    "structural_acc": 0.5,
    "structural_lift": 0.0,
}

LOWER_IS_BETTER = {"brier_1d", "brier_1w"}
GUARDED = {"acc_1d": "coverage_1d", "lift_1d": "coverage_1d",
           "acc_1w": "coverage_1w", "lift_1w": "coverage_1w"}


def build_db(path):
    c = sqlite3.connect(path)
    c.executescript(SCHEMA)
    c.executemany("insert into prediction_outcomes (symbol_id,horizon,ts,prob,up) "
                  "values (?,?,?,?,?)", PRED_1D + PRED_1W)
    c.executemany("insert into regime_outcomes (kind,actual,correct,naive_label) "
                  "values (?,?,?,?)",
                  [("trend21", "up", 1, "up"), ("trend21", "down", 0, "up"),
                   ("vol21", None, None, "up"), ("vol21", None, None, None)])
    c.commit()
    c.close()


def main():
    # ignore_cleanup_errors: on Windows a still-open sqlite handle blocks rmtree,
    # which would mask the real assertion results with a PermissionError.
    with tempfile.TemporaryDirectory(ignore_cleanup_errors=True) as tmp:
        db = os.path.join(tmp, "t.db")
        build_db(db)
        stats = ab.compute_stats(db)

        missing = set(EXPECTED) - set(stats)
        assert not missing, f"stats missing: {sorted(missing)}"
        for name, want in EXPECTED.items():
            got = stats[name]["value"]
            assert got is not None, f"{name} is None"
            assert abs(got - want) < 1e-9, f"{name}: got {got!r}, want {want!r}"
            assert stats[name]["higher_is_better"] == (name not in LOWER_IS_BETTER), \
                f"{name}: wrong direction flag"
        for name, guard in GUARDED.items():
            assert stats[name].get("guard_by") == guard, \
                f"{name} must be guarded by {guard}, got {stats[name].get('guard_by')!r}"

        # Missing tables -> None, never a crash.
        bare = os.path.join(tmp, "bare.db")
        sqlite3.connect(bare).close()
        assert ab.compute_stats(bare)["acc_1d"]["value"] is None

        # Rows present but NONE graded is a real 0.0, not "no data" -- this is the
        # live state of the structural surface until its horizons mature, and a
        # truthiness check on the graded count silently reports it as None.
        ung = os.path.join(tmp, "ungraded.db")
        uc = sqlite3.connect(ung)
        uc.executescript(SCHEMA)
        uc.executemany("insert into regime_outcomes (kind,actual,correct,naive_label) "
                       "values (?,?,?,?)", [("trend21", None, None, "up")] * 4)
        uc.commit()
        uc.close()
        us = ab.compute_stats(ung)
        assert us["structural_graded_frac"]["value"] == 0.0, \
            f"0 of 4 graded must be 0.0, got {us['structural_graded_frac']['value']!r}"
        assert us["structural_acc"]["value"] is None      # no graded rows to average

        assert ab.compute_stats(db) == stats, "compute_stats is not deterministic"

        # --- progress math ---
        def s(v, hib=True, guard=None):
            d = {"value": v, "higher_is_better": hib}
            if guard:
                d["guard_by"] = guard
            return d

        base = {"a": s(0.464), "b": s(0.2825, hib=False), "z": s(0.0), "cov": s(0.80)}
        cur = {"a": s(0.5104), "b": s(0.25425, hib=False), "z": s(0.5), "cov": s(0.80)}
        p = ab.progress(base, cur, target_pct=10.0)
        assert abs(p["a"]["pct_change"] - 10.0) < 1e-6 and p["a"]["met"] is True
        # lower-is-better improving must read POSITIVE
        assert abs(p["b"]["pct_change"] - 10.0) < 1e-6 and p["b"]["met"] is True
        # a lower-is-better stat getting WORSE must read negative
        worse = ab.progress({"b": base["b"]}, {"b": s(0.31075, hib=False)}, target_pct=10.0)
        assert worse["b"]["pct_change"] < 0 and worse["b"]["met"] is False
        # zero baseline -> undefined, never a divide-by-zero or a free win
        assert p["z"]["pct_change"] is None and p["z"]["met"] is False

        # --- THE ANTI-GAMING RULE ---
        # +10% accuracy bought by abstaining (coverage 0.80 -> 0.60) must NOT count.
        base2 = {"acc": s(0.464, guard="cov"), "cov": s(0.80)}
        gamed = {"acc": s(0.5104, guard="cov"), "cov": s(0.60)}
        g = ab.progress(base2, gamed, target_pct=10.0)
        assert abs(g["acc"]["pct_change"] - 10.0) < 1e-6, "pct_change itself is unchanged"
        assert g["acc"]["met"] is False, "accuracy bought with coverage must not count as met"
        assert g["acc"].get("blocked_by") == "cov", f"must name the guard: {g['acc']}"

        # Coverage holding steady (or rising) leaves the win intact.
        honest = {"acc": s(0.5104, guard="cov"), "cov": s(0.80)}
        assert ab.progress(base2, honest, target_pct=10.0)["acc"]["met"] is True
        better = {"acc": s(0.5104, guard="cov"), "cov": s(0.85)}
        assert ab.progress(base2, better, target_pct=10.0)["acc"]["met"] is True
        # A hair of coverage noise is tolerated (default tolerance 1% relative).
        noise = {"acc": s(0.5104, guard="cov"), "cov": s(0.7960)}   # -0.5%
        assert ab.progress(base2, noise, target_pct=10.0)["acc"]["met"] is True

        # --- snapshot round trip ---
        snap = os.path.join(tmp, "baseline.json")
        ab.write_snapshot(db, snap)
        with open(snap) as f:
            doc = json.load(f)
        assert "captured_at" in doc and "stats" in doc
        assert doc["stats"]["acc_1d"]["value"] == stats["acc_1d"]["value"]

    print("accuracy_baseline: OK")
    return 0


if __name__ == "__main__":
    sys.exit(main())
