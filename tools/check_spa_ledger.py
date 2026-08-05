"""Objective check for tools/spa_ledger.py output. Run AFTER spa_ledger.py."""
import json
import math
import os
import sqlite3

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
RESULT = os.path.join(HERE, "spa_ledger_result.json")

r = json.load(open(RESULT))

# --- structure ---
for key in ("rules", "spa", "stepm", "bonferroni", "panel"):
    assert key in r, f"missing top-level key {key}"

rules = r["rules"]
assert len(rules) == 48, f"expected 48 rules, got {len(rules)}"

# --- rule ids must match the live ledger exactly ---
db = sqlite3.connect(f"file:{os.path.join(ROOT,'data','signaldeck.db')}?mode=ro", uri=True)
live = {x[0] for x in db.execute("select id from research_loop_hypotheses")}
got = {x["id"] for x in rules}
assert got == live, f"rule id mismatch: missing={live-got} extra={got-live}"

# --- panel sanity ---
p = r["panel"]
assert p["n_weeks"] >= 300, f"only {p['n_weeks']} weeks; corpus should span ~340"
assert p["n_rules"] == 48
# corpus window must match what the loop actually graded
assert p["ts_from"] == 1578459600, f"ts_from {p['ts_from']} != loop obs_ts_from"

# --- every rule needs a finite mean return, a t-stat and a fire count ---
for x in rules:
    for f in ("mean_weekly_return", "t_stat", "p_value", "weeks_fired"):
        assert f in x, f"{x['id']} missing {f}"
        assert x[f] is not None and math.isfinite(x[f]), f"{x['id']}.{f} not finite"
    assert 0 <= x["p_value"] <= 1, f"{x['id']} p_value out of range"
    assert x["weeks_fired"] > 0, f"{x['id']} never fired -- parser bug"

# --- reconstruction vs the ledger's own week counts ---
# NOTE: ledger 'weeks' is NOT "weeks the rule fired". Verified by hand against the
# corpus: rsi_pct>=0.8 fires in all 342 weeks but the ledger records 311, so the Go
# loop applies extra per-week eligibility filtering we are not reproducing. Report
# the gap, do not assert on it. Only the bounds are a real invariant.
led = dict(db.execute("select id, weeks from research_loop_hypotheses"))
for x in rules:
    assert 0 < x["weeks_fired"] <= p["n_weeks"], \
        f"{x['id']} weeks_fired {x['weeks_fired']} outside (0, {p['n_weeks']}]"
gaps = [(x["id"], led[x["id"]], x["weeks_fired"]) for x in rules
        if led[x["id"]] and abs(x["weeks_fired"] - led[x["id"]]) / led[x["id"]] > 0.10]

# --- SPA output ---
spa = r["spa"]
for k in ("pvalue_lower", "pvalue_consistent", "pvalue_upper"):
    assert k in spa and 0 <= spa[k] <= 1, f"bad SPA {k}"
assert spa["pvalue_lower"] <= spa["pvalue_consistent"] <= spa["pvalue_upper"] + 1e-9, \
    "SPA p-values must be ordered lower <= consistent <= upper"
assert spa.get("bootstrap") in ("stationary", "circular", "moving"), "block bootstrap required"
assert spa.get("reps", 0) >= 1000, "need >=1000 bootstrap reps"

# --- StepM output ---
assert isinstance(r["stepm"]["survivors"], list), "stepm.survivors must be a list"
assert set(r["stepm"]["survivors"]) <= got, "stepm survivor not in rule set"

# --- Bonferroni reference must reproduce the loop's live state ---
b = r["bonferroni"]
assert b["divisor"] == 384, f"divisor {b['divisor']} != live 384"
assert abs(b["corrected_alpha"] - 0.05 / 384) < 1e-9, "corrected alpha wrong"
assert b["survivors"] == [] or isinstance(b["survivors"], list)

print("OK", len(rules), "rules |",
      "weeks:", p["n_weeks"],
      "| ledger-week gaps >10%:", len(gaps),
      "| SPA p(consistent) =", round(spa["pvalue_consistent"], 4),
      "| StepM survivors:", len(r["stepm"]["survivors"]),
      "| Bonferroni survivors:", len(b["survivors"]))
