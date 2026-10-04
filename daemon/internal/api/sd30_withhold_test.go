package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/evidence"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/pipeline"
	"github.com/nyaungnicholas-wq/signaldeck/internal/postmortem"
	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
	"github.com/nyaungnicholas-wq/signaldeck/internal/signalbt"
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

func TestSD30_AccuracyEnvelopeNamesWithheldHorizons(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	d.RegistryPath = writeRegistry(t, sd30Registry)
	srv := restartWith(t, d)
	freshHeartbeat(t, st)
	sd30On(t)
	if _, body := getAccuracy(t, srv.URL); fmt.Sprint(body["withheld_horizons"]) != "[1d 1w]" {
		t.Fatalf("withheld_horizons %v, want both directional horizons named by the switch", body["withheld_horizons"])
	}
	sd30Off(t)
	if _, body := getAccuracy(t, srv.URL); body["withheld_horizons"] != nil {
		t.Fatalf("flag off still names withheld horizons: %v", body["withheld_horizons"])
	}
}

// A refused grader keeps its own reason first; SD-30 is still named beside it.
func TestSD30_TrackRecordNamesSD30BesideARefusal(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	sd30SeedPerfect(t, st)
	d.RegistryPath = writeRegistry(t, `{"status": "REFUSED", "refused_since": "2026-10-01T14:05:18", "rows": []}`)
	sd30On(t)
	resp, err := d.buildTrackRecord(context.Background(), md.H1d)
	if err != nil {
		t.Fatal(err)
	}
	note, _ := resp["note"].(string)
	if resp["gateReason"] != "refused" || !strings.Contains(note, "REFUSED since") || !strings.Contains(note, publication.SD30Reason) {
		t.Fatalf("gateReason %v note %q: both reasons must be stated", resp["gateReason"], note)
	}
}

func TestSD30_PredictionsLatestOffRestoresWinRate(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	sd30SeedPerfect(t, st)
	sd30Off(t)
	resp, err := d.buildPredictionsLatest(context.Background(), md.H1d) // the build, not the shared cache
	if err != nil {
		t.Fatal(err)
	}
	live, _ := resp["liveRecord"].(map[string]any)
	label, _ := resp["trackLabel"].(string)
	if live["winRate"] != 1.0 || strings.Contains(label, "SD-30") || !strings.Contains(label, "win rate 100.0%") {
		t.Fatalf("flag off: liveRecord %v trackLabel %q", live, label)
	}
}

func serveRecorded(t *testing.T, h http.HandlerFunc, path string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", path, err, rec.Body.String())
	}
	return body
}

