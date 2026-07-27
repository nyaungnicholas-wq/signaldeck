// A store's cache identity must be unique for the lifetime of the PROCESS, not
// for the lifetime of its allocation.
//
// The package-level caches in internal/api are keyed by store identity so that
// one store's aggregation is never served to another. Every one of those keys
// was built with fmt.Sprintf("%p", st) — the store's ADDRESS — and Go reuses
// addresses freely once an allocation is unreachable. Measured 2026-07-27:
// opening and closing 40 stores in sequence produced only 26 distinct addresses,
// so 35% of them inherited a predecessor's cache entries.
//
// Live that is harmless — one process holds one store — and in the test suite it
// is the reason `go test ./internal/api/` failed on a DIFFERENT test roughly one
// run in three ("isolated store leaked: universeN=0", a band computed from
// another test's ledger). A suite that fails at random on tests unrelated to the
// change is a suite whose failures stop being read.
package store

import (
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCacheKeyIsUniquePerInstanceAcrossReuse(t *testing.T) {
	const n = 40
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		st, err := Open(filepath.Join(t.TempDir(), fmt.Sprintf("id%d.db", i)))
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		k := st.CacheKey()
		if k == "" {
			t.Fatal("empty cache key")
		}
		if seen[k] {
			t.Fatalf("store %d reused cache key %q from an earlier, closed store — "+
				"a package-level cache would serve it the dead store's entries", i, k)
		}
		seen[k] = true
		_ = st.Close()
		// Make the address genuinely available for reuse, which is the whole
		// point: an identity that survives this is an identity.
		runtime.GC()
	}
	if len(seen) != n {
		t.Fatalf("%d distinct keys across %d stores", len(seen), n)
	}
}

// A reader clone shares the parent's data and must therefore share nothing of
// the parent's cache identity by ACCIDENT — it gets its own, so a clone's
// entries and the parent's cannot silently merge.
func TestReaderCloneHasItsOwnCacheKey(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "clone.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close() //nolint:errcheck
	clone, err := st.ReaderClone(2)
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close() //nolint:errcheck
	if clone.CacheKey() == st.CacheKey() {
		t.Fatalf("clone and parent share cache key %q", st.CacheKey())
	}
}
