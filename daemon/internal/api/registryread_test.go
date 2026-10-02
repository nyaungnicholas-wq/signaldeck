package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// The grader rewrites data/accuracy_registry.json in place (open "w" then
// json.dump; its script is hash-pinned, so it cannot be made atomic there), and
// a reader can catch it half-written. These pin the reader-side answer.

const tornRegistry = `{"graded_at": "2026-10-01T14:05:18", "refused_since": null, "rows": [`

// setRegistryWait swaps what one retry wait does, for the length of the test.
func setRegistryWait(t *testing.T, wait func()) {
	t.Helper()
	old := registryRetryWait
	registryRetryWait = wait
	t.Cleanup(func() { registryRetryWait = old })
}

// The writer finishes while the reader waits: the second read parses.
func TestLoadRegistryRereadsATornFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "accuracy_registry.json")
	if err := os.WriteFile(p, []byte(tornRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	waits := 0
	setRegistryWait(t, func() {
		waits++
		if err := os.WriteFile(p, []byte(tornRegistry+`]}`), 0o600); err != nil {
			t.Fatal(err)
		}
	})
	reg, err := loadRegistry(p)
	if err != nil {
		t.Fatalf("a torn read that the writer then finished must parse on re-read: %v", err)
	}
	if reg.GradedAt != "2026-10-01T14:05:18" || waits != 1 {
		t.Fatalf("graded_at=%q after %d waits, want the finished file after 1", reg.GradedAt, waits)
	}
}

// A registry that stays broken is believed after the retries, and says so.
func TestLoadRegistryPersistentlyBadStillFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "accuracy_registry.json")
	if err := os.WriteFile(p, []byte(tornRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	waits := 0
	setRegistryWait(t, func() { waits++ })
	if _, err := loadRegistry(p); !errors.Is(err, errRegistryUnparsed) {
		t.Fatalf("persistently bad registry: err=%v, want errRegistryUnparsed", err)
	}
	if waits != registryParseRetries {
		t.Fatalf("waited %d times, want %d re-reads before giving up", waits, registryParseRetries)
	}
}

// A track-record refusal over an unparsed registry is served, but must not be
// cached: the SWR cache (and its disk copy) would otherwise hold a torn read's
// refusal until the next rebuild. The next request reads again and publishes.
func TestTrackRecordParseErrorRefusalIsNotCached(t *testing.T) {
	sd30Off(t) // with SD-30 on, 1d is withheld whatever the registry says
	setRegistryWait(t, func() {})
	st, err := store.Open(filepath.Join(t.TempDir(), "torn.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sym, err := st.UpsertSymbol(context.Background(), "TRN", md.Stocks, "")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	base := int64(store.GradingEpochTS)
	for di := int64(0); di < 40; di++ {
		prob, fwd := 0.8, 0.02
		if di%2 == 1 {
			prob, fwd = 0.2, -0.02
		}
		seedResolvedPrediction(t, st, sym.ID, md.H1d, base+di*86400, prob, fwd)
	}
	p := filepath.Join(t.TempDir(), "accuracy_registry.json")
	if err := os.WriteFile(p, []byte(tornRegistry), 0o600); err != nil {
		t.Fatal(err)
	}
	d := Deps{St: st, RegistryPath: p}

	resp, err := d.cachedTrackRecord(context.Background(), md.H1d)
	if err != nil {
		t.Fatalf("cachedTrackRecord: %v", err)
	}
	note, _ := resp["note"].(string)
	if resp["gated"] != true || resp["winRate"] != nil || !strings.Contains(note, "did not parse") {
		t.Fatalf("an unparsed registry must still refuse (gated=%v winRate=%v note=%q)", resp["gated"], resp["winRate"], note)
	}
	if err := os.WriteFile(p, []byte(tornRegistry+`]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err = d.cachedTrackRecord(context.Background(), md.H1d)
	if err != nil {
		t.Fatalf("cachedTrackRecord: %v", err)
	}
	if resp["gated"] != false || resp["winRate"] == nil {
		t.Fatalf("the parse-error refusal was cached: the next read did not rebuild (gated=%v note=%v)", resp["gated"], resp["note"])
	}
}
