// Pairs-study route tests. The payload is frozen data, so the things worth
// asserting are the ones a future edit could quietly break: that the embedded
// run still decodes, that the arms stay comparable, and above all that the
// verdict-bearing facts survive — the selected arm's interval contains zero and
// the selection edge over a random same-sector book is negligible. If either of
// those ever flips silently, a do-not-ship result becomes a ship-it page.
package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/pairsstudy"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func newPairsServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "pairs_api.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	srv := httptest.NewUnstartedServer(nil)
	t.Cleanup(srv.Close)
	d.Cfg.AllowedHosts = []string{srv.Listener.Addr().String()}
	mux := http.NewServeMux()
	d.registerPairsStudy(mux)
	srv.Config.Handler = d.secure(mux)
	srv.Start()
	return srv
}

func TestPairsStudyRoute(t *testing.T) {
	srv := newPairsServer(t)
	resp, err := http.Get(srv.URL + "/api/pairs-study")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Study  pairsstudy.Study `json:"study"`
		Frozen bool             `json:"frozen"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Frozen {
		t.Error("frozen = false; the study is an offline run and must not claim freshness")
	}
	if len(body.Study.Costs) < 2 {
		t.Fatalf("cost levels = %d, want the sweep (a single cost number would hide the finding)", len(body.Study.Costs))
	}

	// Ascending cost, so the page can read the break-even level off the order.
	for i := 1; i < len(body.Study.Costs); i++ {
		if body.Study.Costs[i].CostBps <= body.Study.Costs[i-1].CostBps {
			t.Errorf("cost levels not ascending at %d: %v then %v", i, body.Study.Costs[i-1].CostBps, body.Study.Costs[i].CostBps)
		}
	}

	zero := body.Study.Costs[0]
	if zero.CostBps != 0 {
		t.Fatalf("first cost level = %v bps, want the zero-cost arm first", zero.CostBps)
	}
	if zero.Coint.Trades == 0 || zero.Random.Trades == 0 || zero.Worst.Trades == 0 {
		t.Fatal("an arm has no trades — the three arms must all be traded on identical rules")
	}

	// THE verdict facts.
	if zero.Coint.ExcludesZero {
		t.Errorf("selected arm's interval excludes zero at ZERO cost ([%v, %v]) — that contradicts the shipped do-not-ship verdict",
			zero.Coint.MeanLo, zero.Coint.MeanHi)
	}
	if math.Abs(zero.SelectionEdge) > 0.001 {
		t.Errorf("selection edge = %v per trade; the verdict rests on this being negligible", zero.SelectionEdge)
	}
	if got := zero.Coint.MeanRet - zero.Random.MeanRet; math.Abs(got-zero.SelectionEdge) > 1e-12 {
		t.Errorf("selectionEdge = %v, but coint - random = %v", zero.SelectionEdge, got)
	}
}

func TestPairsStudyPersistenceContrast(t *testing.T) {
	study, err := pairsstudy.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := study.Persistence

	// The mechanism finding: correlation rank persists hard, cointegration rank
	// does not persist at all. Everything the page says follows from this gap.
	if p.CorrRho < 0.5 {
		t.Errorf("correlation rho = %v, want strongly positive", p.CorrRho)
	}
	if math.Abs(p.CointRho) > 0.05 {
		t.Errorf("cointegration rho = %v, want ~zero — a persistent one would reopen the hypothesis", p.CointRho)
	}
	if p.CorrRhoLo == nil || p.CorrRhoHi == nil {
		t.Fatal("correlation rho shipped without its interval")
	}
	if !(*p.CorrRhoLo <= p.CorrRho && p.CorrRho <= *p.CorrRhoHi) {
		t.Errorf("rho %v outside its own CI [%v, %v]", p.CorrRho, *p.CorrRhoLo, *p.CorrRhoHi)
	}
	if p.Blocks < 20 {
		t.Errorf("blocks = %d, want the full non-overlapping walk", p.Blocks)
	}
	if len(study.Limitations) == 0 {
		t.Error("no limitations shipped — the delisting caveat is load-bearing here")
	}
}
