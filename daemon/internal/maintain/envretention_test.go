package maintain

import (
	"os"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/envcfg"
)

// A mistyped retention window must be REPORTED, not silently replaced. The
// operator meant to keep 10 years of scores; the default keeps 90 days and the
// sweep deletes the rest, irreversibly.
func TestMistypedRetentionIsReportedAsCritical(t *testing.T) {
	envcfg.Reset()
	t.Setenv("SIGNALDECK_SCORES_RETENTION_D", "3650d") // unit suffix: a real typo
	if got := envIntOr("SIGNALDECK_SCORES_RETENTION_D", 90); got != 90 {
		t.Fatalf("runtime behaviour must be unchanged: got %d, want the 90 default", got)
	}
	r := envcfg.Rejected()
	if len(r) != 1 || r[0].Key != "SIGNALDECK_SCORES_RETENTION_D" {
		t.Fatalf("rejection not recorded: %+v", r)
	}
	if !r[0].Critical || !envcfg.HasCritical() {
		t.Errorf("a retention key governs DELETION and must be critical: %+v", r[0])
	}
	if r[0].Raw != "3650d" || r[0].Using != "90" {
		t.Errorf("must name what was refused and what is running: %+v", r[0])
	}
}

// A VALID override must stay silent, or the signal is noise.
func TestValidRetentionIsSilent(t *testing.T) {
	envcfg.Reset()
	t.Setenv("SIGNALDECK_SCORES_RETENTION_D", "3650")
	if got := envIntOr("SIGNALDECK_SCORES_RETENTION_D", 90); got != 3650 {
		t.Fatalf("a valid override must be honoured: got %d", got)
	}
	if len(envcfg.Rejected()) != 0 {
		t.Errorf("valid override must not be reported: %+v", envcfg.Rejected())
	}
}

// Unset is not a rejection either.
func TestUnsetRetentionIsSilent(t *testing.T) {
	envcfg.Reset()
	// errcheck: the return is deliberately ignored - "already unset" is the
	// state this test wants, so the only error it can report is not a failure.
	_ = os.Unsetenv("SIGNALDECK_SCORES_RETENTION_D")
	if got := envIntOr("SIGNALDECK_SCORES_RETENTION_D", 90); got != 90 {
		t.Fatalf("got %d", got)
	}
	if len(envcfg.Rejected()) != 0 {
		t.Errorf("unset must not be reported: %+v", envcfg.Rejected())
	}
}
