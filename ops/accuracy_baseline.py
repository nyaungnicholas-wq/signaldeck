"""Success criterion for the overnight self-improvement loop on a stock-forecasting system.
Coverage-guarded stats exist because abstention trivially inflates accuracy."""

import sqlite3
import json
import os
import argparse
from datetime import datetime, timezone

def _query_one(conn, sql, params=None):
    """Execute query, return first column of first row or None on error/empty."""
    try:
        cursor = conn.cursor()
        if params:
            cursor.execute(sql, params)
        else:
            cursor.execute(sql)
        row = cursor.fetchone()
        return row[0] if row else None
    except sqlite3.Error:
        return None

def compute_stats(db_path: str) -> dict:
    """Compute 15 accuracy stats from the database."""
    stats = {}
    
    with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as conn:
        # Prediction outcome stats for each horizon
        for horizon in ("1d", "1w"):
            # Base CTE for deduplication
            cte = """
            WITH deduped AS (
                SELECT symbol_id, horizon, date(ts, 'unixepoch') as day,
                       avg(prob) as prob, max(up) as up
                FROM prediction_outcomes
                WHERE prob IS NOT NULL AND up IS NOT NULL
                GROUP BY symbol_id, horizon, day
            )"""
            
            # ELIGIBLE and ISSUED counts
            eligible = _query_one(conn, 
                f"{cte} SELECT count(*) FROM deduped WHERE horizon = ?",
                (horizon,))
            issued = _query_one(conn,
                f"{cte} SELECT count(*) FROM deduped WHERE horizon = ? AND prob != 0.5",
                (horizon,))
            
            # Coverage
            coverage = (issued / eligible) if eligible and eligible > 0 else None
            stats[f"coverage_{horizon}"] = {
                "value": float(issued) if issued else None,
                "higher_is_better": True,
                "unit": "",
                "_count_total": eligible  # temp for coverage calc
            }
            # Replace with actual coverage value
            stats[f"coverage_{horizon}"] = {
                "value": float(coverage) if coverage is not None else None,
                "higher_is_better": True,
                "unit": ""
            }
            
            # N count
            stats[f"n_{horizon}"] = {
                "value": float(issued) if issued else 0.0,
                "higher_is_better": True,
                "unit": ""
            }
            
            if issued and issued > 0:
                # Accuracy (mean correctness over ISSUED)
                acc = _query_one(conn,
                    f"{cte} SELECT AVG(CASE WHEN (prob > 0.5 AND up = 1) OR "
                    "(prob < 0.5 AND up = 0) THEN 1.0 ELSE 0.0 END) "
                    "FROM deduped WHERE horizon = ? AND prob != 0.5",
                    (horizon,))
                stats[f"acc_{horizon}"] = {
                    "value": float(acc) if acc is not None else None,
                    "higher_is_better": True,
                    "unit": "",
                    "guard_by": f"coverage_{horizon}"
                }
                
                # Naive baseline (majority class on ISSUED)
                avg_up = _query_one(conn,
                    f"{cte} SELECT AVG(up) FROM deduped WHERE horizon = ? AND prob != 0.5",
                    (horizon,))
                naive = max(avg_up, 1.0 - avg_up) if avg_up is not None else None
                stats[f"naive_{horizon}"] = {
                    "value": float(naive) if naive is not None else None,
                    "higher_is_better": True,
                    "unit": ""
                }
                
                # Lift
                lift = (acc - naive) if acc is not None and naive is not None else None
                stats[f"lift_{horizon}"] = {
                    "value": float(lift) if lift is not None else None,
                    "higher_is_better": True,
                    "unit": "",
                    "guard_by": f"coverage_{horizon}"
                }
                
                # Brier score
                brier = _query_one(conn,
                    f"{cte} SELECT AVG((prob - up) * (prob - up)) "
                    "FROM deduped WHERE horizon = ? AND prob != 0.5",
                    (horizon,))
                stats[f"brier_{horizon}"] = {
                    "value": float(brier) if brier is not None else None,
                    "higher_is_better": False,
                    "unit": ""
                }
            else:
                # No issued predictions for this horizon
                for stat in ("acc", "naive", "lift", "brier"):
                    key = f"{stat}_{horizon}"
                    guard = "guard_by" if stat in ("acc", "lift") else None
                    entry = {
                        "value": None,
                        "higher_is_better": stat != "brier",
                        "unit": ""
                    }
                    if guard:
                        entry["guard_by"] = f"coverage_{horizon}"
                    stats[key] = entry
        
        # Structural outcomes stats
        structural_graded = _query_one(conn,
            "SELECT COUNT(CASE WHEN correct IS NOT NULL THEN 1 END) "
            "FROM regime_outcomes")
        structural_total = _query_one(conn,
            "SELECT COUNT(*) FROM regime_outcomes")
        stats["structural_graded_frac"] = {
            # `is not None`, not truthiness: zero graded rows is a real 0.0, and
            # reporting it as "no data" would hide the surface coming online.
            "value": (structural_graded / structural_total)
                     if structural_graded is not None and structural_total
                     else None,
            "higher_is_better": True,
            "unit": ""
        }
        
        structural_acc = _query_one(conn,
            "SELECT AVG(correct) FROM regime_outcomes WHERE correct IS NOT NULL")
        stats["structural_acc"] = {
            "value": float(structural_acc) if structural_acc is not None else None,
            "higher_is_better": True,
            "unit": ""
        }
        
        structural_lift = _query_one(conn,
            "SELECT AVG(correct) - AVG(CASE WHEN naive_label = actual THEN 1.0 ELSE 0.0 END) "
            "FROM regime_outcomes WHERE correct IS NOT NULL AND actual IS NOT NULL "
            "AND naive_label IS NOT NULL")
        stats["structural_lift"] = {
            "value": float(structural_lift) if structural_lift is not None else None,
            "higher_is_better": True,
            "unit": ""
        }
    
    return stats

