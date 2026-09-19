package workers

import (
	"testing"
	"time"
)

func TestDailyAtETCatchUp(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	et := func(y int, m time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, m, d, h, mi, s, 0, ny)
	}
	tests := []struct {
		name string
		last time.Time
		now  time.Time
		want time.Time
	}{
		{
			name: "missed yesterday slot",
			last: et(2026, time.September, 8, 9, 0, 5),
			now:  et(2026, time.September, 9, 10, 0, 0),
			want: et(2026, time.September, 9, 10, 0, 0),
		},
		{
			name: "ran today slot",
			last: et(2026, time.September, 9, 9, 0, 5),
			now:  et(2026, time.September, 9, 10, 0, 0),
			want: et(2026, time.September, 10, 9, 0, 0),
		},
		{
			name: "before today slot",
			last: et(2026, time.September, 8, 9, 0, 5),
			now:  et(2026, time.September, 9, 8, 0, 0),
			want: et(2026, time.September, 9, 9, 0, 0),
		},
		{
			name: "zero last",
			last: time.Time{},
			now:  et(2026, time.September, 9, 10, 0, 0),
			want: et(2026, time.September, 10, 9, 0, 0),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := DailyAtETCatchUp(tt.last, tt.now, 9, 0)
			if !got.Equal(tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestWeeklyAtETCatchUp(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	et := func(y int, m time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, m, d, h, mi, s, 0, ny)
	}
	tests := []struct {
		name string
		last time.Time
		now  time.Time
		want time.Time
	}{
		{
			name: "missed weekly slot",
			last: et(2026, time.August, 31, 19, 21, 0),
			now:  et(2026, time.September, 7, 20, 0, 0),
			want: et(2026, time.September, 7, 20, 0, 0),
		},
		{
			name: "ran weekly slot",
			last: et(2026, time.September, 5, 9, 0, 10),
			now:  et(2026, time.September, 7, 20, 0, 0),
			want: et(2026, time.September, 12, 9, 0, 0),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := WeeklyAtETCatchUp(tt.last, tt.now, time.Saturday, 9, 0)
			if !got.Equal(tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestTradingDayAtETCatchUp(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	et := func(y int, m time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, m, d, h, mi, s, 0, ny)
	}
	tests := []struct {
		name string
		last time.Time
		now  time.Time
		want time.Time
	}{
		{
			name: "missed trading day slot after holiday",
			last: et(2026, time.September, 3, 18, 45, 2),
			now:  et(2026, time.September, 8, 17, 0, 0),
			want: et(2026, time.September, 8, 17, 0, 0),
		},
		{
			name: "ran trading day slot",
			last: et(2026, time.September, 4, 18, 45, 2),
			now:  et(2026, time.September, 8, 17, 0, 0),
			want: et(2026, time.September, 8, 18, 45, 0),
		},
		{
			name: "weekend before trading day",
			last: et(2026, time.September, 4, 18, 45, 2),
			now:  et(2026, time.September, 6, 12, 0, 0),
			want: et(2026, time.September, 8, 18, 45, 0),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := TradingDayAtETCatchUp(tt.last, tt.now, 18, 45)
			if !got.Equal(tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

func TestDailyAtETCatchUpAcrossDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	et := func(y int, m time.Month, d, h, mi, s int) time.Time {
		return time.Date(y, m, d, h, mi, s, 0, ny)
	}
	tests := []struct {
		name string
		last time.Time
		now  time.Time
		want time.Time
	}{
		{
			name: "DST end missed slot",
			last: et(2026, time.October, 31, 9, 0, 3),
			now:  et(2026, time.November, 1, 12, 0, 0),
			want: et(2026, time.November, 1, 12, 0, 0),
		},
		{
			name: "DST end ran slot",
			last: et(2026, time.November, 1, 9, 0, 3),
			now:  et(2026, time.November, 1, 12, 0, 0),
			want: et(2026, time.November, 2, 9, 0, 0),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := DailyAtETCatchUp(tt.last, tt.now, 9, 0)
			if !got.Equal(tt.want) {
				t.Errorf("%s: got %v, want %v", tt.name, got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}
