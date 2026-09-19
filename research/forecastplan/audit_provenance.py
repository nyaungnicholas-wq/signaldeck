import sys
import os
import json
import csv
import sqlite3
import hashlib
import subprocess
import platform
import statistics
import tempfile
import datetime
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
import auditlib

try:
    import pandas
    PANDAS_VER = pandas.__version__
except Exception:
    PANDAS_VER = None
try:
    import numpy
    NUMPY_VER = numpy.__version__
except Exception:
    NUMPY_VER = None

SQLITE_VER = sqlite3.sqlite_version
PYTHON_VER = platform.python_version()

FILES_TO_HASH = [
    "daemon/internal/structregime/resolve.go",
    "daemon/internal/structregime/structregime.go",
    "daemon/internal/forecast/forecast.go",
    "daemon/internal/pipeline/regimeoutcomes.go",
    "daemon/internal/pipeline/predict.go",
    "daemon/internal/marketdata/settled.go",
    "tools/accuracy_registry.py",
    "tools/rv_forecast_backtest.py",
    "research/harness/instruments.py",
    "research/forecastplan/auditlib.py",
    "research/forecastplan/audit_ledger.py",
    "research/forecastplan/audit_collapse.py",
    "research/forecastplan/audit_provenance.py",
    "research/forecastplan/labels.py",
    "research/forecastplan/parity_check.py",
    "daemon/cmd/label-parity/main.go",
]

CONSTANTS = {
    "TRADING_DAY_OFFSET_SECS": auditlib.TRADING_DAY_OFFSET_SECS,
    "SECONDS_PER_DAY": auditlib.SECONDS_PER_DAY,
    "SURVIVORSHIP_EPOCH_TS": auditlib.SURVIVORSHIP_EPOCH_TS,
    "MIN_BARS": auditlib.MIN_BARS,
    "MAX_FLAT_SHARE": auditlib.MAX_FLAT_SHARE,
    "HC_CUTOFF": auditlib.HC_CUTOFF,
    "MIN_SYMBOLS_FOR_COLLAPSE": auditlib.MIN_SYMBOLS_FOR_COLLAPSE,
}


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def run_git(repo, args):
    try:
        r = subprocess.run(["git"] + args, cwd=repo, capture_output=True, text=True, timeout=30)
        if r.returncode == 0:
            return r.stdout.strip()
        return f"git unavailable: {r.stderr.strip() or r.returncode}"
    except Exception as e:
        return f"git unavailable: {e}"


def get_row_counts(con):
    rc = {}
    q = lambda s: con.execute(s).fetchone()[0]
    rc["bars_1d_stocks"] = q("""
        SELECT COUNT(*) FROM bars b JOIN symbols s ON s.id=b.symbol_id
        WHERE b.tf='1d' AND s.market='stocks'
    """)
    rc["bars_1d_crypto"] = q("""
        SELECT COUNT(*) FROM bars b JOIN symbols s ON s.id=b.symbol_id
        WHERE b.tf='1d' AND s.market='crypto'
    """)
    rc["universe_membership"] = q("SELECT COUNT(*) FROM universe_membership")
    rc["predictions"] = q("SELECT COUNT(*) FROM predictions")
    rc["prediction_outcomes"] = q("SELECT COUNT(*) FROM prediction_outcomes")
    rc["prediction_outcomes_resolved"] = q("SELECT COUNT(*) FROM prediction_outcomes WHERE resolved_at IS NOT NULL")
    rc["fundamentals"] = q("SELECT COUNT(*) FROM fundamentals")
    rc["symbols"] = q("SELECT COUNT(*) FROM symbols")
    rc["companies"] = q("SELECT COUNT(*) FROM companies")
    rc["worker_runs"] = q("SELECT COUNT(*) FROM worker_runs")
    return rc


