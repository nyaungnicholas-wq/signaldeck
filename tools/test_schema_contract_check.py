#!/usr/bin/env python3
"""Tests for tools/schema_contract_check.py.

A gate is only evidence if it can be shown to FAIL on the condition it claims to
detect. These plant a contracted object that the schema does not create and
assert the check reports exactly that, then assert the real repository passes —
in that order, because a check that can only ever pass is indistinguishable from
no check at all.
"""
import os
import sqlite3
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from schema_contract_check import (  # noqa: E402
    DEFAULT_CONTRACT,
    DEFAULT_MIGRATE,
    DEFAULT_SCHEMA,
    apply_migrations,
    check,
    missing_objects,
    parse_contract,
)

GO_TEMPLATE = '''package store

var SchemaContract = map[string][]string{{
	"worker-a": {{
{entries}
	}},
}}
'''


def write(tmp, name, body):
    p = os.path.join(tmp, name)
    with open(p, "w", encoding="utf-8") as f:
        f.write(body)
    return p


class ParseContractTest(unittest.TestCase):
    def test_reads_objects_and_ignores_commented_entries(self):
        src = GO_TEMPLATE.format(entries='\t\t"t.a",\n\t\t// "t.ghost",\n\t\t"t2",')
        self.assertEqual(parse_contract(src), {"worker-a": ["t.a", "t2"]})

    def test_absent_declaration_parses_to_nothing(self):
        self.assertEqual(parse_contract("package store\n"), {})


class MissingObjectsTest(unittest.TestCase):
    def test_reports_absent_table_and_absent_column(self):
        conn = sqlite3.connect(":memory:")
        conn.execute("CREATE TABLE t (a INTEGER)")
        self.assertEqual(
            missing_objects(conn, ["t", "t.a", "t.b", "gone", "gone.x"]),
            ["t.b", "gone", "gone.x"])


class ApplyMigrationsTest(unittest.TestCase):
    def test_alter_add_column_is_applied_once_and_is_idempotent(self):
        conn = sqlite3.connect(":memory:")
        conn.execute("CREATE TABLE t (a INTEGER)")
        go = "x := `ALTER TABLE t ADD COLUMN b INTEGER NOT NULL DEFAULT 0`\n"
        self.assertEqual(apply_migrations(conn, go), [])
        self.assertEqual(apply_migrations(conn, go), [])
        self.assertEqual(missing_objects(conn, ["t.b"]), [])

    def test_data_movement_is_not_executed(self):
        conn = sqlite3.connect(":memory:")
        conn.execute("CREATE TABLE t (a INTEGER)")
        conn.execute("INSERT INTO t VALUES (1)")
        apply_migrations(conn, "y := `DELETE FROM t`\n")
        self.assertEqual(conn.execute("SELECT COUNT(*) FROM t").fetchone()[0], 1)


class CheckTest(unittest.TestCase):
    def test_fails_and_names_an_object_the_schema_does_not_create(self):
        with tempfile.TemporaryDirectory() as tmp:
            schema = write(tmp, "schema.sql", "CREATE TABLE t (a INTEGER);\n")
            contract = write(tmp, "c.go", GO_TEMPLATE.format(
                entries='\t\t"t.a",\n\t\t"t.never_created",'))
            migrate = write(tmp, "m.go", "package store\n")
            code, lines = check(schema, contract, migrate)
        self.assertEqual(code, 1)
        self.assertTrue(any("t.never_created" in l for l in lines), lines)

    def test_a_vacuous_contract_is_a_run_failure_not_a_pass(self):
        with tempfile.TemporaryDirectory() as tmp:
            schema = write(tmp, "schema.sql", "CREATE TABLE t (a INTEGER);\n")
            contract = write(tmp, "c.go", "package store\n")
            migrate = write(tmp, "m.go", "package store\n")
            code, _ = check(schema, contract, migrate)
        self.assertEqual(code, 2)

    def test_the_real_repository_satisfies_its_own_contract(self):
        code, lines = check(DEFAULT_SCHEMA, DEFAULT_CONTRACT, DEFAULT_MIGRATE)
        self.assertEqual(code, 0, "\n".join(lines))


if __name__ == "__main__":
    unittest.main()
