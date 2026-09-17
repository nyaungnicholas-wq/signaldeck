// StoreSource adapts the daemon's store to the narrow read-only Source the
// MCP server needs.
//
// The reduction happens HERE, at the edge, and that is the point: a
// store.RegimeForecast carries fields the MCP surface must never see (the raw
// timestamp, the tier, the rank), so it is converted into the package's own
// Verdict rather than passed through. By the time a value reaches a tool it
// has already lost everything it was not supposed to have, and the allowlist
// in layer 3 is the second line rather than the only one.
package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

// StoreSource reads the daemon's SQLite store.
type StoreSource struct{ St *store.Store }

var _ Source = StoreSource{}

// Verdicts returns every current structural forecast, reduced. The MCP server
// caches the result and filters it in memory, so a client-supplied symbol
// never reaches the database.
func (s StoreSource) Verdicts(ctx context.Context) ([]Verdict, error) {
	rows, err := s.St.RegimeForecasts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Verdict, 0, len(rows))
	for _, r := range rows {
		out = append(out, Verdict{
			Symbol:             r.Symbol,
			Kind:               string(r.Kind),
			Regime:             r.Regime,
			Conviction:         r.Conviction,
			HistoricalAccuracy: r.HistoricalAccuracy,
			HorizonDays:        r.HorizonDays,
			N:                  r.N,
			// Day granularity only. The exact write timestamp would tell a
			// client when the worker runs, which is operational detail, and
			// enough of them reconstruct the schedule.
			AsOfDay:      time.Unix(r.Ts, 0).UTC().Format("2006-01-02"),
			Tradeability: structregime.TradeabilityFor(r.Kind, r.Conviction),
			// Shipped verbatim from the package that owns the sentence.
			EvidenceCaveat:  structregime.EvidenceCaveatText(),
			FirstGradableOn: structregime.FirstGradableOnDate(),
		})
	}
	return out, nil
}

// ModelHealth returns the stored verdict blob for a model, or "".
func (s StoreSource) ModelHealth(ctx context.Context, model string) (string, error) {
	return s.St.GetMeta(ctx, "model_health:"+model)
}

// Preregistration returns the frozen claims plus the chain's verification
// state. The full chained records are NOT returned: a client gets each claim's
// content hash, which is what makes verification possible, not the record's
// serialized internals.
func (s StoreSource) Preregistration(ctx context.Context) (PreregSummary, error) {
	recs, err := s.St.PreregRecords(ctx)
	if err != nil {
		return PreregSummary{}, err
	}
	ok, brokenAt, err := s.St.VerifyPrereg(ctx)
	if err != nil {
		return PreregSummary{}, err
	}
	sum := PreregSummary{
		ChainVerified:    ok,
		BrokenAtSeq:      brokenAt,
		RegisteredBefore: true,
		FirstGradableOn:  prereg.FirstGradableOn,
	}
	for _, rec := range recs {
		when := time.Unix(rec.Ts, 0).UTC()
		// Only the records FirstGradableOn actually describes can pull this
		// aggregate down. The machinery records below are skipped from Claims
		// precisely because that date does not govern them, so letting one of
		// them flip RegisteredBefore reported a lateness nobody had measured.
		if before, comparable := prereg.RegisteredBeforeGradable(rec); comparable && !before {
			sum.RegisteredBefore = false
		}
		// Only predictor claims are surfaced. The grading-protocol and
		// auto-retire records describe machinery, not a falsifiable claim, and
		// their payloads have a different shape.
		if rec.Kind == prereg.ProtocolKind || rec.Kind == prereg.RetireRuleKind {
			continue
		}
		var spec prereg.Spec
		if json.Unmarshal([]byte(rec.SpecJSON), &spec) != nil {
			continue
		}
		// A record whose payload does not decode into a predictor claim —
		// another machinery record kind added later, say — deserializes to an
		// empty Spec rather than an error. Emitting it would publish a row of
		// blanks that reads as a claim of nothing, so it is skipped. The chain
		// still covers it; only this view does not render it.
		if spec.Kind == "" || spec.Question == "" {
			continue
		}
		top := 0.0
		for _, b := range spec.Bands {
			if b.Claimed > top {
				top = b.Claimed
			}
		}
		sum.Claims = append(sum.Claims, PreregClaim{
			Kind:         spec.Kind,
			Question:     spec.Question,
			Baseline:     spec.Baseline,
			HorizonDays:  spec.HorizonDays,
			TopBandClaim: top,
			RegisteredOn: when.Format("2006-01-02"),
			SpecHash:     rec.SpecHash,
		})
	}
	return sum, nil
}

// EarliestGradeableOn derives the first gradable date from the outstanding
// structural calls themselves. See the Source interface for why this is
// reported alongside the frozen prereg.FirstGradableOn rather than replacing it.
func (s StoreSource) EarliestGradeableOn(ctx context.Context) (string, bool, error) {
	t, ok, err := s.St.EarliestGradeableAt(ctx)
	if err != nil || !ok {
		return "", false, err
	}
	return t.Format("2006-01-02"), true, nil
}

// EarliestVerdictOn derives the first date a VERDICT can exist — the block gate
// plus the resolution wait — from the calls on disk. See store.EarliestVerdictAt.
func (s StoreSource) EarliestVerdictOn(ctx context.Context) (string, bool, error) {
	t, ok, err := s.St.EarliestVerdictAt(ctx)
	if err != nil || !ok {
		return "", false, err
	}
	return t.Format("2006-01-02"), true, nil
}