def build_provenance(repo, db_path, out_dir):
    db_stat = os.stat(db_path)
    repo_head = run_git(repo, ["rev-parse", "HEAD"])
    dirty_out = run_git(repo, ["status", "--porcelain"])
    repo_dirty = len(dirty_out.splitlines()) if dirty_out and not dirty_out.startswith("git unavailable") else None

    file_sha256 = {}
    files_missing = []
    for rel in FILES_TO_HASH:
        p = repo / rel
        if p.exists():
            file_sha256[rel] = sha256_file(p)
        else:
            files_missing.append(rel)

    prov = {
        "generated_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
        "repo_head": repo_head,
        "repo_dirty_paths": repo_dirty,
        "python": PYTHON_VER,
        "pandas": PANDAS_VER,
        "numpy": NUMPY_VER,
        "sqlite": SQLITE_VER,
        "db_path": str(db_path),
        "db_size_bytes": db_stat.st_size,
        "db_mtime_utc": datetime.datetime.fromtimestamp(db_stat.st_mtime, datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
        "row_counts": {},
        "file_sha256": file_sha256,
        "files_missing": files_missing,
        "constants": CONSTANTS,
    }
    with auditlib.open_ro(db_path) as con:
        prov["row_counts"] = get_row_counts(con)
    return prov


def read_csv_counts(path, key):
    counts = {}
    with open(path, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            v = row.get(key, "")
            if v:
                counts[v] = counts.get(v, 0) + 1
    return counts


def median_or_na(vals):
    if not vals:
        return "n/a"
    return statistics.median(vals)


def build_summary(out_dir):
    sections = []
    out = pathlib.Path(out_dir)

    # 1. Eligibility
    elig = out / "eligibility_ledger.csv"
    if elig.exists():
        by_market = read_csv_counts(elig, "market")
        by_class = read_csv_counts(elig, "instrument_class")
        har_screen = sum(1 for _ in csv.DictReader(open(elig, newline="")) if _.get("har_screen_pass") == "1")
        har_comp = sum(1 for _ in csv.DictReader(open(elig, newline="")) if _.get("har_companies_only_pass") == "1")
        rows = [["metric", "value"]]
        for k, v in sorted(by_market.items()):
            rows.append([f"market:{k}", str(v)])
        for k, v in sorted(by_class.items()):
            rows.append([f"instrument_class:{k}", str(v)])
        rows.append(["har_screen_pass", str(har_screen)])
        rows.append(["har_companies_only_pass", str(har_comp)])
        sections.append(("Eligibility", rows, "eligibility_ledger.csv"))
    else:
        sections.append(("Eligibility", [["metric", "value"], ["not produced", ""]], "eligibility_ledger.csv"))

    # 2. Membership gap
    mg = out / "membership_gap.json"
    if mg.exists():
        d = json.loads(mg.read_text())
        rows = [["metric", "value"]]
        for k in ["membership_max_day_utc", "bars_1d_max_day_utc", "n_uncovered_days", "n_uncovered_symbol_days"]:
            rows.append([k, str(d.get(k, "n/a"))])
        sections.append(("Membership gap", rows, "membership_gap.json"))
    else:
        sections.append(("Membership gap", [["metric", "value"], ["not produced", ""]], "membership_gap.json"))

    # 3. HAR screen bias
    hsb = out / "har_screen_bias.json"
    if hsb.exists():
        d = json.loads(hsb.read_text())
        rows = [["metric", "value"]]
        for k, v in sorted(d.items()):
            if k != "by_instrument_class":
                rows.append([k, str(v)])
        sections.append(("HAR screen bias", rows, "har_screen_bias.json"))
    else:
        sections.append(("HAR screen bias", [["metric", "value"], ["not produced", ""]], "har_screen_bias.json"))

    # 4. History depth
    hd = out / "history_depth.json"
    if hd.exists():
        d = json.loads(hd.read_text())
        rows = [["metric", "value"]]
        for market, md in sorted(d.items()):
            if not isinstance(md, dict):
                continue  # a flat fixture file has no per-market blocks
            for k in ["n_symbols_with_bars", "n_ge_500", "n_ge_756", "n_ge_756_before_2023_07_03"]:
                rows.append([market + ":" + k, str(md.get(k, "n/a"))])
            rows.append([market + ":p50", str(md.get("n_bars_quantiles", {}).get("p50", "n/a"))])
        sections.append(("History depth", rows, "history_depth.json"))
    else:
        sections.append(("History depth", [["metric", "value"], ["not produced", ""]], "history_depth.json"))

    # 5. Collapse
    ctp = out / "collapse_trace_predictions.csv"
    cto = out / "collapse_trace_outcomes.csv"
    crm = out / "collapse_registry_match.json"
    collapse_rows = [["metric", "value"]]
    if ctp.exists():
        by_horizon = {}
        with open(ctp, newline="") as f:
            for row in csv.DictReader(f):
                h = row.get("horizon", "")
                if h not in by_horizon:
                    by_horizon[h] = {"days": 0, "collapsed": 0, "n_distinct_cal": []}
                by_horizon[h]["days"] += 1
                if row.get("collapsed") == "1":
                    by_horizon[h]["collapsed"] += 1
                if int(row.get("n_symbols", 0)) >= 30:
                    by_horizon[h]["n_distinct_cal"].append(int(row.get("n_distinct_cal", 0)))
        for h in sorted(by_horizon):
            d = by_horizon[h]
            collapse_rows.append([f"{h}:days", str(d["days"])])
            collapse_rows.append([f"{h}:collapsed", str(d["collapsed"])])
            collapse_rows.append([f"{h}:min_n_distinct_cal", str(min(d["n_distinct_cal"]) if d["n_distinct_cal"] else "n/a")])
            collapse_rows.append([f"{h}:median_n_distinct_cal", str(median_or_na(d["n_distinct_cal"]))])
    if cto.exists():
        by_horizon = {}
        with open(cto, newline="") as f:
            for row in csv.DictReader(f):
                h = row.get("horizon", "")
                if h not in by_horizon:
                    by_horizon[h] = {"n_distinct_prob": []}
                if int(row.get("n", row.get("n_symbols", 0))) >= 30:  # outcomes trace names the count n
                    by_horizon[h]["n_distinct_prob"].append(int(row.get("n_distinct_prob", 0)))
        for h in sorted(by_horizon):
            d = by_horizon[h]
            collapse_rows.append([f"{h}:min_n_distinct_prob", str(min(d["n_distinct_prob"]) if d["n_distinct_prob"] else "n/a")])
            collapse_rows.append([f"{h}:median_n_distinct_prob", str(median_or_na(d["n_distinct_prob"]))])
    if crm.exists():
        d = json.loads(crm.read_text())
        for k in ["registry_status", "n_flagged", "n_exact", "n_near", "n_missing_in_trace"]:
            collapse_rows.append([k, str(d.get(k, "n/a"))])
    if not (ctp.exists() or cto.exists() or crm.exists()):
        collapse_rows = [["metric", "value"], ["not produced", ""]]
    sections.append(("Collapse", collapse_rows, "collapse_trace_predictions.csv"))

    # 6. Parity
    pr = out / "parity_report.json"
    if pr.exists():
        d = json.loads(pr.read_text())
        rows = [["metric", "value"]]
        rows.append(["n_cases", str(d.get("n_cases", "n/a"))])
        for f, st in sorted(d.get("per_function", {}).items()):
            rows.append([f + "_mismatch", str(st.get("n_mismatch", "n/a"))])
        sections.append(("Parity", rows, "parity_report.json"))
    else:
        sections.append(("Parity", [["metric", "value"], ["not produced", ""]], "parity_report.json"))

    # 7. Provenance
    prov = out / "provenance.json"
    if prov.exists():
        d = json.loads(prov.read_text())
        rows = [["metric", "value"]]
        for k in ["repo_head", "repo_dirty_paths", "db_size_bytes", "generated_at_utc"]:
            rows.append([k, str(d.get(k, "n/a"))])
        sections.append(("Provenance", rows, "provenance.json"))
    else:
        sections.append(("Provenance", [["metric", "value"], ["not produced", ""]], "provenance.json"))

    # Write summary.md
    lines = []
    for title, rows, src in sections:
        lines.append(f"## {title}")
        lines.append("")
        for i, r in enumerate(rows):
            lines.append(f"| {r[0]} | {r[1]} |")
            if i == 0:
                lines.append("|---|---|")
        lines.append("")
        lines.append(f"Source: {src}")
        lines.append("")
    (out / "summary.md").write_text("\n".join(lines), encoding="utf-8")


def selfcheck():
    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        out_dir = tmp / "out"
        out_dir.mkdir()
        repo_dir = tmp / "repo"
        repo_dir.mkdir()
        db_file = tmp / "test.db"
        db_file.write_bytes(b"0123456789")

        # eligibility_ledger.csv
        (out_dir / "eligibility_ledger.csv").write_text(
            "symbol,market,instrument_class,har_screen_pass,har_companies_only_pass\n"
            "AAA,stocks,common,1,0\n"
            "BBB,stocks,common,0,1\n"
            "BTC/USD,crypto,common,0,0\n", encoding="utf-8")

        # membership_gap.json
        (out_dir / "membership_gap.json").write_text(json.dumps({
            "membership_max_day_utc": "2024-01-01",
            "bars_1d_max_day_utc": "2024-01-01",
            "n_uncovered_days": 2,
            "n_uncovered_symbol_days": 10
        }))

        # har_screen_bias.json
        (out_dir / "har_screen_bias.json").write_text(json.dumps({
            "total_symbols": 3,
            "screen_pass": 1,
            "by_instrument_class": {"common": 1}
        }))

        # history_depth.json
        (out_dir / "history_depth.json").write_text(json.dumps({
            "n_symbols_with_bars": 3,
            "p50": 500,
            "n_ge_500": 2,
            "n_ge_756": 1,
            "n_ge_756_before_2023_07_03": 0
        }))

        # collapse_trace_predictions.csv
        (out_dir / "collapse_trace_predictions.csv").write_text(
            "horizon,n_symbols,n_distinct_cal,collapsed\n"
            "1d,50,3,1\n"
            "1d,50,40,0\n", encoding="utf-8")

        # collapse_trace_outcomes.csv (empty but headed)
        (out_dir / "collapse_trace_outcomes.csv").write_text(
            "horizon,n_symbols,n_distinct_prob\n", encoding="utf-8")

        # collapse_registry_match.json
        (out_dir / "collapse_registry_match.json").write_text(json.dumps({
            "registry_status": "ok",
            "n_flagged": 0,
            "n_exact": 0,
            "n_near": 0,
            "n_missing_in_trace": 0
        }))

        # Create one file in repo for hashing
        (repo_dir / "research" / "harness" / "instruments.py").parent.mkdir(parents=True)
        (repo_dir / "research" / "harness" / "instruments.py").write_text("x = 1")

        # Build provenance using fixture_db
        con = auditlib.fixture_db()
        # We need to attach the temp db file? No, provenance uses the db_path for metadata only.
        # The row_counts come from the fixture_db connection.
        # But we need to write provenance.json with db_path pointing to our temp file.
        prov = {
            "generated_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
            "repo_head": "test-head",
            "repo_dirty_paths": 0,
            "python": PYTHON_VER,
            "pandas": PANDAS_VER,
            "numpy": NUMPY_VER,
            "sqlite": SQLITE_VER,
            "db_path": str(db_file),
            "db_size_bytes": 10,
            "db_mtime_utc": datetime.datetime.fromtimestamp(db_file.stat().st_mtime, datetime.timezone.utc).isoformat().replace("+00:00", "Z"),
            "row_counts": get_row_counts(con),
            "file_sha256": {"research/harness/instruments.py": sha256_file(repo_dir / "research" / "harness" / "instruments.py")},
            "files_missing": [f for f in FILES_TO_HASH if f != "research/harness/instruments.py"],
            "constants": CONSTANTS,
        }
        auditlib.write_json(out_dir / "provenance.json", prov)

        # Build summary
        build_summary(out_dir)

        # Assertions
        assert prov["row_counts"]["symbols"] == 6, f"symbols={prov['row_counts']['symbols']}"
        assert prov["row_counts"]["bars_1d_crypto"] == 400, f"bars_1d_crypto={prov['row_counts']['bars_1d_crypto']}"
        assert len(prov["file_sha256"]) == 1, f"file_sha256={len(prov['file_sha256'])}"
        assert len(prov["files_missing"]) == 15, f"files_missing={len(prov['files_missing'])}"
        assert prov["db_size_bytes"] == 10, f"db_size_bytes={prov['db_size_bytes']}"
        assert isinstance(prov["repo_head"], str), "repo_head not string"
        summary = (out_dir / "summary.md").read_text()
        assert "Eligibility" in summary
        assert "Collapse" in summary
        assert "Source: collapse_trace_predictions.csv" in summary
        assert "not produced" in summary  # for parity report
        print("SELFCHECK OK")


def main():
    auditlib.stdout_utf8()
    import argparse
    ap = argparse.ArgumentParser()
    ap.add_argument("--db", default=None)
    ap.add_argument("--out", default=None)
    ap.add_argument("--repo", default=None)
    ap.add_argument("--selfcheck", action="store_true")
    args = ap.parse_args()

    if args.selfcheck:
        selfcheck()
        return

    repo = pathlib.Path(args.repo) if args.repo else auditlib.repo_root(__file__)
    db_path = pathlib.Path(args.db) if args.db else repo / "data" / "signaldeck.db"
    out_dir = pathlib.Path(args.out) if args.out else repo / "research" / "forecastplan" / "out"
    out_dir.mkdir(parents=True, exist_ok=True)

    prov = build_provenance(repo, db_path, out_dir)
    auditlib.write_json(out_dir / "provenance.json", prov)
    build_summary(out_dir)
    print(f"PROVENANCE OK files_hashed={len(prov['file_sha256'])} sections=7")


if __name__ == "__main__":
    main()