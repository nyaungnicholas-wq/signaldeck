package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/fleetmon"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newFleetHealthServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, d := newTestServer(t, func(c *config.Config) { c.PublicReads = true })
	mux := http.NewServeMux()
	d.registerFleetHealth(mux)
	d.registerFeatureHealth(mux)
	srv.Config.Handler = d.secure(mux)
	return srv, st
}

type fleetHealthResp struct {
	Note        string            `json:"note"`
	GeneratedTs int64             `json:"generatedTs"`
	Snapshot    fleetmon.Snapshot `json:"snapshot"`
}

func getFleetJSON(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := newClient(t).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close() //nolint:errcheck
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("decode %s: %v", url, err)
		}
	}
	return res.StatusCode
}

// An empty platform must report UNKNOWN, not healthy — the single most important
// property of this endpoint, because a green light on no data is the failure mode
// it exists to prevent.
func TestFleetHealthEmptyPlatformIsUnknownNotHealthy(t *testing.T) {
	srv, _ := newFleetHealthServer(t)

	var got fleetHealthResp
	if code := getFleetJSON(t, srv.URL+"/api/fleet-health", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if got.Snapshot.Status == fleetmon.StatusHealthy {
		t.Fatal("an empty platform must never report healthy")
	}
	if got.Note == "" {
		t.Error("the honesty note must ship in the payload")
	}
	// Both paper books are surfaced even when empty, with their metrics withheld
	// rather than zeroed.
	if len(got.Snapshot.Trading) != len(fleetHealthStrategies) {
		t.Errorf("want %d books surfaced, got %d", len(fleetHealthStrategies), len(got.Snapshot.Trading))
	}
	for _, tr := range got.Snapshot.Trading {
		if tr.ProfitFactor != nil {
			t.Errorf("%s: profit factor must be withheld with no round trips, got %v",
				tr.Strategy, *tr.ProfitFactor)
		}
		if tr.Sortino != nil {
			t.Errorf("%s: Sortino must be withheld with no equity curve", tr.Strategy)
		}
	}
	if len(got.Snapshot.Withheld) == 0 {
		t.Error("withheld metrics must be named, not silently absent")
	}
}

// Layer coverage must be reported, and it must reflect the real wiring: the
// subsystems this codebase actually calls are Live, and the deliberately-narrow
// model zoo is reported as not implemented rather than hidden.
func TestFleetHealthReportsLayerCoverage(t *testing.T) {
	srv, _ := newFleetHealthServer(t)

	var got fleetHealthResp
	if code := getFleetJSON(t, srv.URL+"/api/fleet-health", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	cov := got.Snapshot.Layers
	if len(cov.Layers) < 10 {
		t.Fatalf("want the full layer list, got %d entries", len(cov.Layers))
	}

	byName := map[string]fleetmon.Layer{}
	for _, l := range cov.Layers {
		byName[l.Name] = l
	}

	// The risk engine is wired into the paper-trading worker — it must claim Live
	// AND enforcing, with evidence.
	risk, ok := byName["risk engine"]
	if !ok {
		t.Fatal("the risk engine must appear in layer coverage")
	}
	if !risk.Implemented || !risk.Live {
		t.Errorf("risk engine should be implemented and live, got %+v", risk)
	}
	if risk.Enforcing == nil || !*risk.Enforcing {
		t.Error("the risk engine enforces; coverage should say so")
	}
	if risk.Evidence == "" {
		t.Error("a Live claim must carry the evidence that proves it")
	}

	// The model zoo is deliberately narrow — reported, not hidden.
	zoo, ok := byName["model zoo"]
	if !ok {
		t.Fatal("the model zoo must still be listed")
	}
	if zoo.Implemented {
		t.Error("the model zoo is deliberately not built out; coverage must say so")
	}
	if !strings.Contains(strings.ToLower(zoo.Detail), "deliberately") {
		t.Errorf("the reason should be stated: %q", zoo.Detail)
	}

	// Data-dependent layers must NOT claim Live on an empty store: the feature
	// grader has published nothing and the canary has recorded no trial.
	if f := byName["feature factory"]; f.Live {
		t.Error("feature retirement must not claim Live before the grader has published a report")
	}
	if c := byName["continuous learning"]; c.Live {
		t.Error("the promotion gate must not claim Live before a canary trial exists")
	}
	if cov.BuiltNotLive == 0 {
		t.Error("the dormant data-dependent layers should be counted as built-not-live")
	}
}

// The feature-health endpoint must distinguish "nothing examined" from "nothing
// retired".
func TestFeatureHealthUngradedIsNotAnEmptyRetireSet(t *testing.T) {
	srv, _ := newFleetHealthServer(t)

	var got struct {
		Note     string `json:"note"`
		Horizons []struct {
			Horizon string   `json:"horizon"`
			Graded  bool     `json:"graded"`
			Reason  string   `json:"reason"`
			Retire  []string `json:"retire"`
		} `json:"horizons"`
	}
	if code := getFleetJSON(t, srv.URL+"/api/feature-health", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(got.Horizons) != 2 {
		t.Fatalf("want both horizons reported, got %d", len(got.Horizons))
	}
	for _, h := range got.Horizons {
		if h.Graded {
			t.Errorf("%s: nothing has been graded yet", h.Horizon)
		}
		if h.Reason == "" {
			t.Errorf("%s: an ungraded horizon must explain itself", h.Horizon)
		}
		if !strings.Contains(h.Reason, "not the same as") {
			t.Errorf("%s: the reason should distinguish unexamined from unretired: %q", h.Horizon, h.Reason)
		}
	}
	if !strings.Contains(got.Note, "NOT evidence") {
		t.Errorf("the note must disclaim predictive value: %q", got.Note)
	}
}

// The monitor's critical drawdown line must equal the risk gate's halt, checked
// through the served payload rather than only in the package.
func TestFleetHealthThresholdsAgreeWithTheGate(t *testing.T) {
	if fleetmon.DefaultThresholds().CriticalDrawdown != 0.20 {
		t.Error("the monitor's red line drifted from riskgate's 0.20 default halt")
	}
}
