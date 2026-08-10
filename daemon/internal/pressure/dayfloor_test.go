package pressure

import (
	"errors"
	"testing"
)

// THE FLOOR IS IN TRADING DAYS AND MUST BE REACHABLE BY THE REAL RECORD.
//
// Until 7afccf6 (2026-08-05) the feature store returned every re-scoring of a
// symbol — ~39 rows per symbol-day — so a 60-row floor was ~1.5 days of
// evidence. That commit deduplicated to ONE ROW PER TRADING DAY, correctly, and
// the same constant silently became a 60-TRADING-DAY floor against a record
// whose best-covered symbol carries 34. Every symbol has returned
// ErrInsufficientData since, the legs were vetoed for want of a fresh grade, and
// the raw cross-section collapsed to 31 distinct values across 329 symbols.
//
// This pins the floor against the live record's actual coverage so the same
// silent unit change cannot happen twice.
func TestFloorIsReachableByTheRealRecord(t *testing.T) {
	const bestCoveredSymbolDays = 34 // measured on the live 1d record, 2026-08-08

	if minSamples > bestCoveredSymbolDays {
		t.Fatalf("minSamples=%d exceeds the %d trading days the best-covered symbol "+
			"carries; nothing can ever grade", minSamples, bestCoveredSymbolDays)
	}
	// The per-fold product must not re-impose the old floor by another route.
	const folds = 3 // pipeline.pressureFolds
	if folds*minPerFold > bestCoveredSymbolDays {
		t.Errorf("folds*minPerFold = %d exceeds the %d days available; the fold gate "+
			"re-imposes a floor nothing can meet", folds*minPerFold, bestCoveredSymbolDays)
	}

	// A symbol AT the floor must actually grade.
	samples := make([]Sample, minSamples)
	for i := range samples {
		up := 0
		if i%2 == 0 {
			up = 1 // both classes present, or AUC is undefined
		}
		samples[i] = Sample{Ts: int64(1786000000 + i*86400), Pressure: float64(i%7)/7 - 0.5, Up: up}
	}
	if _, err := Evaluate(samples, folds); err != nil {
		t.Errorf("a symbol with exactly minSamples=%d days did not grade: %v", minSamples, err)
	}

	// And one below it must still be refused — the floor is a floor.
	if _, err := Evaluate(samples[:minSamples-1], folds); !errors.Is(err, ErrInsufficientData) {
		t.Errorf("below the floor did not return ErrInsufficientData, got %v", err)
	}
}
