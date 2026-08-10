# DRAFT ONLY -- builds a throwaway fixture from the READ-ONLY backup.
# Writes ONLY to the path given as argv[1]. Never point that at data/signaldeck.db.
"""Build a slim throwaway DB from the READ-ONLY backup: only what the prune touches."""
import os, sqlite3, sys, time

SRC = "file:C:/Users/Nicholas_N/Desktop/claude code/signaldeck/data/backups/signaldeck-20260806-131007.db?mode=ro"
DST = sys.argv[1]
if os.path.exists(DST):
    os.remove(DST)

src = sqlite3.connect(SRC, uri=True)
dst = sqlite3.connect(DST)
cut = int(time.time()) - 60 * 86400

for name in ("symbols", "user_symbols", "bars", "meta"):
    ddl = src.execute(
        "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", (name,)
    ).fetchone()[0]
    dst.execute(ddl)

for tbl, sql, args in (
    ("symbols", "SELECT * FROM symbols", ()),
    ("user_symbols", "SELECT * FROM user_symbols", ()),
    ("bars", "SELECT * FROM bars WHERE tf='1d' AND ts>=?", (cut,)),
    ("meta", "SELECT * FROM meta", ()),
):
    rows = src.execute(sql, args).fetchall()
    if rows:
        ph = ",".join("?" * len(rows[0]))
        dst.executemany(f"INSERT INTO {tbl} VALUES ({ph})", rows)
    print(f"{tbl}: {len(rows)} rows")

dst.commit()
print("active:", dst.execute("SELECT COUNT(*) FROM symbols WHERE active=1").fetchone()[0])
dst.close()
src.close()
