// Prediction attribution (Layer 6) — the pipeline-side writer that turns one
// ledgered prediction's inputs into persisted top-N attribution parts.
//
// The parts decompose the RAW blended probability's delta from the 0.5
// neutral prior (the exact quantity ensemble.WeightedProbability produced for
// this ledger row), per ensemble.AttributeProbability's contract: comp_*
// pressure components split exactly; every other leg is one part. The GBM leg
// is currently attributed at LEG granularity here: the live blend consumes a
// STORED model-forecast probability (gbm-trainer's row) and no trained model
// is persisted, so per-feature Saabas contributions cannot be reproduced at
// predict time without retraining. ensemble.AttributeProbability and
// gbm.Attribute already support the per-feature split — wiring it is one call
// in the trainer (persist gbm.Attribute's parts alongside the forecast row)
// once that file is free to edit; the method label stays "saabas" so readers
// of mixed eras see one contract.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ensemble"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// attributionTopN is how many parts are persisted per ledgered prediction,
// largest |contribution| first.
const attributionTopN = 8

// WriteLedgerAttribution computes and persists the top-N attribution parts
// for one just-ledgered prediction. Exported as the one-line wiring hook for
// wherever predictions are ledgered:
//
//	pipeline.WriteLedgerAttribution(ctx, st, led.Seq, symbolID, h, ts, sc, c, wts)
//
// Best-effort by design (mirrors the ledger append itself): a failure logs +
// records a dq event but must never fail the prediction.
func WriteLedgerAttribution(ctx context.Context, st *store.Store, ledgerSeq, symbolID int64,
	h md.Horizon, ts int64, sc md.Score, c ensemble.Components, weights map[string]float64) {

	// Pressure comp_* split: mirror buildFeatureVector's rule — weight-0
	// components are informational and carry no contribution, so only the
	// contributing comps enter (their Contribs sum to the score by definition).
	var comps []ensemble.AttributionPart
	for _, comp := range sc.Components {
		if comp.Weight == 0 {
			continue
		}
		comps = append(comps, ensemble.AttributionPart{Name: "comp_" + comp.Name, Contribution: comp.Contrib})
	}
	parts := ensemble.TopParts(ensemble.AttributeProbability(c, weights, comps, nil), attributionTopN)
	if len(parts) == 0 {
		return
	}
	rows := make([]store.PredictionAttribution, 0, len(parts))
	for i, p := range parts {
		rows = append(rows, store.PredictionAttribution{
			LedgerSeq: ledgerSeq, Rank: i, SymbolID: symbolID, Horizon: h, Ts: ts,
			Name: p.Name, Kind: p.Kind, Contribution: p.Contribution,
			Method: store.AttributionMethodSaabas,
		})
	}
	if err := st.InsertPredictionAttributions(ctx, rows); err != nil {
		slog.Warn("prediction attribution: persist failed", "seq", ledgerSeq, "err", err)
		sid := symbolID
		_ = st.InsertDQ(ctx, md.DQEvent{
			SymbolID: &sid, Ts: time.Now().Unix(),
			Kind: "attribution_write_error", Detail: fmt.Sprintf("seq %d horizon %s: %v", ledgerSeq, h, err),
		})
	}
}
