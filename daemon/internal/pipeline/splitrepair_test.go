package pipeline

import (
	"testing"
	"time"
)

// A split repair must re-fetch the whole stored daily series. With five years
// stored, a two-year refetch left the older three on the pre-split basis.
func TestSplitRepairStartCoversStoredHistory(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	twoYears := now.AddDate(-2, 0, 0)
	five := time.Date(2021, 11, 10, 0, 0, 0, 0, time.UTC)
	if got := splitRepairStart(five.Unix(), now); !got.Equal(five) {
		t.Fatalf("five years stored: start = %s, want %s", got, five)
	}
	recent := now.AddDate(0, -6, 0)
	if got := splitRepairStart(recent.Unix(), now); !got.Equal(twoYears) {
		t.Fatalf("six months stored: start = %s, want the two-year window %s", got, twoYears)
	}
	if got := splitRepairStart(0, now); !got.Equal(twoYears) {
		t.Fatalf("no stored bar: start = %s, want %s", got, twoYears)
	}
}
