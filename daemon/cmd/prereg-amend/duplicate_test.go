package main

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func dupDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.TempDir()+"/d.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	if _, err := db.Exec(`CREATE TABLE prereg_records (
		seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER, ts_nanos INTEGER,
		kind TEXT, spec_json TEXT, spec_hash TEXT, prev_hash TEXT,
		entry_hash TEXT, note TEXT)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func file(t *testing.T, db *sql.DB, kind string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO prereg_records (ts, kind, spec_json, spec_hash, prev_hash, entry_hash, note)
		 VALUES (1, ?, '{}', 'h', 'p', 'e', 'n')`, kind); err != nil {
		t.Fatalf("insert: %v", err)
	}
}

// An empty chain must let the first record through, or the guard is
// red-by-construction and nothing can ever be filed.
func TestAlreadyFiled_EmptyChainAllowsTheFirstRecord(t *testing.T) {
	db := dupDB(t)
	dup, _, err := alreadyFiled(context.Background(), db, GradabilityKind)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dup {
		t.Fatal("an empty chain must not report an existing record")
	}
}

// The measured 2026-08-15 gap: after filing seq 75-77, re-running
// gradability-correction or protocol-provenance-correction with -commit would
// have appended a DUPLICATE to an append-only log.
func TestAlreadyFiled_RefusesASecondCopyOfEveryKind(t *testing.T) {
	for _, kind := range []string{
		GradabilityKind, RevisionEpochKind, ProvenanceKind, DataIntegrityKind,
	} {
		db := dupDB(t)
		file(t, db, kind)
		dup, seqs, err := alreadyFiled(context.Background(), db, kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !dup {
			t.Fatalf("%s: a second copy must be refused", kind)
		}
		if seqs == "" {
			t.Fatalf("%s: the refusal must name the existing seq, or the "+
				"operator cannot check what is already on the chain", kind)
		}
	}
}

// A record of a DIFFERENT kind must not block this one.
func TestAlreadyFiled_IsScopedToItsOwnKind(t *testing.T) {
	db := dupDB(t)
	file(t, db, DataIntegrityKind)
	dup, _, err := alreadyFiled(context.Background(), db, GradabilityKind)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if dup {
		t.Fatalf("a %s record must not block %s", DataIntegrityKind, GradabilityKind)
	}
}

// Several copies are all named, so the operator sees the full extent.
func TestAlreadyFiled_NamesEverySeq(t *testing.T) {
	db := dupDB(t)
	file(t, db, ProvenanceKind)
	file(t, db, ProvenanceKind)
	dup, seqs, err := alreadyFiled(context.Background(), db, ProvenanceKind)
	if err != nil || !dup {
		t.Fatalf("dup=%v err=%v", dup, err)
	}
	if seqs != "1,2" {
		t.Fatalf("want both seqs named, got %q", seqs)
	}
}
