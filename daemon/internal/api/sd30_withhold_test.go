package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SD-30 withholding (publication.SD30Withheld). Every test sets the flag
// itself, so none depends on the committed default; each surface is checked
// withheld-with-the-reason, and where the old behaviour is cheap to reach,
// restored with the flag off.

func sd30Set(t *testing.T, v bool) {
	t.Helper()
	old := publication.SD30Withheld
	publication.SD30Withheld = v
	t.Cleanup(func() { publication.SD30Withheld = old })
}

// sd30Off runs the behaviour the SD-30 flag reverses to.
func sd30Off(t *testing.T) { t.Helper(); sd30Set(t, false) }
func sd30On(t *testing.T)  { t.Helper(); sd30Set(t, true) }

const sd30Registry = `{
  "graded_at": "2026-10-01T14:05:18", "refused_since": null,
  "grader_sha256": "fe5edfc9d236135a37d4b5be40ce6f0622ffd5ec7396e2c8d0373426f5d01a29",
  "min_independent_n": 30, "min_distinct_blocks": 10,
  "rows": [
    {"predictor": "directional-ensemble (1d)", "family": "direction", "band": "all",
     "live_n": 2257, "live_acc": 0.4626, "ci": null, "ci_method": "withheld",
     "distinct_days": 9, "effective_n": null, "null_acc": 0.5284, "skill": -0.0658, "retire": false},
    {"predictor": "prequential-majority (1d)", "family": "benchmark", "band": "all",
     "live_n": 1644, "live_acc": 0.5554, "ci": null, "ci_method": "withheld",
     "distinct_days": 6, "effective_n": null, "null_acc": 0.511, "skill": 0.044, "retire": false},
    {"predictor": "trend21", "family": "structure", "band": "all",
     "live_n": 500, "live_acc": 0.78, "ci": null, "ci_method": "withheld",
     "distinct_days": 20, "effective_n": null, "null_acc": 0.70, "skill": 0.08, "retire": false}
  ]
}`

func sd30AccuracyRows(t *testing.T, registry string, claim bool) map[string]map[string]any {
	t.Helper()
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, registry)
	srv := restartWith(t, d)
	freshHeartbeat(t, st)
	if claim {
		if err := st.PutEvidenceClaim(t.Context(), store.EvidenceClaimRow{
			ID: "directional-ensemble-1d", Text: "REFUTED on the live record",
			ScopeJSON: `{"horizons":["1d"]}`, Tier: "refuted", Status: "refuted",
		}); err != nil {
			t.Fatal(err)
		}
	}
	code, body := getAccuracy(t, srv.URL)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%v)", code, body["reason"])
	}
	out := map[string]map[string]any{}
	rows, _ := body["rows"].([]any)
	for _, r := range rows {
		row, _ := r.(map[string]any)
		name, _ := row["predictor"].(string)
		out[name] = row
	}
	return out
}

func TestSD30_AccuracyWithholdsDirectionalRows(t *testing.T) {
	sd30On(t)
	rows := sd30AccuracyRows(t, sd30Registry, false)
	for name, n := range map[string]float64{"directional-ensemble": 2257, "prequential-majority": 1644} {
		row := rows[name]
		if row == nil {
			t.Fatalf("%s missing from %v", name, rows)
		}
		if row["publication_status"] != "REFUSED" {
			t.Fatalf("%s: status %v, want REFUSED", name, row["publication_status"])
		}
		for _, k := range []string{"live_acc", "null_acc", "skill"} {
			if v, present := row[k]; !present || v != nil {
				t.Fatalf("%s: %s = %v (present %v), want an explicit null", name, k, v, present)
			}
		}
		if row["figures_withheld"] != publication.SD30Reason {
			t.Fatalf("%s: figures_withheld %v", name, row["figures_withheld"])
		}
		if reasons, _ := row["reasons"].([]any); len(reasons) == 0 || reasons[0] != publication.SD30Reason {
			t.Fatalf("%s: the SD-30 reason must lead, got %v", name, reasons)
		}
		if jnum(row, "live_n") != n {
			t.Fatalf("%s: live_n %v, sample sizes stay published", name, row["live_n"])
		}
	}
	tr := rows["trend21"]
	if tr["live_acc"] != 0.78 || tr["publication_status"] == "REFUSED" {
		t.Fatalf("a structural row must be untouched: %v", tr)
	}
	if _, present := tr["figures_withheld"]; present {
		t.Fatalf("a structural row carries figures_withheld: %v", tr)
	}
}

