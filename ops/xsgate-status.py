#!/usr/bin/env python
"""Report the cross-section gate's live state. Read-only.
    .venv/Scripts/python.exe ops/xsgate-status.py
Says, per horizon, what the gate is judging and what it decided, so the answer
does not depend on getting a shell one-liner's quoting right.
"""
import json, os, sqlite3, sys, datetime as dt

FLOOR = 0.05  # ensemble.MinCrossSectionSpread
MIN_EVIDENCE_N = 30  # pipeline.xsGateMinEvidenceN

# Resolve the database from THIS FILE's location, not the working directory:
# the check is run from wherever the operator happens to be standing, and a
# relative path made it fail the first time it was used from a home directory.
REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DB = os.path.join(REPO, "data", "signaldeck.db")
if not os.path.exists(DB):
    sys.exit("no database at " + DB)
con = sqlite3.connect("file:" + DB.replace("\\", "/") + "?mode=ro", uri=True, timeout=60)
con.execute("PRAGMA busy_timeout=60000")
now = dt.datetime.now(dt.timezone.utc)
today = now.strftime("%Y-%m-%d")
yesterday = (now - dt.timedelta(days=1)).strftime("%Y-%m-%d")
wd = now.strftime("%A")
print("now %s UTC (%s)%s" % (now.strftime("%Y-%m-%d %H:%M"), wd,
      "  MARKET CLOSED - weekend" if wd in ("Saturday", "Sunday") else ""))
for h in ("1d", "1w"):
    row = con.execute("SELECT v FROM meta WHERE k=?", ("crosssection:" + h,)).fetchone()
    if not row:
        print("  %s: no record -> cold start -> PUBLISHES" % h)
        continue
    d = json.loads(row[0])
    n, sp, day = d["n"], d["spread"], d["day"]
    why = None
    if day not in (today, yesterday):
        why = "record is stale (%s) -> not evidence -> PUBLISHES" % day
    elif n < MIN_EVIDENCE_N:
        why = "n=%d below the %d evidence floor -> PUBLISHES" % (n, MIN_EVIDENCE_N)
    elif sp < FLOOR:
        why = "spread %.4f below %.2f -> WITHHELD (flat)" % (sp, FLOOR)
    else:
        why = "spread %.4f clears %.2f -> PUBLISHES" % (sp, FLOOR)
    print("  %s: day=%s n=%d distinct=%d spread=%.4f agreement=%.3f" %
          (h, day, n, d["distinct"], sp, d["agreement"]))
    print("      %s" % why)
con.close()
