package marketdata

import "testing"

func TestSettleDay(t *testing.T) {
	tests := []struct {
		name   string
		settle int64
		ts     int64
		want   int64
	}{
		{"unknown settle falls back to the calendar day", 0, 1786232400, TradingDay(1786232400)},
		{"negative settle is treated as unknown", -1, 1786232400, TradingDay(1786232400)},
		{"known settle relabels through TradingDay", 1784001600, 1784042580, TradingDay(1784001600)},
		{"settle wins over ts when both are known", 1784001600, 1784042580, TradingDay(1784001600)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SettleDay(tt.settle, tt.ts); got != tt.want {
				t.Errorf("SettleDay(%d, %d) = %d, want %d", tt.settle, tt.ts, got, tt.want)
			}
		})
	}
}

func TestSettleDayCollapsesAWeekend(t *testing.T) {
	const fridayBar = 1784001600 // the settled move all three resolve against
	friPred := int64(1784042580) // Friday, after the close
	satPred := friPred + 86400
	sunPred := friPred + 2*86400

	buckets := map[int64]struct{}{
		SettleDay(fridayBar, friPred): {},
		SettleDay(fridayBar, satPred): {},
		SettleDay(fridayBar, sunPred): {},
	}
	if len(buckets) != 1 {
		t.Errorf("expected all three weekend predictions to settle to the same Friday bar, got %d distinct buckets", len(buckets))
	}

	phantom := map[int64]struct{}{
		TradingDay(friPred): {},
		TradingDay(satPred): {},
		TradingDay(sunPred): {},
	}
	if len(phantom) != 3 {
		t.Errorf("expected 3 phantom observations under old TradingDay behavior, got %d", len(phantom))
	}
}