func TestSD30_CanaryWithheld(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	for _, m := range []string{"directional-ensemble-1d", "directional-ensemble-1h"} {
		if err := st.UpsertCanaryTrial(t.Context(), store.CanaryTrial{Model: m, Incumbent: "v1", Challenger: "v2",
			Decision: "hold", Serving: "v1", Reason: "challenger 63.0% vs incumbent 61.0%",
			IncN: 300, IncAcc: 0.61, ChN: 120, ChAcc: 0.63, ChLower: 0.55, ChUpper: 0.70, Baseline: 0.58, DecidedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	byModel := func(body map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		trials, _ := body["trials"].([]any)
		for _, x := range trials {
			m, _ := x.(map[string]any)
			name, _ := m["model"].(string)
			out[name] = m
		}
		return out
	}
	sd30On(t)
	got := byModel(serveRecorded(t, d.canaryTrials, "/api/canary"))
	w := got["directional-ensemble-1d"]
	for _, k := range []string{"incAcc", "chAcc", "chLower", "chUpper", "baseline"} {
		if v, present := w[k]; !present || v != nil {
			t.Fatalf("1d trial %s = %v (present %v), want an explicit null", k, v, present)
		}
	}
	if w["decision"] != "withheld" || w["reason"] != publication.SD30Reason || w["serving"] != "v1" || jnum(w, "incN") != 300 {
		t.Fatalf("1d trial keeps its facts and states the reason: %v", w)
	}
	if h := got["directional-ensemble-1h"]; h["incAcc"] != 0.61 || h["decision"] != "hold" {
		t.Fatalf("a 1h trial is not SD-30's: %v", h)
	}
	sd30Off(t)
	if w := byModel(serveRecorded(t, d.canaryTrials, "/api/canary"))["directional-ensemble-1d"]; w["incAcc"] != 0.61 || w["decision"] != "hold" {
		t.Fatalf("flag off: %v", w)
	}
}

func TestSD30_SelfAuditWithheld(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	for _, r := range []store.SelfAuditRow{
		{Ts: 100, Metric: "calibration:1d", Value: 0.12, Status: "ok", Detail: "reliability 0.120"},
		{Ts: 100, Metric: "calibration_level:1w", Value: 0.30, Status: "at_chance", Detail: "at chance"},
		{Ts: 100, Metric: "prediction_bias:1d", Value: 0.04, Status: "ok", Detail: "bias +0.04"},
		{Ts: 100, Metric: "factor_ic:trend", Value: 0.05, Status: "ok", Detail: "ic 0.05"},
	} {
		if err := st.InsertSelfAudit(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	byMetric := func(body map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		fs, _ := body["findings"].([]any)
		for _, x := range fs {
			m, _ := x.(map[string]any)
			name, _ := m["metric"].(string)
			out[name] = m
		}
		return out
	}
	sd30On(t)
	got := byMetric(serveRecorded(t, d.selfAudit, "/api/self-audit"))
	for _, m := range []string{"calibration:1d", "calibration_level:1w", "prediction_bias:1d"} {
		f := got[m]
		if f["status"] != "withheld" || f["detail"] != publication.SD30Reason || jnum(f, "value") != 0 {
			t.Fatalf("%s: %v", m, f)
		}
	}
	if f := got["factor_ic:trend"]; f["status"] != "ok" || jnum(f, "value") != 0.05 {
		t.Fatalf("a factor-IC finding is not SD-30's: %v", f)
	}
	sd30Off(t)
	if f := byMetric(serveRecorded(t, d.selfAudit, "/api/self-audit"))["calibration:1d"]; f["status"] != "ok" || jnum(f, "value") != 0.12 {
		t.Fatalf("flag off: %v", f)
	}
}

// The worker's stored grade (its lifetime ledger) is left as it is; what the
// public route SERVES for a directional horizon loses every label-derived figure.
func TestSD30_ModelHealthWithheld(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	blob := `{"model":"directional-ensemble-1d","verdict":"retired","emitting":false,"overall":0.31,
	  "components":{"skill":0.2,"calibration":0.3,"drift":0.5,"freshness":1,"stability":0.9},
	  "reasons":["lifetime accuracy is BELOW the naive baseline"],"observations":2257,
	  "accuracy":0.462,"baseline":0.528,"skillVsBenchmark":-0.08,
	  "benchmark":{"model":"prequential-majority-1d","n":100,"accuracy":0.55,"ensembleAlignedN":100,"ensembleAlignedAcc":0.47},
	  "readmission":{"eligible":false,"lower":0.41,"upper":0.5,"null":0.55,"distinctDays":12,"minDistinctDays":20,"reason":"not re-admitted: lower bound 41.0%"},
	  "window":{"since":1791072000,"n":40,"accuracy":0.6,"baseline":0.52,"brierSkill":0.05,"benchN":30,"benchAccuracy":0.55,"alignedN":30,"alignedAccuracy":0.58,"skillVsBenchmark":0.03}}`
	// A record stored before the window block existed: it must publish no figures.
	old := `{"model":"directional-ensemble-1w","verdict":"retired","emitting":false,"overall":0.31,` +
		`"components":{"skill":0.2,"calibration":0.3,"drift":0.5,"freshness":1},"observations":2257,` +
		`"accuracy":0.462,"baseline":0.528,"skillVsBenchmark":-0.08,` +
		`"benchmark":{"model":"prequential-majority-1w","n":100,"accuracy":0.55,"ensembleAlignedN":100,"ensembleAlignedAcc":0.47}}`
	if err := st.SetMeta(t.Context(), pipeline.MetaKeyPrefix+"directional-ensemble-1w", old); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"directional-ensemble-1d", "structural-trend21"} {
		b := strings.ReplaceAll(blob, "directional-ensemble-1d", k)
		if err := st.SetMeta(t.Context(), pipeline.MetaKeyPrefix+k, b); err != nil {
			t.Fatal(err)
		}
	}
	byModel := func() map[string]map[string]any {
		out := map[string]map[string]any{}
		ms, _ := serveRecorded(t, d.modelHealth, "/api/model-health")["models"].([]any)
		for _, x := range ms {
			m, _ := x.(map[string]any)
			name, _ := m["model"].(string)
			out[name] = m
		}
		return out
	}
	nilAt := func(m map[string]any, keys ...string) {
		t.Helper()
		for _, k := range keys {
			if v, present := m[k]; !present || v != nil {
				t.Fatalf("%s = %v (present %v), want an explicit null in %v", k, v, present, m)
			}
		}
	}

	sd30On(t)
	got := byModel()
	m := got["directional-ensemble-1d"]
	nilAt(m, "accuracy", "baseline", "skillVsBenchmark", "overall")
	b, _ := m["benchmark"].(map[string]any)
	nilAt(b, "accuracy", "ensembleAlignedAcc")
	c, _ := m["components"].(map[string]any)
	nilAt(c, "skill", "calibration", "drift")
	ra, _ := m["readmission"].(map[string]any)
	nilAt(ra, "lower", "upper", "null")
	if _, present := m["window"]; present {
		t.Fatalf("the window block (the corrected label's figures) leaked while withheld: %v", m)
	}
	if m["withheld"] != publication.SD30Reason || ra["reason"] != publication.SD30Reason {
		t.Fatalf("the reason must be stated: withheld %v, readmission.reason %v", m["withheld"], ra["reason"])
	}
	if m["verdict"] != "retired" || jnum(m, "observations") != 2257 || jnum(b, "n") != 100 || c["freshness"] != 1.0 {
		t.Fatalf("verdict, sample sizes and label-free components stay: %v", m)
	}
	if s := got["structural-trend21"]; s["accuracy"] != 0.462 || s["withheld"] != nil {
		t.Fatalf("a structural row is not SD-30's: %v", s)
	}

	sd30Off(t)
	got = byModel()
	m = got["directional-ensemble-1d"]
	b, _ = m["benchmark"].(map[string]any)
	c, _ = m["components"].(map[string]any)
	// Flag off publishes the window block (calls since 2026-10-04), never the
	// lifetime grade: verdict and emitting stay, the composite score does not.
	if m["accuracy"] != 0.6 || m["baseline"] != 0.52 || m["skillVsBenchmark"] != 0.03 || jnum(m, "observations") != 40 ||
		jnum(b, "n") != 30 || b["accuracy"] != 0.55 || b["ensembleAlignedAcc"] != 0.58 || m["withheld"] != nil ||
		m["verdict"] != "retired" || m["figuresSince"] == nil {
		t.Fatalf("flag off must publish the window figures: %v", m)
	}
	nilAt(m, "overall")
	nilAt(c, "skill", "calibration", "drift")
	if _, present := m["window"]; present {
		t.Fatalf("the raw window block is served alongside the figures: %v", m)
	}
	w1 := got["directional-ensemble-1w"]
	nilAt(w1, "accuracy", "baseline", "skillVsBenchmark")
	if jnum(w1, "observations") != 0 {
		t.Fatalf("a record with no window block published lifetime figures: %v", w1)
	}
}

// The seeded refutations are dated history: kept verbatim, and said to be so.
func TestSD30_EvidenceSeedsSayTheyAreDatedHistory(t *testing.T) {
	_, st, d := newTestServer(t, nil)
	if _, err := evidence.EnsureSeeds(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{}
	cs, _ := serveRecorded(t, d.evidenceList, "/api/evidence")["claims"].([]any)
	for _, x := range cs {
		c, _ := x.(map[string]any)
		id, _ := c["id"].(string)
		texts[id], _ = c["text"].(string)
	}
	one := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/evidence/directional-ensemble-1w", nil)
	req.SetPathValue("id", "directional-ensemble-1w")
	d.evidenceOne(one, req)
	for id, text := range map[string]string{"directional-ensemble-1d": texts["directional-ensemble-1d"], "directional-ensemble-1w": one.Body.String()} {
		if !strings.Contains(text, "dated retirement record (2026-07-01 to 2026-07-26)") || !strings.Contains(text, "pre-SD-30") {
			t.Fatalf("%s does not say it is dated history: %s", id, text)
		}
	}
	if !strings.Contains(texts["directional-ensemble-1d"], "48.12%") {
		t.Fatal("the dated figure is history and must stay")
	}
	for id, text := range texts {
		if !strings.HasPrefix(id, "directional-ensemble-") && strings.Contains(text, "SD-30") {
			t.Fatalf("%s is not a directional claim: %s", id, text)
		}
	}
}

// Every postmortem is a 1d/1w directional call judged wrong on the SD-30 label.
func TestSD30_PostmortemsWithheld(t *testing.T) {
	ctx := context.Background()
	_, st, d := newTestServer(t, nil)
	sym, err := st.UpsertSymbol(ctx, "PMX", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, ts := range []int64{1000, store.GradingEpochTS + 3600} {
		if err := st.UpsertPrediction(ctx, store.Prediction{SymbolID: sym.ID, Horizon: md.H1d, Ts: ts,
			RawProb: 0.7, CalProb: 0.7, NUsed: 40, Components: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := st.ResolvePrediction(ctx, sym.ID, md.H1d, ts, -0.02); err != nil {
			t.Fatal(err)
		}
	}
	misses, err := st.UnPostmortemedMisses(ctx, md.H1d, 10)
	if err != nil || len(misses) != 2 {
		t.Fatalf("seeded misses: %d %v", len(misses), err)
	}
	for _, m := range misses {
		rep := postmortem.Classify(postmortem.Case{Prob: m.Prob, Up: m.Up, FwdReturn: m.FwdReturn, Disagreement: 0.5}, postmortem.DefaultThresholds())
		if err := st.InsertPostmortem(ctx, m, rep, time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}

	sd30On(t)
	body := serveRecorded(t, d.postmortems, "/api/postmortems?days=0")
	cl, _ := body["clusters"].([]any)
	rc, _ := body["recent"].([]any)
	if v, present := body["totalMisses"]; !present || v != nil || cl == nil || len(cl) != 0 || rc == nil || len(rc) != 0 ||
		body["withheld"] != publication.SD30Reason {
		t.Fatalf("withheld postmortems: totalMisses %v clusters %v recent %v withheld %v",
			body["totalMisses"], body["clusters"], body["recent"], body["withheld"])
	}

	sd30Off(t)
	body = serveRecorded(t, d.postmortems, "/api/postmortems?days=0")
	cl, _ = body["clusters"].([]any)
	rc, _ = body["recent"].([]any)
	// Only the call issued in the current window (ts >= store.GradingEpochTS) is graded.
	if jnum(body, "totalMisses") != 1 || len(cl) == 0 || len(rc) != 1 || body["withheld"] != nil {
		t.Fatalf("flag off: %v", body)
	}
}

func TestConfidenceEvidence_GradesOnlyTheCurrentWindow(t *testing.T) {
	_, st, d := newTestServer(t, func(c *config.Config) {})
	sd30SeedPerfect(t, st) // 40 correct calls issued from the epoch day on
	ctx := context.Background()
	old, err := st.UpsertSymbol(ctx, "SDOLD", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ { // 10 wrong calls issued before the window
		seedResolvedPrediction(t, st, old.ID, md.H1d, int64(store.GradingEpochTS)-int64(i+1)*86400, 0.8, -0.02)
	}
	sd30Off(t)
	ev, err := d.confidenceEvidence(ctx, md.H1d)
	if err != nil || ev.N != 40 || ev.Accuracy != 1.0 {
		t.Fatalf("confidence must grade only calls issued since GradingEpochTS: N=%d acc=%v err=%v", ev.N, ev.Accuracy, err)
	}
	if rec, err := st.DirectionalRecordIssued(ctx, md.H1d, 0, store.GradingEpochTS); err != nil || len(rec.Days) != 40 {
		t.Fatalf("the per-day record must carry the same floor: %d days, err %v", len(rec.Days), err)
	}
}

func TestSD30_AttributionLiveWithheld(t *testing.T) {
	srv, st := newAttributionServer(t)
	ctx := context.Background()
	sym, err := st.UpsertSymbol(ctx, "LIVE", md.Stocks, "")
	if err != nil {
		t.Fatal(err)
	}
	seedAttributionLive(t, st, sym.ID, 40, 26)
	sd30On(t)
	url := srv.URL + "/api/attribution?symbol=LIVE&market=stocks&horizon=1d"
	if body := getAttributionBand(t, url); body.Report.LiveN != 0 {
		t.Fatalf("withheld live record still graded: liveN=%d", body.Report.LiveN)
	}
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close() //nolint:errcheck
	var m map[string]any
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["liveWithheld"] != publication.SD30Reason {
		t.Fatalf("liveWithheld = %v, want the SD-30 reason", m["liveWithheld"])
	}
}

func TestSD30_SignalBacktestWithheld(t *testing.T) {
	srv, st := newSignalBTServer(t, nil)
	pin := signalbt.Pinned{DayKey: "2026-07-05", ComputedTs: 1751756400, BenchmarkSymbol: "SPY", HasBenchmark: true,
		Results: map[string]signalbt.Result{"1d": {Horizon: "1d", RawN: 100, IndependentN: 42, MinIndependentN: 30, IC: 0.05, HitRate: 0.6}}}
	if err := st.SetJSON(context.Background(), signalbt.MetaKeyLatest, pin); err != nil {
		t.Fatal(err)
	}
	sd30On(t)
	for _, q := range []string{"?horizon=1d", "?horizon=1d&pinned=1", "?horizon=1w"} {
		res, err := http.Get(srv.URL + "/api/signal-backtest" + q)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		err = json.NewDecoder(res.Body).Decode(&m)
		res.Body.Close() //nolint:errcheck
		if err != nil {
			t.Fatal(err)
		}
		r, _ := m["result"].(map[string]any)
		if m["withheld"] != publication.SD30Reason || m["pinned"] != false || r == nil || r["gated"] != true ||
			r["ic"] != nil || r["hitRate"] != nil || r["quintileSpread"] != nil || r["note"] != publication.SD30Reason {
			t.Fatalf("%s: SD-30 must withhold the own-signal grade: %v", q, m)
		}
	}
}