func TestSD30_AccuracyOffRestoresFigures(t *testing.T) {
	sd30Off(t)
	row := sd30AccuracyRows(t, sd30Registry, false)["directional-ensemble"]
	if row["live_acc"] != 0.4626 || row["publication_status"] != "INSUFFICIENT" {
		t.Fatalf("flag off must restore the old row: %v", row)
	}
	if _, present := row["figures_withheld"]; present {
		t.Fatalf("flag off still marks the row withheld: %v", row)
	}
	reasons, _ := row["reasons"].([]any)
	for _, r := range reasons {
		if s, _ := r.(string); strings.Contains(s, "SD-30") {
			t.Fatalf("flag off still cites SD-30: %v", reasons)
		}
	}
}

// Retirement is a fact about the record, not a figure over the withheld label:
// it stays RETIRED, with the figures still withheld.
func TestSD30_AccuracyRetiredRowStaysRetired(t *testing.T) {
	sd30On(t)
	row := sd30AccuracyRows(t, thinWindowRegistry, true)["directional-ensemble"]
	if row["publication_status"] != "RETIRED" || row["retired"] != true {
		t.Fatalf("retirement must stay visible: %v", row)
	}
	if row["live_acc"] != nil || row["figures_withheld"] != publication.SD30Reason {
		t.Fatalf("a retired row's figures are still withheld: %v", row)
	}
}

// sd30SeedPerfect writes 40 resolved 1d calls on one symbol inside the graded
// window, every one directionally right: ungated and winRate 1.0 when published.
func sd30SeedPerfect(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "SDTH", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	const day = int64(86400)
	base := int64(store.GradingEpochTS)
	base -= base % day
	for i := 0; i < 40; i++ {
		prob, fwd := 0.8, 0.02+0.001*float64(i)
		if i%2 == 1 {
			prob, fwd = 0.2, -0.02-0.001*float64(i)
		}
		seedResolvedPrediction(t, st, sym.ID, md.H1d, base+int64(i)*day, prob, fwd)
	}
}

func TestSD30_TrackRecordWithheld(t *testing.T) {
	srv, st := newTrackRecordServer(t)
	sd30SeedPerfect(t, st)

	sd30On(t)
	body := getJSON(t, srv, "/api/track-record?horizon=1d")
	if body["gated"] != true || body["gateReason"] != "refused" || body["note"] != publication.SD30Reason {
		t.Fatalf("gated=%v gateReason=%v note=%v", body["gated"], body["gateReason"], body["note"])
	}
	for _, k := range []string{"winRate", "brier", "ic"} {
		if body[k] != nil {
			t.Fatalf("%s = %v while withheld", k, body[k])
		}
	}
	if jnum(body, "independentN") != 40 {
		t.Fatalf("independentN %v: the sample size stays published", body["independentN"])
	}
	byMarket, _ := body["byMarket"].([]any)
	if len(byMarket) == 0 {
		t.Fatal("byMarket missing: the hit-rate check below would be vacuous")
	}
	for _, m := range byMarket {
		mm, _ := m.(map[string]any)
		if _, present := mm["dirHitRate"]; present || mm["n"] == nil {
			t.Fatalf("byMarket row %v: dirHitRate must go, n must stay", mm)
		}
	}
	if rel, ok := body["reliability"].([]any); !ok || len(rel) != 0 {
		t.Fatalf("reliability %v, want an empty array", body["reliability"])
	}

	sd30Off(t)
	body = getJSON(t, srv, "/api/track-record?horizon=1d")
	if body["gated"] != false || jnum(body, "winRate") != 1.0 {
		t.Fatalf("flag off: gated=%v winRate=%v, want the ungated record back", body["gated"], body["winRate"])
	}
}

func TestSD30_CalibrationWithheld(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	sd30SeedPerfect(t, st)

	sd30On(t)
	body := callCalibration(t, d, "1d")
	for _, k := range []string{"withheld", "trackLabel", "brierNote"} {
		if body[k] != publication.SD30Reason {
			t.Fatalf("%s = %v, want the SD-30 reason", k, body[k])
		}
	}
	if bins, ok := body["bins"].([]any); !ok || len(bins) != 0 {
		t.Fatalf("bins %v, want an empty array", body["bins"])
	}
	for _, k := range []string{"brier", "reliability", "brierSkill"} {
		if body[k] != nil {
			t.Fatalf("%s = %v while withheld", k, body[k])
		}
	}
	live, _ := body["liveRecord"].(map[string]any)
	if live["winRate"] != nil || jnum(live, "independentN") != 40 {
		t.Fatalf("liveRecord %v: win rate withheld, sample size kept", live)
	}
	if jnum(body, "n") != 40 {
		t.Fatalf("n %v", body["n"])
	}

	sd30Off(t)
	body = callCalibration(t, d, "1d")
	if _, present := body["withheld"]; present {
		t.Fatalf("flag off still withheld: %v", body["withheld"])
	}
	if bins, _ := body["bins"].([]any); len(bins) == 0 || body["brier"] == nil {
		t.Fatalf("flag off: bins %v brier %v, want the calibration back", body["bins"], body["brier"])
	}
}

