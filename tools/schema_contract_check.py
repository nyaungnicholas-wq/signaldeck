#!/usr/bin/env python3
"""Assert that schema.sql, applied to an EMPTY database, actually creates every
object the worker schema contract names.

WHY THIS EXISTS. daemon/internal/store/schemacontract.go declares, per worker,
the tables and columns that worker must be able to write ("research-loop" needs
research_loop_judgments, and so on), and the daemon refuses to start when an
audit-record worker's objects are absent. But both halves of that guard are Go:
the contract list and the check that reads it live in the same binary, and the
check only ever runs against a database that already exists on the operator's
machine. Nothing asserts that a FRESH database — the one a reviewer, a restore,
or a new deployment gets by applying schema.sql — contains those objects at all.

That gap is not hypothetical for this repo: the defect that produced the whole
contract mechanism was a research pass judging 48 rules and storing nothing,
because the table it wrote to was absent. On the operator's box the tables had
accumulated; a cold clone is the case nobody tested.

So this check builds the cold case on purpose. It creates an empty SQLite file,
applies daemon/internal/store/schema.sql verbatim, then applies the DDL that
migrate() in store.go issues — those two together, and only those two, are what
a fresh database gets — and finally looks up every object named in
SchemaContract. Missing objects are listed by name and the exit status is
non-zero.

migrate()'s DDL is EXTRACTED from store.go rather than re-typed here: several
contracted columns (research_loop_hypotheses.null_p0, .null_weeks) exist only as
ALTER TABLE statements in migrate, so a check that read schema.sql alone would
report a false failure, and a hand-copied migration list would drift from the
real one exactly the way the contract could drift from the schema.

It is one-directional by construction. It reads two source files and writes only
a throwaway database in a temp directory. It cannot repair schema.sql, cannot
touch the production database, and cannot alter any statistic — its only power
is to fail a build.

Exit codes: 0 = every contracted object is created by schema.sql;
            1 = at least one is missing; 2 = the check could not run.

Run: python3 tools/schema_contract_check.py
"""
import argparse
import os
import re
import sqlite3
import sys
import tempfile

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_SCHEMA = os.path.join(REPO, "daemon", "internal", "store", "schema.sql")
DEFAULT_CONTRACT = os.path.join(REPO, "daemon", "internal", "store", "schemacontract.go")
DEFAULT_MIGRATE = os.path.join(REPO, "daemon", "internal", "store", "store.go")

# The DDL forms migrate() issues. Backquoted Go raw strings, possibly spanning
# lines. Only shape-creating statements are extracted: an UPDATE or a DELETE in
# migrate moves data, and this check must never execute data movement.
MIGRATE_DDL_RE = re.compile(
    r"`\s*((?:ALTER\s+TABLE|CREATE\s+TABLE|CREATE\s+INDEX)\b[^`]*)`", re.I | re.S)
ADD_COLUMN_RE = re.compile(
    r"ALTER\s+TABLE\s+(\w+)\s+ADD\s+COLUMN\s+(\w+)", re.I | re.S)

# The Go declaration this check mirrors. Parsed rather than duplicated: a
# hand-copied list here would drift from the contract silently, which is the
# same class of defect the check exists to catch.
CONTRACT_DECL_RE = re.compile(
    r"var\s+SchemaContract\s*=\s*map\[string\]\[\]string\{", re.S)
WORKER_ENTRY_RE = re.compile(r'"([^"]+)"\s*:\s*\{([^}]*)\}', re.S)
QUOTED_RE = re.compile(r'"([^"]+)"')


def _strip_line_comments(src):
    """Drop `//` comments so a commented-out entry is not read as a contract.

    schemacontract.go is heavily annotated and its comments quote object names
    in prose; treating those as contract entries would invent obligations.
    """
    return "\n".join(re.sub(r"//.*$", "", line) for line in src.split("\n"))


def parse_contract(go_src):
    """worker -> list of contracted objects, read out of the Go declaration.

    Returns {} when the declaration is absent, which the caller treats as a
    failure to run rather than as an empty contract: a contract that silently
    parsed to nothing would make this check pass by vacuum.
    """
    src = _strip_line_comments(go_src)
    m = CONTRACT_DECL_RE.search(src)
    if not m:
        return {}
    i = m.end()
    depth = 1
    while i < len(src) and depth > 0:
        if src[i] == "{":
            depth += 1
        elif src[i] == "}":
            depth -= 1
        i += 1
    if depth != 0:
        return {}
    body = src[m.end():i - 1]
    out = {}
    for worker, entries in WORKER_ENTRY_RE.findall(body):
        out[worker] = QUOTED_RE.findall(entries)
    return out


