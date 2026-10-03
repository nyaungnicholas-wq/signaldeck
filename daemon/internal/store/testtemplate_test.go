package store

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func schemaObjects(t *testing.T, db *sql.DB) []string {
	rows, err := db.Query("SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close() //nolint:errcheck
	var objs []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		objs = append(objs, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	return objs
}

// A seed that clobbers an existing file would wipe a reopen/migration test's
// prepared database before Open ever saw it.
func TestSeedFromTemplateNeverTouchesAnExistingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "existing.db")
	if err := os.WriteFile(p, []byte("not a database"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	seedFromTemplate(p)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "not a database" {
		t.Fatalf("existing file was overwritten (%d bytes now)", len(data))
	}
	if _, err := os.Stat(p + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WAL file should not exist: %v", err)
	}
}

// Goes through Open, so it fails if seeding silently stops (the suite just gets
// slow again, nothing else notices) or if the template stops matching what a
// fresh build produces.
func TestSeedFromTemplateCopiesASchemaCompleteDatabase(t *testing.T) {
	p := filepath.Join(t.TempDir(), "seeded.db")
	st, err := Open(p)
	if err != nil {
		t.Fatalf("open seeded: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// A fresh build leaves its pages in the WAL; a seeded file already holds
	// the whole template in the main file.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat seeded db: %v", err)
	}
	if len(tmplDB) == 0 || fi.Size() < int64(len(tmplDB)) {
		t.Fatalf("Open did not seed from the template: file %d bytes, template %d bytes, build err %v", fi.Size(), len(tmplDB), tmplErr)
	}
	freshPath := filepath.Join(t.TempDir(), "fresh.db")
	fresh, err := open(freshPath)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	got := schemaObjects(t, st.db)
	want := schemaObjects(t, fresh.db)
	if len(want) == 0 {
		t.Fatalf("expected schema objects >0, got 0")
	}
	if !slices.Equal(got, want) {
		var i int
		for i = 0; i < len(got) && i < len(want); i++ {
			if got[i] != want[i] {
				break
			}
		}
		var gotVal, wantVal string
		if i < len(got) {
			gotVal = got[i]
		}
		if i < len(want) {
			wantVal = want[i]
		}
		t.Fatalf("schema mismatch: got %d, want %d; first diff at index %d: got %q, want %q", len(got), len(want), i, gotVal, wantVal)
	}
}

// A relative path (or ":memory:") must never get a file written into the
// package directory.
func TestSeedFromTemplateSkipsRelativePaths(t *testing.T) {
	name := "seedtemplate-relative-must-not-exist.db"
	t.Cleanup(func() {
		_ = os.Remove(name)
		_ = os.Remove(name + "-wal")
	})
	seedFromTemplate(name)
	if _, err := os.Stat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("relative path should not create file: %v", err)
	}
	if _, err := os.Stat(name + "-wal"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("relative path WAL should not exist: %v", err)
	}
}
