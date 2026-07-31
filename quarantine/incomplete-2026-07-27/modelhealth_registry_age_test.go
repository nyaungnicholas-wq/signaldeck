// The kill switch reads the accuracy registry as evidence, and evidence has a
// shelf life. These tests pin the bound: a registry generated well past
// accuracyRegistryMaxAge must announce itself (dq_event + degraded flag) while
// still enforcing its retire flags, and a fresh one must behave exactly as
// before.
package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func writeRegistry(t *testing.T, generated time.Time) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "accuracy_registry.json")
	blob := fmt.Sprintf(`{"generated": %q, "rows": [
		{"predictor": "directional-ensemble (1d)", "family": "direction", "retire": true}
	]}`, generated.Format("2006-01-02T15:04:05"))
	if err := os.WriteFile(path, []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func dqCount(t *testing.T, st *store.Store, kind string) int {
	t.Helper()
	evs, err := st.RecentDQ(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecentDQ: %v", err)
	}
	n := 0
	for _, e := range evs {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestRegistryAgePastBoundDegradesAndRaisesDQ(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mh_stale.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	stamp := time.Now().Add(-accuracyRegistryMaxAge - 6*time.Hour)
	w := &ModelHealthWorker{St: st, RegistryPath: writeRegistry(t, stamp)}

	flags := w.registryRetired()
	// Enforcement is unchanged — dropping the flags would un-retire a model on
	// no new evidence, which is the failure this worker exists to prevent.
	if !flags["directional-ensemble-1d"] {
		t.Error("stale registry stopped enforcing its retire flag; the bound must make expiry visible, not fail open")
	}
	if !w.registryStale {
		t.Errorf("registry generated %s was not marked stale against bound %s", stamp, accuracyRegistryMaxAge)
	}
	if got := dqCount(t, st, "model_health_registry_stale"); got != 1 {
		t.Errorf("dq_events(model_health_registry_stale) = %d, want 1", got)
	}
}

func TestRegistryAgeWithinBoundUnchanged(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "mh_fresh.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	w := &ModelHealthWorker{St: st, RegistryPath: writeRegistry(t, time.Now().Add(-2*time.Hour))}
	flags := w.registryRetired()
	if !flags["directional-ensemble-1d"] {
		t.Error("fresh registry did not retire directional-ensemble-1d")
	}
	if w.registryStale {
		t.Error("a two-hour-old registry was marked stale")
	}
	if got := dqCount(t, st, "model_health_registry_stale"); got != 0 {
		t.Errorf("fresh registry raised %d staleness dq_events, want 0", got)
	}
}

func TestRegistryMissingGeneratedIsTreatedAsExpired(t *testing.T) {
	// Age unknown is not age zero: a registry that cannot say when it was built
	// cannot be certified current.
	got := parseRegistryGenerated("", time.Now())
	if !got.Stale || got.Known {
		t.Errorf("absent generated stamp = %+v, want stale/unknown", got)
	}
	if got := parseRegistryGenerated("not-a-time", time.Now()); !got.Stale {
		t.Errorf("unparseable generated stamp = %+v, want stale", got)
	}
}
