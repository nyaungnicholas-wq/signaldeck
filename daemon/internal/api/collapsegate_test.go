package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// THE TWO PUBLICATION SURFACES MUST NOT BE ABLE TO DISAGREE.
//
// On 2026-08-19 they did. /api/accuracy returned 503 REFUSED with zero rows for
// a window containing 15 collapsed cross-sections across 38 days, while
// ops/accuracy-registry.sh generated the live-accuracy block that README.md and
// eight other documents include — with the full verdict table and no caveat.
// README's own freeze notice asserted the block "currently reads GRADING
// REFUSED" when it in fact carried four verdict rows. One system, two opposite
// answers to "may these numbers be published at all".
//
// The repair was structural: the shell script stopped deciding and started
// asking, through cmd/collapsecheck, which calls the SAME exported
// CollapsedGradingWindow the HTTP handler reaches via d.collapsedGradingWindow.
//
// This file is the adversarial half. A shared function is only shared while
// someone keeps it shared, so these tests drive both paths over a matrix of
// fixtures designed to pull them apart — a clean window, a collapsed one, a
// per-horizon collapse that a single-horizon check would miss, an unreadable
// registry — and assert byte-identical verdicts every time.
//
// MUTATION CHECKS, all verified:
//   - make CollapsedGradingWindow return (\"\", false, nil) unconditionally (the
//     shape a reimplementation-that-drifted takes) → TestGate_BothSurfacesAgree
//     fails on the collapsed and per-horizon cases;
//   - drop the per-horizon loop in collapsedGradingWindow and probe \"1d\" only →
//     the 1w-only collapse case fails;
//   - make the handler publish on a collapsed window →
//     TestGate_ApiRefusalMatchesTheDocumentGate fails with HTTP 200.

// seedResolvedForecasts writes `symbols` resolved outcomes on one day with
// `distinct` distinct probabilities — the exact shape forecastmon grades. A low
// distinct/symbols ratio is a collapsed cross-section: every name got
// effectively the same call, so the day carries one market opinion, not N.
func seedResolvedForecasts(t *testing.T, st *store.Store, horizon md.Horizon, day time.Time, symbols, distinct int) {
	t.Helper()
	ctx := context.Background()
	ts := day.UTC().Truncate(24 * time.Hour).Unix()
	for i := 0; i < symbols; i++ {
		sym, err := st.UpsertSymbol(ctx, fmt.Sprintf("G%s%04d", horizon, i), md.Stocks, "")
		if err != nil {
			t.Fatalf("upsert: %v", err)
		}
		prob := 0.40 + float64(i%distinct)*0.01
		if err := st.UpsertPrediction(ctx, store.Prediction{
			SymbolID: sym.ID, Horizon: horizon, Ts: ts,
			RawProb: prob, CalProb: prob, NUsed: 3, Components: "{}",
		}); err != nil {
			t.Fatalf("upsert prediction: %v", err)
		}
		// Alternating outcome so the day is not degenerate in the OTHER axis:
		// the collapse being measured is in the forecast probabilities, not in
		// what the market did.
		fwd := 0.01
		if i%2 == 1 {
			fwd = -0.01
		}
		if err := st.ResolvePrediction(ctx, sym.ID, horizon, ts, fwd); err != nil {
			t.Fatalf("resolve: %v", err)
		}
	}
}

func registryFor(horizonDays map[string]int) string {
	rows := ""
	for h, d := range horizonDays {
		if rows != "" {
			rows += ","
		}
		rows += fmt.Sprintf(`{"predictor": "directional-ensemble (%s)", "family": "direction", "band": "all",
     "live_n": 900, "live_acc": 0.51, "ci": null, "ci_method": "withheld",
     "distinct_days": %d, "effective_n": null, "null_acc": 0.50,
     "skill": 0.01, "retire": false, "note": "live forward record"}`, h, d)
	}
	return `{
  "graded_at": "2026-08-20T14:05:07",
  "refused_since": null,
  "grader_sha256": "6908c6f9446ab44000e4a1fd3f8e6c325f4300e22d8bf7949deba3732ad8c28f",
  "min_independent_n": 30,
  "min_distinct_blocks": 10,
  "rows": [` + rows + `]
}`
}

