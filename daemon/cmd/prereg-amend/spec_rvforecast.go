package main

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/nyaungnicholas-wq/signaldeck/internal/volprereg"
)

// RVForecastKind registers the HAR volatility forward test. A NEW kind, so it
// can never be read as an amendment to an existing claim -- the registrar's
// amendment path keys on the latest hash per kind, and prereg seq 87 must stay
// exactly where it is.
const RVForecastKind = volprereg.RVForecastKind

// rvForecastState is what the chain records about the world at filing time.
type rvForecastState struct {
	// Resolved is the number of rv_forecasts rows that already carry an
	// outcome. It MUST be zero. A forward test registered after its own
	// results are readable is not a registration, and an append-only log
	// cannot tell the two apart afterwards.
	Resolved int
	// Frozen is the number of forecasts already written but not yet resolved.
	// Non-zero is allowed and is disclosed rather than hidden: the registered
	// start rule excludes them, because the window opens at the first forecast
	// STRICTLY AFTER this record.
	Frozen int
	// TableExists is false before the schema that carries it has ever run.
	// That is the cleanest possible filing position -- nothing can predate the
	// registration if nothing has ever been forecast.
	TableExists bool
}

func measureRVForecast(ctx context.Context, db *sql.DB) (rvForecastState, error) {
	var m rvForecastState

	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='rv_forecasts'`).Scan(&n)
	if err != nil {
		return m, err
	}
	if n == 0 {
		// No table means no forecast has ever been made. Zero resolved is not
		// an assumption here, it is the only possibility.
		return m, nil
	}
	m.TableExists = true

	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rv_forecasts WHERE actual IS NOT NULL`).Scan(&m.Resolved); err != nil {
		return m, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rv_forecasts WHERE actual IS NULL`).Scan(&m.Frozen); err != nil {
		return m, err
	}
	return m, nil
}

// rvForecastSpec renders the frozen registration as the chain's spec_json.
//
// The document is internal/volprereg.Registration(), which is where every
// registered choice actually lives; this only serialises it and staples on the
// measured state at filing time. specDigest is volprereg's own byte-stable
// canonical hash, carried INSIDE the record so a later reader can detect a
// drift between the filed text and the package that produced it -- the chain's
// own spec_hash is a hash of this JSON, which is a different question.
func rvForecastSpec(m rvForecastState) string {
	s := volprereg.Registration()

	doc := map[string]any{
		"testId":               s.TestID,
		"question":             s.Question,
		"estimand":             s.Estimand,
		"horizons":             s.Horizons,
		"model":                s.Model,
		"nulls":                s.Nulls,
		"losses":               s.Losses,
		"control":              s.Control,
		"unitOfObservation":    s.UnitOfObservation,
		"testStatistic":        s.TestStatistic,
		"headline":             s.Headline,
		"familySize":           s.FamilySize,
		"looksSpent":           s.LooksSpent,
		"minDistinctDays":      s.MinDistinctDays,
		"minSymbolsPerDay":     s.MinSymbolsPerDay,
		"startRule":            s.StartRule,
		"decisionRule":         s.DecisionRule,
		"knownWeakness":        s.KnownWeakness,
		"whatThisCannotChange": s.WhatThisCannotChange,
		"backtestAtFiling":     s.BacktestAtFiling,
		"specDigest":           s.Hash(),
		"measuredAtFiling": map[string]any{
			"rvForecastsTableExists":        m.TableExists,
			"forecastsResolved":             m.Resolved,
			"forecastsFrozenNotYetResolved": m.Frozen,
		},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		// Unreachable for this shape, and a panic is correct: filing a record
		// whose body failed to serialise would put a lie on an append-only log.
		panic("rvForecastSpec: " + err.Error())
	}
	return string(b)
}

const rvForecastNote = "Registers the HAR realized-variance forward test " +
	"(har-rv-2026-09) BEFORE any forecast it grades has resolved. Two horizons, " +
	"three nulls, two losses and two outcome proxies are all fixed here, with ONE " +
	"named as the headline, because reporting whichever of 24 cells clears the bar " +
	"is the selection effect that retired every previous predictor on this " +
	"platform. Two exploratory looks were spent before the harness existed and are " +
	"charged to the multiplicity divisor; the first of them LOST to the EWMA null. " +
	"The backtest known at filing is recorded verbatim in the spec and is labelled " +
	"a backtest, not evidence for the live claim. ESTIMATOR ARTIFACT is one of the " +
	"four registered verdicts: if the headline clears its bar but the " +
	"construction-independent control does not, the advantage is smoothing rather " +
	"than forecasting and the claim is withdrawn automatically."
