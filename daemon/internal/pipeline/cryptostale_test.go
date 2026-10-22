package pipeline

import (
	"testing"
	"time"
)

func TestCryptoBarStale(t *testing.T) {
	now := time.Date(2026, 10, 21, 23, 59, 0, 0, time.UTC)

	tests := []struct {
		name  string
		barTs int64
		want  bool
	}{
		{
			name:  "yesterday's settled bar, late in the day",
			barTs: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).Unix(), // 47h 59m old
			want:  false,
		},
		{
			name:  "exactly at the bound",
			barTs: now.Add(-72 * time.Hour).Unix(),
			want:  false, // comparison is strictly greater
		},
		{
			name:  "one second past the bound",
			barTs: now.Add(-72*time.Hour - time.Second).Unix(),
			want:  true,
		},
		{
			name:  "frozen feed, a week old",
			barTs: now.AddDate(0, 0, -7).Unix(), // 7 days = 168 hours old
			want:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cryptoBarStale(tc.barTs, now)
			if got != tc.want {
				t.Errorf("case %q: barTs %d: got %t, want %t", tc.name, tc.barTs, got, tc.want)
			}
		})
	}
}