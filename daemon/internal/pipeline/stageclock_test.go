package pipeline

import (
	"strings"
	"testing"
	"time"
)

// This clock exists because worker_runs.detail was the only per-run evidence kept,
// and a 30-minute run said only that it was 30 minutes, which let two investigations
// misread the same rows. The ordering and the 100ms floor are the properties
// that make the line readable at 3am, so they are pinned rather than left to taste.
func TestStageClockNamesTheStageThatSpentTheBudget(t *testing.T) {
	t.Run("reports in execution order, not alphabetically", func(t *testing.T) {
		c := newStageClock()
		c.add("zeta", 2*time.Second)
		c.add("alpha", 3*time.Second)
		s := c.String()
		idxZeta := strings.Index(s, "zeta")
		idxAlpha := strings.Index(s, "alpha")
		if idxZeta == -1 || idxAlpha == -1 || idxZeta > idxAlpha {
			t.Fatalf("expected 'zeta' before 'alpha', got %q", s)
		}
	})

	t.Run("omits stages under the 100ms floor", func(t *testing.T) {
		c := newStageClock()
		c.add("noise", 50*time.Millisecond)
		c.add("real", 5*time.Second)
		s := c.String()
		if !strings.Contains(s, "real") {
			t.Fatalf("expected to contain 'real', got %q", s)
		}
		if strings.Contains(s, "noise") {
			t.Fatalf("expected to omit 'noise' (under 100ms floor), got %q. A detail line listing every trivial stage is one nobody reads.", s)
		}
	})

	t.Run("an all-trivial clock renders empty", func(t *testing.T) {
		c := newStageClock()
		c.add("tiny", time.Millisecond)
		s := c.String()
		if s != "" {
			t.Fatalf("expected empty string for all-trivial clock, got %q", s)
		}
	})

	t.Run("double stop adds the time once", func(t *testing.T) {
		c := newStageClock()
		stop := c.at("x")
		stop()
		stop() // Call stop twice
		if c.n["x"] != 1 {
			t.Fatalf("expected count for 'x' to be 1 after double stop, got %d", c.n["x"])
		}
	})

	t.Run("a nil clock is safe", func(t *testing.T) {
		var c *stageClock
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("nil clock operation panicked: %v", r)
			}
		}()

		// Test at()
		stopFunc := c.at("x")
		stopFunc() // Should not panic

		// Test add()
		c.add("y", time.Second) // Should not panic

		// Test String()
		s := c.String()
		if s != "" {
			t.Fatalf("expected empty string for nil clock, got %q", s)
		}
	})
}
