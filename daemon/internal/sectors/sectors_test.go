package sectors

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestSectorOf(t *testing.T) {
	tests := []struct {
		name   string
		symbol string
		want   string
	}{
		// Technology single names.
		{"aapl tech", "AAPL", "Technology"},
		{"nvda tech", "NVDA", "Technology"},
		{"amd tech", "AMD", "Technology"},
		{"msft tech", "MSFT", "Technology"},
		{"googl tech", "GOOGL", "Technology"},
		{"meta tech", "META", "Technology"},

		// Index / broad-market ETFs.
		{"qqq tech index", "QQQ", "Tech/Index"},
		{"spy broad index", "SPY", "Broad Index"},

		// Consumer / autos.
		{"tsla consumer auto", "TSLA", "Consumer/Auto"},

		// Crypto & crypto-adjacent fintech.
		{"coin crypto fintech", "COIN", "Crypto/Fintech"},
		{"btc crypto", "BTC/USD", "Crypto"},
		{"eth crypto", "ETH/USD", "Crypto"},

		// Financials.
		{"jpm financials", "JPM", "Financials"},
		{"bac financials", "BAC", "Financials"},
		{"gs financials", "GS", "Financials"},

		// Energy.
		{"xom energy", "XOM", "Energy"},
		{"cvx energy", "CVX", "Energy"},

		// Case-insensitive matching.
		{"lowercase aapl", "aapl", "Technology"},
		{"lowercase btc", "btc/usd", "Crypto"},
		{"mixed case coin", "Coin", "Crypto/Fintech"},

		// Fallback.
		{"unknown falls back", "ZZZZ", OtherSector},
		{"empty falls back", "", OtherSector},
		{"unknown lowercase falls back", "wxyz", OtherSector},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SectorOf(tt.symbol); got != tt.want {
				t.Errorf("SectorOf(%q) = %q, want %q", tt.symbol, got, tt.want)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	tests := []struct {
		name string
		in   []Input
		want []SectorAgg
	}{
		{
			name: "empty input safe",
			in:   nil,
			want: []SectorAgg{},
		},
		{
			name: "single symbol single sector",
			in: []Input{
				{Symbol: "AAPL", Score: 90, Ret1M: 0.10},
			},
			want: []SectorAgg{
				{Sector: "Technology", MeanScore: 90, MeanRet1M: 0.10, N: 1, Symbols: []string{"AAPL"}},
			},
		},
		{
			name: "groups and averages within a sector",
			in: []Input{
				{Symbol: "AAPL", Score: 80, Ret1M: 0.10},
				{Symbol: "NVDA", Score: 100, Ret1M: 0.30},
			},
			want: []SectorAgg{
				{Sector: "Technology", MeanScore: 90, MeanRet1M: 0.20, N: 2, Symbols: []string{"AAPL", "NVDA"}},
			},
		},
		{
			name: "sorts strongest sector first",
			in: []Input{
				{Symbol: "XOM", Score: 40, Ret1M: -0.02}, // Energy, weakest
				{Symbol: "AAPL", Score: 95, Ret1M: 0.12}, // Technology, strongest
				{Symbol: "JPM", Score: 60, Ret1M: 0.03},  // Financials, middle
			},
			want: []SectorAgg{
				{Sector: "Technology", MeanScore: 95, MeanRet1M: 0.12, N: 1, Symbols: []string{"AAPL"}},
				{Sector: "Financials", MeanScore: 60, MeanRet1M: 0.03, N: 1, Symbols: []string{"JPM"}},
				{Sector: "Energy", MeanScore: 40, MeanRet1M: -0.02, N: 1, Symbols: []string{"XOM"}},
			},
		},
		{
			name: "unknown symbols bucket into Other",
			in: []Input{
				{Symbol: "ZZZZ", Score: 50, Ret1M: 0.01},
				{Symbol: "WXYZ", Score: 70, Ret1M: 0.05},
			},
			want: []SectorAgg{
				{Sector: OtherSector, MeanScore: 60, MeanRet1M: 0.03, N: 2, Symbols: []string{"ZZZZ", "WXYZ"}},
			},
		},
		{
			name: "multi-sector mixed with crypto and index",
			in: []Input{
				{Symbol: "BTC/USD", Score: 88, Ret1M: 0.20},
				{Symbol: "ETH/USD", Score: 92, Ret1M: 0.24},
				{Symbol: "SPY", Score: 55, Ret1M: 0.02},
			},
			want: []SectorAgg{
				{Sector: "Crypto", MeanScore: 90, MeanRet1M: 0.22, N: 2, Symbols: []string{"BTC/USD", "ETH/USD"}},
				{Sector: "Broad Index", MeanScore: 55, MeanRet1M: 0.02, N: 1, Symbols: []string{"SPY"}},
			},
		},
		{
			name: "equal scores break ties by sector name",
			in: []Input{
				{Symbol: "XOM", Score: 70, Ret1M: 0.05},  // Energy
				{Symbol: "AAPL", Score: 70, Ret1M: 0.05}, // Technology
			},
			want: []SectorAgg{
				{Sector: "Energy", MeanScore: 70, MeanRet1M: 0.05, N: 1, Symbols: []string{"XOM"}},
				{Sector: "Technology", MeanScore: 70, MeanRet1M: 0.05, N: 1, Symbols: []string{"AAPL"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Aggregate(tt.in)
			if got == nil {
				t.Fatalf("Aggregate returned nil slice; want non-nil")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Aggregate len = %d, want %d\n got=%+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				w, g := tt.want[i], got[i]
				if g.Sector != w.Sector {
					t.Errorf("[%d] Sector = %q, want %q", i, g.Sector, w.Sector)
				}
				if !almostEqual(g.MeanScore, w.MeanScore) {
					t.Errorf("[%d] MeanScore = %v, want %v", i, g.MeanScore, w.MeanScore)
				}
				if !almostEqual(g.MeanRet1M, w.MeanRet1M) {
					t.Errorf("[%d] MeanRet1M = %v, want %v", i, g.MeanRet1M, w.MeanRet1M)
				}
				if g.N != w.N {
					t.Errorf("[%d] N = %d, want %d", i, g.N, w.N)
				}
				if !equalStrings(g.Symbols, w.Symbols) {
					t.Errorf("[%d] Symbols = %v, want %v", i, g.Symbols, w.Symbols)
				}
			}
		})
	}
}

func TestRotation(t *testing.T) {
	tests := []struct {
		name string
		prev []SectorAgg
		curr []SectorAgg
		want []Move
	}{
		{
			name: "empty both safe",
			prev: nil,
			curr: nil,
			want: []Move{},
		},
		{
			name: "computes deltas and orders in at top out at bottom",
			prev: []SectorAgg{
				{Sector: "Technology", MeanScore: 80},
				{Sector: "Energy", MeanScore: 60},
				{Sector: "Financials", MeanScore: 50},
			},
			curr: []SectorAgg{
				{Sector: "Technology", MeanScore: 70}, // -10 (rotating out)
				{Sector: "Energy", MeanScore: 75},     // +15 (rotating in)
				{Sector: "Financials", MeanScore: 52}, // +2
			},
			want: []Move{
				{Sector: "Energy", ScoreDelta: 15},
				{Sector: "Financials", ScoreDelta: 2},
				{Sector: "Technology", ScoreDelta: -10},
			},
		},
		{
			name: "missing prev sector treated as zero",
			prev: []SectorAgg{
				{Sector: "Technology", MeanScore: 80},
			},
			curr: []SectorAgg{
				{Sector: "Technology", MeanScore: 82}, // +2
				{Sector: "Crypto", MeanScore: 90},     // +90 from 0
			},
			want: []Move{
				{Sector: "Crypto", ScoreDelta: 90},
				{Sector: "Technology", ScoreDelta: 2},
			},
		},
		{
			name: "missing curr sector treated as zero",
			prev: []SectorAgg{
				{Sector: "Energy", MeanScore: 40}, // -40 to 0
			},
			curr: []SectorAgg{
				{Sector: "Technology", MeanScore: 10}, // +10 from 0
			},
			want: []Move{
				{Sector: "Technology", ScoreDelta: 10},
				{Sector: "Energy", ScoreDelta: -40},
			},
		},
		{
			name: "equal deltas break ties by sector name",
			prev: []SectorAgg{
				{Sector: "Technology", MeanScore: 50},
				{Sector: "Energy", MeanScore: 50},
			},
			curr: []SectorAgg{
				{Sector: "Technology", MeanScore: 55}, // +5
				{Sector: "Energy", MeanScore: 55},     // +5
			},
			want: []Move{
				{Sector: "Energy", ScoreDelta: 5},
				{Sector: "Technology", ScoreDelta: 5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Rotation(tt.prev, tt.curr)
			if got == nil {
				t.Fatalf("Rotation returned nil slice; want non-nil")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Rotation len = %d, want %d\n got=%+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				w, g := tt.want[i], got[i]
				if g.Sector != w.Sector {
					t.Errorf("[%d] Sector = %q, want %q", i, g.Sector, w.Sector)
				}
				if !almostEqual(g.ScoreDelta, w.ScoreDelta) {
					t.Errorf("[%d] ScoreDelta = %v, want %v", i, g.ScoreDelta, w.ScoreDelta)
				}
			}
		})
	}
}

// TestAggregateThenRotation exercises the two functions end to end: aggregate
// two universe snapshots, then diff them.
func TestAggregateThenRotation(t *testing.T) {
	prevIn := []Input{
		{Symbol: "AAPL", Score: 80, Ret1M: 0.10},
		{Symbol: "XOM", Score: 60, Ret1M: 0.02},
	}
	currIn := []Input{
		{Symbol: "AAPL", Score: 70, Ret1M: 0.05},
		{Symbol: "XOM", Score: 75, Ret1M: 0.08},
	}
	moves := Rotation(Aggregate(prevIn), Aggregate(currIn))
	want := []Move{
		{Sector: "Energy", ScoreDelta: 15},
		{Sector: "Technology", ScoreDelta: -10},
	}
	if len(moves) != len(want) {
		t.Fatalf("moves len = %d, want %d", len(moves), len(want))
	}
	for i := range want {
		if moves[i].Sector != want[i].Sector || !almostEqual(moves[i].ScoreDelta, want[i].ScoreDelta) {
			t.Errorf("[%d] = %+v, want %+v", i, moves[i], want[i])
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
