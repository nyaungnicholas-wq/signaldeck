package tombstone

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendWritesOnlyUIDAndHashAndReplaysInOrder(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "deletions.jsonl")
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", p)
	if err := Append(7, " A@B.c "); err != nil {
		t.Fatal(err)
	}
	if err := Append(9, ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	want := `{"uid":7,"email_sha256":"` + EmailHash("a@b.c") + `"}` + "\n" +
		`{"uid":9,"email_sha256":"` + EmailHash("") + `"}` + "\n"
	if string(raw) != want {
		t.Fatalf("log =\n%s\nwant\n%s", raw, want)
	}
	if strings.Contains(string(raw), "a@b.c") {
		t.Fatal("the log holds the address")
	}
	var got []int64
	if err := Replay(p, func(e Entry) error { got = append(got, e.UID); return nil }); err != nil || len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("replay = %v, %v", got, err)
	}
}

func TestReplayStopsOnAMalformedLine(t *testing.T) {
	good := `{"uid":1,"email_sha256":"` + EmailHash("x@y.z") + `"}`
	for name, bad := range map[string]string{
		"extra field": `{"uid":2,"email_sha256":"` + EmailHash("x@y.z") + `","email":"x@y.z"}`,
		"no uid":      `{"email_sha256":"` + EmailHash("x@y.z") + `"}`,
		"short hash":  `{"uid":2,"email_sha256":"abcd"}`,
		"not hex":     `{"uid":2,"email_sha256":"` + strings.Repeat("zz", 32) + `"}`,
		"torn":        `{"uid":2,"email_sha2`,
	} {
		p := filepath.Join(t.TempDir(), "d.jsonl")
		if err := os.WriteFile(p, []byte(good+"\n"+bad+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := Replay(p, func(Entry) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "line 2") {
			t.Errorf("%s: err = %v, want a line 2 error", name, err)
		}
	}
	stop := errors.New("stop")
	p := filepath.Join(t.TempDir(), "d.jsonl")
	os.WriteFile(p, []byte(good+"\n"), 0o600) //nolint:errcheck
	if err := Replay(p, func(Entry) error { return stop }); !errors.Is(err, stop) {
		t.Fatalf("fn's error was not returned: %v", err)
	}
}

func TestAppendFailureNamesNoAddress(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SIGNALDECK_TOMBSTONE_LOG", filepath.Join(blocker, "deletions.jsonl"))
	err := Append(3, "secret@example.com")
	if err == nil {
		t.Fatal("append under a regular file succeeded")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("the error carries the address: %v", err)
	}
}
