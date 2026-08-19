"""Seal the final holdout BEFORE any search. Hash it so peeking is detectable."""
import hashlib, json, pandas as pd
SPLIT = "2025-03-01"          # search: < SPLIT ; holdout: >= SPLIT
p = pd.read_parquet("panel.parquet")
hold = p[p.day >= SPLIT]
h = hashlib.sha256(pd.util.hash_pandas_object(hold[["symbol_id","day","close"]],
                                              index=False).values.tobytes()).hexdigest()
meta = {"split": SPLIT, "sha256": h,
        "search_days": int(p[p.day < SPLIT].day.nunique()),
        "holdout_days": int(hold.day.nunique()),
        "search_rows": int((p.day < SPLIT).sum()), "holdout_rows": int(len(hold)),
        "rule": "holdout graded EXACTLY ONCE, at the end. Any earlier read is a protocol breach."}
json.dump(meta, open("HOLDOUT.json","w"), indent=1)
print(json.dumps(meta, indent=1))