// Flag on only: /api/predictions/latest is served through a process-wide cache
// keyed on the store, so a second read in the same test would see the first.
func TestSD30_PredictionsLatestWithheld(t *testing.T) {
	url, st := newStage5Server(t)
	sd30SeedPerfect(t, st)
	sd30On(t)
	resp, err := newClient(t).Get(url + "/api/predictions/latest?horizon=1d")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	live, _ := body["liveRecord"].(map[string]any)
	label, _ := body["trackLabel"].(string)
	if live["winRate"] != nil || !strings.Contains(label, publication.SD30Reason) {
		t.Fatalf("liveRecord %v trackLabel %q", live, label)
	}
}

func TestSD30_SignalReportNote(t *testing.T) {
	sd30On(t)
	if note, ok := liveDirectionalNote(5, 0.9, nil); !ok || !strings.Contains(note, publication.SD30Reason) || strings.Contains(note, "90") {
		t.Fatalf("withheld note %q ok=%v", note, ok)
	}
	sd30Off(t)
	if note, ok := liveDirectionalNote(40, 0.5, nil); !ok || !strings.Contains(note, "win rate 50") || strings.Contains(note, "SD-30") {
		t.Fatalf("flag off note %q ok=%v", note, ok)
	}
	if _, ok := liveDirectionalNote(5, 0.5, nil); ok {
		t.Fatal("flag off, below the floor: no note")
	}
}

func TestSD30_FleetEdgeWithheld(t *testing.T) {
	const day = int64(86400)
	base := int64(store.GradingEpochTS)
	base -= base % day
	var rows []store.ResolvedPredictionOutcome
	for i := 0; i < 40; i++ {
		o := store.ResolvedPredictionOutcome{SymbolID: int64(i%4 + 1), Market: md.Stocks, Horizon: md.H1d,
			Ts: base + int64(i)*day, Prob: 0.8, Up: 1, FwdReturn: 0.01}
		if i%2 == 1 {
			o.Prob, o.Up, o.FwdReturn = 0.2, 0, -0.01
		}
		o.SettleTs = o.Ts
		rows = append(rows, o)
	}
	sd30On(t)
	g := gradeFleetEdge(rows)
	if g.proven || g.graded || g.winRate != 0 || !strings.Contains(g.note, publication.SD30Reason) ||
		!g.cluster.Refused || g.cluster.Reason != publication.SD30Reason {
		t.Fatalf("withheld fleet edge: %+v", g)
	}
	sd30Off(t)
	if g := gradeFleetEdge(rows); !g.graded || g.winRate != 1.0 {
		t.Fatalf("flag off fleet edge: graded=%v winRate=%v", g.graded, g.winRate)
	}
}

func TestSD30_ConfidenceEvidenceWithheld(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	sd30SeedPerfect(t, st)
	sd30On(t)
	ev, err := d.confidenceEvidence(context.Background(), md.H1d)
	if err != nil || ev.WithheldReason != publication.SD30Reason {
		t.Fatalf("WithheldReason %q err %v", ev.WithheldReason, err)
	}
	sd30Off(t)
	if ev, err := d.confidenceEvidence(context.Background(), md.H1d); err != nil || ev.WithheldReason != "" {
		t.Fatalf("flag off WithheldReason %q err %v", ev.WithheldReason, err)
	}
}

func TestSD30_DeskAccuracyPhrase(t *testing.T) {
	if got := deskAccuracyPhrase(0, "live edge "+publication.SD30Reason); strings.Contains(got, "0.0%") || !strings.Contains(got, "SD-30") {
		t.Fatalf("unknown accuracy printed as a figure: %q", got)
	}
	if got := deskAccuracyPhrase(0.512, "x"); got != "measured accuracy 51.2%" {
		t.Fatalf("a measured accuracy must still print: %q", got)
	}
}
