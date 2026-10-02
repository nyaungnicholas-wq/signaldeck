package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStopFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".stop-request")
	if err := os.WriteFile(p, nil, 0o644); err != nil { // leftover from an earlier stop
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	ctx := stopOnFile(context.Background(), p, time.Now(), 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if ctx.Err() != nil {
		t.Fatal("a stop file left over at startup must not stop the new process")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("leftover stop file was not removed at startup")
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("stop file did not cancel the context")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("stop file was not consumed")
	}
}

// A stop written while the process was still opening its store (after start,
// before the watcher is armed) is FOR this process: it must cancel, not be
// swept away as a leftover.
func TestStopFileWrittenDuringStartupIsHonoured(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".stop-request")
	start := time.Now().Add(-time.Minute)
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := stopOnFile(context.Background(), p, start, 10*time.Millisecond)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a stop written after process start was deleted instead of honoured")
	}
}
