package store

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"testing"
)

// walIndex reads the WAL-index header from the -shm file without touching a lock.
func walIndex(t *testing.T, dbPath string) (mxFrame, nBackfill uint32) {
	// Offsets are SQLite's documented WAL-index layout (header mxFrame at 16, WalCkptInfo.nBackfill at 96)
	f, err := os.Open(dbPath + "-shm")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	var buf [120]byte
	if _, err := io.ReadFull(f, buf[:]); err != nil {
		t.Fatal(err)
	}
	mx := binary.LittleEndian.Uint32(buf[16:20])
	nb := binary.LittleEndian.Uint32(buf[96:100])
	return mx, nb
}

// In WAL mode a commit on a connection whose wal_autocheckpoint is on runs a PASSIVE checkpoint of every frame no reader still needs, inline, before the statement returns.
// A read snapshot that pinned the WAL for hours leaves a backlog of millions of frames, and whichever connection commits first after it releases copies the whole backlog inside a one-row write
// (logged live 2026-10-01 as 5-56 s "long write-lock holds" on single-row inserts: InsertDQ 56 s, InsertNews 42 s, InsertSnap1s 27 s).
// The account writer must never be that connection; the main writer keeps checkpointing, so the WAL stays bounded.
func TestAccountWriteDoesNotRunTheCheckpointBacklog(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if _, err := st.w.ExecContext(ctx, "CREATE TABLE ckb_probe (v BLOB)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := st.w.ExecContext(ctx, "INSERT INTO ckb_probe (v) VALUES (x'00')"); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := st.db.QueryContext(ctx, "SELECT v FROM ckb_probe")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("nothing to pin")
	}
	t.Cleanup(func() { _ = rows.Close() })

	for i := 0; i < 1500; i++ {
		if _, err := st.w.ExecContext(ctx, "INSERT INTO ckb_probe (v) VALUES (randomblob(3000))"); err != nil {
			t.Fatal(err)
		}
	}

	mx1, nb1 := walIndex(t, st.path)
	if nb1 >= mx1 || mx1 < 1000 {
		t.Fatalf("no backlog behind the pinned reader (backfill %d of %d frames); the test pinned nothing", nb1, mx1)
	}

	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	release := st.Priority()
	if _, err := st.authW().ExecContext(ctx, "INSERT INTO ckb_probe (v) VALUES (x'01')"); err != nil {
		release()
		t.Fatal(err)
	}
	release()

	mx2, nb2 := walIndex(t, st.path)
	if nb2 != nb1 {
		t.Fatalf("the account write ran the checkpoint backlog inline: backfill %d -> %d of %d frames", nb1, nb2, mx2)
	}

	if _, err := st.w.ExecContext(ctx, "INSERT INTO ckb_probe (v) VALUES (x'02')"); err != nil {
		t.Fatal(err)
	}

	mx3, nb3 := walIndex(t, st.path)
	if nb3 != mx3 {
		t.Fatalf("the main writer's commit left %d of %d frames un-checkpointed; with the account writer opted out, nothing would bound the WAL", mx3-nb3, mx3)
	}

	t.Logf("backlog %d frames behind the reader; the account write left it; a one-row main-writer insert checkpointed %d frames", mx1-nb1, nb3-nb1)
}
