package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSidecarSize(t *testing.T) {
	dir := t.TempDir()

	entries := []struct {
		name string
		size int
	}{
		{"signaldeck.db", 100},
		{"signaldeck.db-wal", 200},
		{"signaldeck.db-shm", 300},
		{"signaldeck.db.bak-presplit-20260804", 400},
		{"signaldeck.db.bak-presplit-20260804-wal", 500},
		{"signaldeck.db.premigration", 600},
		{"signaldeck.db.loopsnapshot-shm", 700},
		{"other.db.bak-whatever", 800},
	}
	for _, entry := range entries {
		if err := os.WriteFile(filepath.Join(dir, entry.name), make([]byte, entry.size), 0644); err != nil {
			t.Fatalf("writing %s: %v", entry.name, err)
		}
	}

	backupDir := filepath.Join(dir, "backups")
	if err := os.Mkdir(backupDir, 0755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "backup.bin"), make([]byte, 900), 0644); err != nil {
		t.Fatalf("writing backup file: %v", err)
	}

	livePath := filepath.Join(dir, "signaldeck.db")
	got := sidecarSize(livePath)
	want := int64(400 + 500 + 600 + 700)
	if got != want {
		t.Fatalf("sidecarSize(%q) = %d, want %d", livePath, got, want)
	}

	missingPath := filepath.Join(dir, "nope", "signaldeck.db")
	if got := sidecarSize(missingPath); got != 0 {
		t.Fatalf("sidecarSize(%q) = %d, want 0", missingPath, got)
	}

	tripleDir := t.TempDir()
	for _, name := range []string{"signaldeck.db", "signaldeck.db-wal", "signaldeck.db-shm"} {
		if err := os.WriteFile(filepath.Join(tripleDir, name), make([]byte, 100), 0644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	if got := sidecarSize(filepath.Join(tripleDir, "signaldeck.db")); got != 0 {
		t.Fatalf("sidecarSize(live triple) = %d, want 0", got)
	}
}