// TestGate_BothSurfacesAgree drives the exported function (what the document
// generator reaches through cmd/collapsecheck) and the handler's own method over
// the same fixtures, and requires identical verdicts AND identical reasons.
func TestGate_BothSurfacesAgree(t *testing.T) {
	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name          string
		seed          func(*testing.T, *store.Store)
		registry      map[string]int
		wantCollapsed bool
	}{
		{
			name:          "clean cross-section publishes",
			registry:      map[string]int{"1d": 3},
			wantCollapsed: false,
			seed: func(t *testing.T, st *store.Store) {
				for i := 1; i <= 3; i++ {
					seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
				}
			},
		},
		{
			name:          "collapsed cross-section refuses",
			registry:      map[string]int{"1d": 3},
			wantCollapsed: true,
			seed: func(t *testing.T, st *store.Store) {
				seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -1), 300, 170)
				seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -2), 300, 5) // the collapse
				seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -3), 300, 175)
			},
		},
		{
			name: "collapse confined to 1w still refuses",
			// The defect a single-horizon probe reproduces: 1d is pristine, so a
			// gate that only looks at 1d publishes a 1w table built on one call.
			registry:      map[string]int{"1d": 3, "1w": 3},
			wantCollapsed: true,
			seed: func(t *testing.T, st *store.Store) {
				for i := 1; i <= 3; i++ {
					seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -i), 300, 180)
				}
				seedResolvedForecasts(t, st, md.H1w, now.AddDate(0, 0, -1), 300, 170)
				seedResolvedForecasts(t, st, md.H1w, now.AddDate(0, 0, -2), 300, 4) // the collapse
				seedResolvedForecasts(t, st, md.H1w, now.AddDate(0, 0, -3), 300, 175)
			},
		},
		{
			name:          "no graded days is not a collapse",
			registry:      map[string]int{"1d": 3},
			wantCollapsed: false,
			seed:          func(*testing.T, *store.Store) {},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, st, d := newTestServer(t, nil)
			tc.seed(t, st)
			path := writeRegistry(t, registryFor(tc.registry))
			d.RegistryPath = path

			reg, err := loadRegistry(path)
			if err != nil {
				t.Fatalf("load registry: %v", err)
			}
			apiReason, apiCollapsed, apiErr := d.collapsedGradingWindow(context.Background(), reg, now)
			docReason, docCollapsed, docErr := CollapsedGradingWindow(context.Background(), st, path, now)

			if apiCollapsed != tc.wantCollapsed {
				t.Fatalf("API gate collapsed=%v want %v (reason %q)", apiCollapsed, tc.wantCollapsed, apiReason)
			}
			if apiCollapsed != docCollapsed {
				t.Fatalf("THE TWO SURFACES DISAGREE: api=%v doc=%v. This is the 2026-08-19 defect — the HTTP "+
					"surface refusing a window the generated documents publish in full.", apiCollapsed, docCollapsed)
			}
			if apiReason != docReason {
				t.Fatalf("same verdict, DIFFERENT reason:\n  api: %q\n  doc: %q\nA reader comparing the two "+
					"surfaces would find them describing different evidence.", apiReason, docReason)
			}
			if (apiErr == nil) != (docErr == nil) {
				t.Fatalf("error disagreement: api=%v doc=%v", apiErr, docErr)
			}
			if tc.wantCollapsed && apiReason == "" {
				t.Fatal("a refusal with no reason: a reader cannot act on it")
			}
		})
	}
}

// TestGate_ApiRefusalMatchesTheDocumentGate closes the loop at the HTTP layer:
// when the shared gate says collapsed, /api/accuracy must actually refuse, and
// it must refuse with ZERO rows. A 200 carrying rows plus a caveat is what the
// documents were doing.
func TestGate_ApiRefusalMatchesTheDocumentGate(t *testing.T) {
	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	_, st, d := newTestServer(t, nil)
	freshHeartbeat(t, st)
	seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -1), 300, 170)
	seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -2), 300, 5)
	seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -3), 300, 175)

	path := writeRegistry(t, registryFor(map[string]int{"1d": 3}))
	d.RegistryPath = path

	_, collapsed, err := CollapsedGradingWindow(context.Background(), st, path, now)
	if err != nil || !collapsed {
		t.Fatalf("fixture is not collapsed (collapsed=%v err=%v); the assertion below would prove nothing", collapsed, err)
	}

	srv := restartWith(t, d)
	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("HTTP %d, want 503. The document gate refuses this window; the API publishing it is the "+
			"exact divergence this shares one function to prevent. body=%v", code, body)
	}
	if body["status"] != "REFUSED" {
		t.Fatalf("status = %v, want REFUSED", body["status"])
	}
	if rows, ok := body["rows"]; ok && rows != nil {
		if arr, isArr := rows.([]any); isArr && len(arr) > 0 {
			t.Fatalf("a REFUSED response carried %d row(s): refusal must be atomic, not partial", len(arr))
		}
	}
}

// TestGate_IsDeterministicAndIdempotent: repeated calls on unchanged inputs must
// give the identical verdict and the identical reason string. A gate whose text
// wobbles between runs makes every regenerated document a spurious diff, and
// makes "the documents changed" useless as a signal.
func TestGate_IsDeterministicAndIdempotent(t *testing.T) {
	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	_, st, _ := newTestServer(t, nil)
	// Two horizons, both collapsed, so the reason string has to order its parts.
	seedResolvedForecasts(t, st, md.H1d, now.AddDate(0, 0, -1), 300, 4)
	seedResolvedForecasts(t, st, md.H1w, now.AddDate(0, 0, -1), 300, 3)
	path := writeRegistry(t, registryFor(map[string]int{"1d": 1, "1w": 1}))

	first, collapsed, err := CollapsedGradingWindow(context.Background(), st, path, now)
	if err != nil || !collapsed {
		t.Fatalf("fixture not collapsed: collapsed=%v err=%v", collapsed, err)
	}
	for i := 0; i < 5; i++ {
		got, c, err := CollapsedGradingWindow(context.Background(), st, path, now)
		if err != nil || !c {
			t.Fatalf("run %d: collapsed=%v err=%v", i, c, err)
		}
		if got != first {
			t.Fatalf("run %d produced a different reason on unchanged inputs:\n  first: %q\n  now:   %q",
				i, first, got)
		}
	}
}

// TestGate_UnreadableRegistryFailsOpenOnBothSurfaces. An unreadable registry is
// NOT evidence of a collapse, and both surfaces must treat it the same way:
// report the error and let the caller publish. Refusing on a failed read would
// wedge publication shut on a transient database or filesystem error — and if
// only ONE surface did that, they would diverge again in the opposite direction.
func TestGate_UnreadableRegistryFailsOpenOnBothSurfaces(t *testing.T) {
	now := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	_, st, _ := newTestServer(t, nil)

	_, collapsed, err := CollapsedGradingWindow(context.Background(), st, "/nonexistent/registry.json", now)
	if err == nil {
		t.Fatal("an unreadable registry must surface an error, not a silent verdict")
	}
	if collapsed {
		t.Fatal("an unreadable registry reported as COLLAPSED: a failed read is not evidence, and " +
			"cmd/collapsecheck exits 2 on this so the caller publishes")
	}
}
