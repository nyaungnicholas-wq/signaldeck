package tombstone

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendWritesOnlyUIDAndHashAndReplaysInOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "deletions.jsonl")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", p)
	if err := Append(7, "ann", " A@B.c ", 1700000000); err != nil {
		t.Fatal(err)
	}
	if err := Append(9, "bo", "", 1700000009); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	// The hash input is pinned byte for byte: normalised address, username and
	// decimal created_ts, joined by newlines.
	h := func(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
	want := `{"uid":7,"account_sha256":"` + h("a@b.c\nann\n1700000000") + `"}` + "\n" +
		`{"uid":9,"account_sha256":"` + h("\nbo\n1700000009") + `"}` + "\n"
	if string(raw) != want {
		t.Fatalf("log =\n%s\nwant\n%s", raw, want)
	}
	if strings.Contains(string(raw), "a@b.c") || strings.Contains(string(raw), "ann") {
		t.Fatal("the log holds the address or the name")
	}
	var got []int64
	if err := Replay(p, func(e Entry) error { got = append(got, e.UID); return nil }); err != nil || len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("replay = %v, %v", got, err)
	}
}

func TestReplayStopsOnAMalformedLine(t *testing.T) {
	h := AccountHash("x@y.z", "x", 1)
	good := `{"uid":1,"account_sha256":"` + h + `"}`
	for name, bad := range map[string]string{
		"extra field":     `{"uid":2,"account_sha256":"` + h + `","email":"x@y.z"}`,
		"no uid":          `{"account_sha256":"` + h + `"}`,
		"short hash":      `{"uid":2,"account_sha256":"abcd"}`,
		"not hex":         `{"uid":2,"account_sha256":"` + strings.Repeat("zz", 32) + `"}`,
		"no hash":         `{"uid":2}`,
		"old format":      `{"uid":2,"email_sha256":"` + h + `","username_sha256":"` + h + `"}`,
		"glued onto torn": `{"uid":2,"acc` + good, // a torn line is skipped, a glued one is not
	} {
		p := filepath.Join(t.TempDir(), "d.jsonl")
		if err := os.WriteFile(p, []byte(good+"\n"+bad+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := Replay(p, func(Entry) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "account_sha256") {
			t.Errorf("%s: err = %v, want a line 2 error naming the expected format", name, err)
		}
	}
	stop := errors.New("stop")
	p := filepath.Join(t.TempDir(), "d.jsonl")
	os.WriteFile(p, []byte(good+"\n"), 0o600) //nolint:errcheck
	if err := Replay(p, func(Entry) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("fn's error was not returned: %v", err)
	}
}

// TestATornRecordIsSkippedAndTheNextAppendStartsAFreshLine: a crash mid-write
// leaves a record with no newline. The next Append must not glue onto it, and
// Replay must skip it (its delete never ran) instead of blocking the whole log.
func TestATornRecordIsSkippedAndTheNextAppendStartsAFreshLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "deletions.jsonl")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", p)
	if err := Append(4, "dee", "d@e.f", 1); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"uid":5,"account_sha2`) //nolint:errcheck
	f.Close()                               //nolint:errcheck
	if err := Append(6, "fay", "f@a.y", 1); err != nil {
		t.Fatal(err)
	}
	var got []int64
	if err := Replay(p, func(e Entry) error { got = append(got, e.UID); return nil }); err != nil || len(got) != 2 || got[0] != 4 || got[1] != 6 {
		t.Fatalf("replay = %v, %v; want [4 6] with the torn record skipped", got, err)
	}
}

func TestAppendFailureNamesNoAddress(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", filepath.Join(blocker, "deletions.jsonl"))
	err := Append(3, "secretname", "secret@example.com", 1)
	if err == nil {
		t.Fatal("append under a regular file succeeded")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("the error carries the address: %v", err)
	}
}