def apply_schema(schema_sql, db_path):
    """Apply schema.sql to a fresh, empty database file."""
    conn = sqlite3.connect(db_path)
    conn.executescript(schema_sql)
    conn.commit()
    return conn


def apply_migrations(conn, go_src):
    """Apply the shape-creating DDL migrate() issues, in source order.

    Mirrors migrate's own guard: an ADD COLUMN whose column already exists is
    skipped rather than executed, so re-declared columns are not an error here
    any more than they are there. A statement that fails for any other reason is
    reported — a migration this check cannot replay is a migration a fresh
    database may not get either.
    """
    errors = []
    for stmt in MIGRATE_DDL_RE.findall(_strip_line_comments(go_src)):
        m = ADD_COLUMN_RE.match(stmt.strip())
        if m:
            table, col = m.group(1), m.group(2)
            have = conn.execute(
                "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
                (table,)).fetchone()
            if not have:
                errors.append(f"{stmt.strip()[:80]}…: table {table} does not exist")
                continue
            if col in {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}:
                continue
        try:
            conn.execute(stmt)
        except sqlite3.Error as e:
            errors.append(f"{' '.join(stmt.split())[:80]}: {e}")
    conn.commit()
    return errors


def missing_objects(conn, objects):
    """Objects ("table" or "table.column") the applied schema does not create."""
    missing = []
    for obj in objects:
        table, _, col = obj.partition(".")
        exists = conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",
            (table,)).fetchone()
        if not exists:
            missing.append(obj)
            continue
        if not col:
            continue
        cols = {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}
        if col not in cols:
            missing.append(obj)
    return missing


def check(schema_path, contract_path, migrate_path):
    """Returns (exit_code, list of report lines)."""
    try:
        with open(schema_path, encoding="utf-8") as f:
            schema_sql = f.read()
        with open(contract_path, encoding="utf-8") as f:
            go_src = f.read()
        with open(migrate_path, encoding="utf-8") as f:
            migrate_src = f.read()
    except OSError as e:
        return 2, [f"schema contract check: cannot read source: {e}"]

    contract = parse_contract(go_src)
    if not contract:
        return 2, [f"schema contract check: no SchemaContract declaration parsed from "
                   f"{contract_path} — refusing to report a vacuous pass"]

    with tempfile.TemporaryDirectory() as tmp:
        db_path = os.path.join(tmp, "fresh.db")
        try:
            conn = apply_schema(schema_sql, db_path)
        except sqlite3.Error as e:
            return 2, [f"schema contract check: applying {schema_path} to an empty "
                       f"database failed: {e}"]
        try:
            mig_errors = apply_migrations(conn, migrate_src)
            if mig_errors:
                return 2, ["schema contract check: migrate() DDL could not be replayed "
                           "on a fresh database:"] + [f"  {e}" for e in mig_errors]
            problems = {}
            for worker in sorted(contract):
                gone = missing_objects(conn, contract[worker])
                if gone:
                    problems[worker] = sorted(gone)
            n_obj = sum(len(v) for v in contract.values())
        finally:
            conn.close()

    if not problems:
        return 0, [f"schema contract: OK — schema.sql + migrate() create all {n_obj} "
                   f"object(s) contracted by {len(contract)} worker(s) on an empty "
                   "database."]

    lines = ["SCHEMA CONTRACT FAILED — schema.sql + migrate() applied to an EMPTY "
             "database do not create every object the worker contract requires:"]
    for worker in sorted(problems):
        lines.append(f"  {worker} is missing: {', '.join(problems[worker])}")
    lines.append("A worker whose objects a fresh database lacks runs, judges, stores "
                 "nothing, and still reports status='ok'. Add the object to schema.sql "
                 "(and to migrate() for existing databases); nothing is repaired here.")
    return 1, lines


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--schema", default=DEFAULT_SCHEMA)
    ap.add_argument("--contract", default=DEFAULT_CONTRACT)
    ap.add_argument("--migrate", default=DEFAULT_MIGRATE,
                    help="Go source holding migrate()'s DDL")
    args = ap.parse_args(argv)
    code, lines = check(args.schema, args.contract, args.migrate)
    out = sys.stdout if code == 0 else sys.stderr
    for line in lines:
        print(line, file=out)
    return code


if __name__ == "__main__":
    sys.exit(main())
