#!/usr/bin/env python3
"""db-manifest.py DB - a comparable fingerprint of one SQLite file, for cutover.

Run it on the PC against the final snapshot and on the VM against the restored
copy, then `diff` the two outputs: any difference means the VM does not hold
that exact snapshot. Read-only (mode=ro), stdlib only. It prints counts and
hashes, never row contents, so the output is safe to keep in a ticket.

  sha256        of the file bytes
  user_version  PRAGMA user_version
  schema        sha256 of sqlite_master (type, name, tbl_name, sql), sorted
  ledger_head   prediction_ledger: max(seq), count(*), entry_hash at max(seq)
  anchor_head   ledger_anchors: max(seq), its ledger_seq and head_hash
  table <name>  row count, one line per table, sorted by name

`python3 db-manifest.py __selfcheck` builds two tiny databases and prints
SELFCHECK OK when equal files match and a one-row change is caught.
"""
import hashlib
import os
import shutil
import sqlite3
import sys
import tempfile
import urllib.parse


def manifest(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    uri = "file:" + urllib.parse.quote(os.path.abspath(path)) + "?mode=ro"
    con = sqlite3.connect(uri, uri=True)
    try:
        out = ["sha256 " + h.hexdigest()]
        out.append("user_version %d" % con.execute("PRAGMA user_version").fetchone()[0])
        rows = con.execute(
            "select type, name, tbl_name, coalesce(sql, '') from sqlite_master order by type, name"
        ).fetchall()
        out.append("schema " + hashlib.sha256(repr(rows).encode("utf-8")).hexdigest())
        tables = [r[1] for r in rows if r[0] == "table" and not r[1].startswith("sqlite_")]
        if "prediction_ledger" in tables:
            seq, n = con.execute("select max(seq), count(*) from prediction_ledger").fetchone()
            head = con.execute("select entry_hash from prediction_ledger where seq=?", (seq,)).fetchone()
            out.append("ledger_head seq=%s count=%s hash=%s" % (seq, n, head[0] if head else ""))
        if "ledger_anchors" in tables:
            a = con.execute(
                "select seq, ledger_seq, head_hash from ledger_anchors order by seq desc limit 1"
            ).fetchone()
            out.append("anchor_head %s" % ("seq=%s ledger_seq=%s hash=%s" % a if a else "none"))
        for t in sorted(tables):
            n = con.execute('select count(*) from "%s"' % t.replace('"', '""')).fetchone()[0]
            out.append("table %s %d" % (t, n))
        return out
    finally:
        con.close()


def selfcheck():
    d = tempfile.mkdtemp()
    a, b = os.path.join(d, "a b.db"), os.path.join(d, "b.db")
    for p in (a, b):
        con = sqlite3.connect(p)
        con.execute("create table prediction_ledger (seq integer primary key, entry_hash text)")
        con.execute("create table ledger_anchors (seq integer primary key, ledger_seq int, head_hash text)")
        con.execute("insert into prediction_ledger values (1, 'h1')")
        con.execute("insert into ledger_anchors values (1, 1, 'h1')")
        con.commit()
        con.close()
    with open(a, "rb") as f, open(b, "wb") as g:
        g.write(f.read())  # byte-identical copy
    assert manifest(a) == manifest(b), "identical files must match"
    assert "ledger_head seq=1 count=1 hash=h1" in manifest(a)
    con = sqlite3.connect(b)
    con.execute("insert into prediction_ledger values (2, 'h2')")
    con.commit()
    con.close()
    diff = [x for x in manifest(b) if x not in manifest(a)]
    assert any(x.startswith("table prediction_ledger 2") for x in diff), diff
    assert any(x.startswith("sha256 ") for x in diff), diff
    shutil.rmtree(d, ignore_errors=True)
    print("SELFCHECK OK")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("usage: db-manifest.py DB | __selfcheck")
    if sys.argv[1] == "__selfcheck":
        selfcheck()
    else:
        print("\n".join(manifest(sys.argv[1])))