def progress(baseline: dict, current: dict, target_pct: float = 10.0,
             guard_tol_pct: float = 1.0) -> dict:
    """Compute progress from baseline to current, applying guards."""
    result = {}
    
    for key in set(baseline.keys()) & set(current.keys()):
        base_val = baseline[key].get("value")
        curr_val = current[key].get("value")
        higher_is_better = current[key].get("higher_is_better", 
                                           baseline[key].get("higher_is_better", True))
        
        # Compute pct_change
        pct_change = None
        if base_val is not None and curr_val is not None and base_val != 0:
            if higher_is_better:
                pct_change = (curr_val - base_val) / abs(base_val) * 100
            else:
                pct_change = (base_val - curr_val) / abs(base_val) * 100
        
        # Check met
        # Tolerance, not a bare >=: a stat landing exactly on target computes to
        # 9.999999999999986 and would otherwise be denied by float noise.
        met = pct_change is not None and pct_change >= target_pct - 1e-9
        
        # Apply guard
        blocked_by = None
        guard_stat = current[key].get("guard_by", baseline[key].get("guard_by"))
        if guard_stat and met:
            # Compute guard stat's pct_change
            guard_base = baseline.get(guard_stat, {}).get("value")
            guard_curr = current.get(guard_stat, {}).get("value")
            guard_hib = current.get(guard_stat, {}).get("higher_is_better",
                                                       baseline.get(guard_stat, {}).get("higher_is_better", True))
            
            if guard_base is not None and guard_curr is not None and guard_base != 0:
                if guard_hib:
                    guard_pct = (guard_curr - guard_base) / abs(guard_base) * 100
                else:
                    guard_pct = (guard_base - guard_curr) / abs(guard_base) * 100
                
                if guard_pct < -guard_tol_pct:
                    met = False
                    blocked_by = guard_stat
        
        result[key] = {
            "baseline": float(base_val) if base_val is not None else None,
            "current": float(curr_val) if curr_val is not None else None,
            "pct_change": float(pct_change) if pct_change is not None else None,
            "target_pct": target_pct,
            "met": met,
            "blocked_by": blocked_by
        }
    
    return result

def write_snapshot(db_path: str, out_path: str) -> dict:
    """Compute stats and write snapshot JSON."""
    stats = compute_stats(db_path)
    doc = {
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "db": db_path,
        "stats": stats
    }
    os.makedirs(os.path.dirname(out_path) or ".", exist_ok=True)
    with open(out_path, "w") as f:
        json.dump(doc, f, indent=2)
    return doc

def main():
    parser = argparse.ArgumentParser(description="Accuracy baseline and reporting")
    parser.add_argument("--db", default="data/signaldeck.db")
    parser.add_argument("--baseline", default="ops/accuracy_baseline.json")
    parser.add_argument("--progress-out", default="ops/accuracy_progress.json")
    parser.add_argument("--target", type=float, default=10.0)
    parser.add_argument("--guard-tol", type=float, default=1.0)
    parser.add_argument("--snapshot", action="store_true")
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--report", action="store_true")
    parser.add_argument("--require-goal", action="store_true")
    parser.add_argument("--json", action="store_true")
    args = parser.parse_args()
    
    # Default behavior
    if not args.snapshot and not args.report:
        args.report = os.path.exists(args.baseline)
        if not args.report:
            args.snapshot = True
    
    if args.snapshot:
        if os.path.exists(args.baseline) and not args.force:
            print("Baseline already exists. Use --force to overwrite.", file=sys.stderr)
            return 2
        write_snapshot(args.db, args.baseline)
        return 0
    
    if args.report:
        if not os.path.exists(args.baseline):
            print("Baseline file not found.", file=sys.stderr)
            return 2
        
        with open(args.baseline) as f:
            baseline_doc = json.load(f)
        baseline_stats = baseline_doc["stats"]
        
        current_stats = compute_stats(args.db)
        prog = progress(baseline_stats, current_stats, args.target, args.guard_tol)
        
        if args.json:
            print(json.dumps(prog, indent=2))
        else:
            # Print aligned table
            print(f"{'STAT':<20} {'BASELINE':>10} {'CURRENT':>10} {'CHANGE%':>10} {'MET':>15}")
            print("-" * 65)
            
            met_count = 0
            total_count = 0
            for key in sorted(prog.keys()):
                entry = prog[key]
                base = f"{entry['baseline']:.4f}" if entry['baseline'] is not None else "-"
                curr = f"{entry['current']:.4f}" if entry['current'] is not None else "-"
                change = f"{entry['pct_change']:.2f}" if entry['pct_change'] is not None else "-"
                
                if entry['met']:
                    met_str = "TRUE"
                    met_count += 1
                elif entry['blocked_by']:
                    met_str = f"blocked:{entry['blocked_by']}"
                else:
                    met_str = "FALSE"
                
                total_count += 1
                print(f"{key:<20} {base:>10} {curr:>10} {change:>10} {met_str:>15}")
            
            print(f"\n{met_count}/{total_count} stats at or above +{args.target}%")
        
        # Write progress JSON
        with open(args.progress_out, "w") as f:
            json.dump(prog, f, indent=2)
        
        if args.require_goal and met_count < total_count:
            return 1
        return 0
    
    return 0

if __name__ == "__main__":
    import sys
    sys.exit(main())