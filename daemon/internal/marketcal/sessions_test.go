package marketcal

import (
	"testing"
	"time"
)

func TestSessionDate(t *testing.T) {
	tests := []struct {
		inUTC time.Time
		want  time.Time
	}{
		{time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 0, 0, 0, 0, Loc())},
		{time.Date(2026, 9, 8, 3, 59, 0, 0, time.UTC), time.Date(2026, 9, 7, 0, 0, 0, 0, Loc())},
	}
	for _, tt := range tests {
		tt := tt
		t.Run("", func(t *testing.T) {
			if got := SessionDate(tt.inUTC); !got.Equal(tt.want) {
				t.Errorf("SessionDate(%v) = %v, want %v", tt.inUTC, got, tt.want)
			}
		})
	}
}

func TestSessionClose(t *testing.T) {
	tests := []struct {
		inNY    time.Time
		wantOK  bool
		wantCls time.Time
	}{
		{time.Date(2026, 11, 27, 12, 0, 0, 0, Loc()), true, time.Date(2026, 11, 27, 13, 0, 0, 0, Loc())},
		{time.Date(2026, 9, 8, 12, 0, 0, 0, Loc()), true, time.Date(2026, 9, 8, 16, 0, 0, 0, Loc())},
		{time.Date(2026, 9, 7, 12, 0, 0, 0, Loc()), false, time.Time{}},
		{time.Date(2026, 9, 6, 12, 0, 0, 0, Loc()), false, time.Time{}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run("", func(t *testing.T) {
			gotCls, gotOK := SessionClose(tt.inNY)
			if gotOK != tt.wantOK {
				t.Errorf("SessionClose(%v).ok = %v, want %v", tt.inNY, gotOK, tt.wantOK)
				return
			}
			if !gotCls.Equal(tt.wantCls) {
				t.Errorf("SessionClose(%v).close = %v, want %v", tt.inNY, gotCls, tt.wantCls)
			}
		})
	}
}

func TestSessionsClosedSince(t *testing.T) {
	tests := []struct {
		barUTC time.Time
		nowUTC time.Time
		want   int
	}{
		{time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 13, 36, 0, 0, time.UTC), 0},
		{time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC), 0},
		{time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 9, 20, 0, 0, 0, time.UTC), 1},
		{time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 9, 19, 59, 0, 0, time.UTC), 0},
		{time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 7, 20, 15, 0, 0, time.UTC), 0},
		{time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC), 1},
		{time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 9, 23, 41, 0, 0, time.UTC), 2},
		{time.Date(2026, 11, 27, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 18, 0, 0, 0, time.UTC), 0},
		{time.Date(2026, 11, 27, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 21, 0, 0, 0, time.UTC), 1},
		{time.Date(2025, 3, 11, 4, 0, 0, 0, time.UTC), time.Date(2026, 9, 9, 23, 41, 0, 0, time.UTC), 60},
		{time.Unix(0, 0), time.Date(2026, 9, 9, 23, 41, 0, 0, time.UTC), 0},
	}
	for _, tt := range tests {
		tt := tt
		t.Run("", func(t *testing.T) {
			if got := SessionsClosedSince(tt.barUTC.Unix(), tt.nowUTC); got != tt.want {
				t.Errorf("SessionsClosedSince(barTs=%v(now=%v), now=%v) = %v, want %v", tt.barUTC, tt.barUTC.Unix(), tt.nowUTC, got, tt.want)
			}
		})
	}
}

func TestDailyBarStale(t *testing.T) {
	tests := []struct {
		barY, barM, barD int
		nowY, nowM, nowD, nowH, nowMins, nowSec int
		want bool
	}{
		{2026, 9, 4, 2026, 9, 8, 5, 0, 0, false},
		{2026, 9, 4, 2026, 9, 9, 5, 0, 0, false},
		{2026, 9, 4, 2026, 9, 10, 5, 0, 0, true},
		{2026, 8, 28, 2026, 8, 31, 5, 0, 0, false},
		{2026, 8, 26, 2026, 8, 28, 23, 0, 0, false},
		{2026, 8, 26, 2026, 8, 29, 9, 0, 0, true},
		{2026, 7, 2, 2026, 7, 6, 9, 0, 0, false},
		{0, 0, 0, 2026, 9, 8, 5, 0, 0, false},
		{2026, 6, 10, 2026, 9, 8, 5, 0, 0, true}, // 90 days before 2026-09-08 is approx 2026-06-10
	}
	for _, tt := range tests {
		tt := tt
		t.Run("", func(t *testing.T) {
			var barTs int64
			if tt.barY == 0 {
				barTs = 0
			} else {
				barTs = time.Date(tt.barY, time.Month(tt.barM), tt.barD, 0, 0, 0, 0, Loc()).Unix()
			}
			now := time.Date(tt.nowY, time.Month(tt.nowM), tt.nowD, tt.nowH, tt.nowMins, tt.nowSec, 0, Loc())
			if got := DailyBarStale(barTs, now); got != tt.want {
				t.Errorf("DailyBarStale(barTs=%v(now=%v), now=%v) = %v, want %v", barTs, time.Unix(barTs, 0).In(Loc()), now, got, tt.want)
			}
		})
	}
}

func TestLoc(t *testing.T) {
	if loc := Loc(); loc == nil {
		t.Error("Loc() returned nil")
	} else if loc.String() != "America/New_York" {
		t.Errorf("Loc() = %v, want America/New_York", loc)
	}
}