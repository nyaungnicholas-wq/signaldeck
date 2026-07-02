package backtest

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantErr   bool
		wantEntry Rule
		wantExit  Rule
		wantCost  float64
	}{
		{
			name:      "50/200 crossover",
			text:      "50/200 moving-average crossover",
			wantEntry: Rule{Indicator: "sma_cross", Fast: 50, Slow: 200},
			wantExit:  Rule{Indicator: "sma_cross", Fast: 50, Slow: 200},
			wantCost:  10,
		},
		{
			name:      "crossover reversed order normalizes fast<slow",
			text:      "200/50 crossover",
			wantEntry: Rule{Indicator: "sma_cross", Fast: 50, Slow: 200},
			wantExit:  Rule{Indicator: "sma_cross", Fast: 50, Slow: 200},
			wantCost:  10,
		},
		{
			name:      "golden cross phrasing",
			text:      "golden cross 20 100",
			wantEntry: Rule{Indicator: "sma_cross", Fast: 20, Slow: 100},
			wantExit:  Rule{Indicator: "sma_cross", Fast: 20, Slow: 100},
			wantCost:  10,
		},
		{
			name:      "rsi below/above",
			text:      "buy when RSI below 30, sell above 70",
			wantEntry: Rule{Indicator: "rsi", Period: 14, Op: "<", Threshold: 30},
			wantExit:  Rule{Indicator: "rsi", Period: 14, Op: ">", Threshold: 70},
			wantCost:  10,
		},
		{
			name:      "rsi with operators",
			text:      "RSI < 25 buy, > 75 sell",
			wantEntry: Rule{Indicator: "rsi", Period: 14, Op: "<", Threshold: 25},
			wantExit:  Rule{Indicator: "rsi", Period: 14, Op: ">", Threshold: 75},
			wantCost:  10,
		},
		{
			name:      "price vs 200-day",
			text:      "buy above the 200-day, sell below",
			wantEntry: Rule{Indicator: "price_vs_sma", Period: 200, Op: ">"},
			wantExit:  Rule{Indicator: "price_vs_sma", Period: 200, Op: "<"},
			wantCost:  10,
		},
		{
			name:      "price vs sma explicit cost",
			text:      "buy when price is over the 100 day sma, 5 bps cost",
			wantEntry: Rule{Indicator: "price_vs_sma", Period: 100, Op: ">"},
			wantExit:  Rule{Indicator: "price_vs_sma", Period: 100, Op: "<"},
			wantCost:  5,
		},
		{
			name:    "unrecognized",
			text:    "do something clever with the moon phase",
			wantErr: true,
		},
		{
			name:    "empty",
			text:    "   ",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(tt.text)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = no error, want error", tt.text)
				}
				// Error must list the supported forms to be helpful.
				if !strings.Contains(err.Error(), "Supported forms") {
					t.Errorf("error not helpful (no supported-forms list): %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.text, err)
			}
			if s.Entry != tt.wantEntry {
				t.Errorf("Entry = %+v, want %+v", s.Entry, tt.wantEntry)
			}
			if s.Exit != tt.wantExit {
				t.Errorf("Exit = %+v, want %+v", s.Exit, tt.wantExit)
			}
			if s.CostBps != tt.wantCost {
				t.Errorf("CostBps = %v, want %v", s.CostBps, tt.wantCost)
			}
			// Name is preserved (trimmed original).
			if s.Name != strings.TrimSpace(tt.text) {
				t.Errorf("Name = %q, want %q", s.Name, strings.TrimSpace(tt.text))
			}
			// A parsed strategy must survive validation (be runnable).
			if err := validateRule(s.Entry, "entry"); err != nil {
				t.Errorf("parsed Entry fails validation: %v", err)
			}
			if err := validateRule(s.Exit, "exit"); err != nil {
				t.Errorf("parsed Exit fails validation: %v", err)
			}
		})
	}
}

// TestParseThenBacktest is an end-to-end smoke test: a phrase parses into a
// strategy that then runs without error on a plausible series.
func TestParseThenBacktest(t *testing.T) {
	phrases := []string{
		"50/200 moving-average crossover",
		"buy when RSI below 30, sell above 70",
		"buy above the 50-day, sell below",
	}
	// A 260-bar rising-then-falling series so all indicators have data.
	cs := make([]float64, 260)
	for i := range cs {
		if i < 130 {
			cs[i] = 100 + float64(i)
		} else {
			cs[i] = 230 - float64(i-130)
		}
	}
	bars := barsFromCloses(cs...)
	for _, p := range phrases {
		s, err := Parse(p)
		if err != nil {
			t.Fatalf("Parse(%q): %v", p, err)
		}
		if _, err := Backtest(bars, s); err != nil {
			t.Errorf("Backtest of parsed %q: %v", p, err)
		}
	}
}
