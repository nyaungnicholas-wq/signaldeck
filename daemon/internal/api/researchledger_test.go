package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// seedResearchLedger seeds two hypotheses — H002 with a full four-kind
// evidence chain (experiment, attack, replication, backtest with an era note)
// and H008 with no evidence at all (the honest never-graded case) — plus one
// historical research-week row. Shared by the ledger and graph endpoint tests.
func seedResearchLedger(t *testing.T, ctx context.Context, st *store.Store, now int64) {
	t.Helper()
	if err := st.UpsertLedgerHypothesis(ctx, rl.Hypothesis{
		ID: "H002", Family: "meanrev", Statement: "weekly inverse works", Horizon: "1w",
		Prior: 0.25, MaxEdge: 0.10, Posterior: 0.52, Status: rl.StatusUncertain,
		Replications: 1, Regimes: 1, OpenQuestions: []string{"survives high VIX?"},
	}, now); err != nil {
		t.Fatalf("upsert H002: %v", err)
	}
	if err := st.UpsertLedgerHypothesis(ctx, rl.Hypothesis{
		ID: "H008", Family: "momentum", Statement: "never graded yet", Horizon: "1w",
		Prior: 0.20, MaxEdge: 0.10, Posterior: 0.30, Status: rl.StatusDoubtful,
	}, now); err != nil {
		t.Fatalf("upsert H008: %v", err)
	}
	for _, e := range []rl.Evidence{
		{HypID: "H002", Ts: now, Kind: rl.KindExperiment, K: 546, N: 1006, P0: 0.5070,
			BF: 5.2, Note: "live grade", WindowFrom: 1, WindowTo: 2},
		{HypID: "H002", Ts: now, Kind: rl.KindAttack, BF: 0.6,
			Note: "single-regime: calm-vol only"},
		{HypID: "H002", Ts: now + 60, Kind: rl.KindReplication, K: 30, N: 50, P0: 0.5,
			BF: 2.0, Note: "live replication on fresh window", WindowFrom: 3, WindowTo: 4},
		{HypID: "H002", Ts: now + 120, Kind: rl.KindBacktest, K: 12, N: 20, P0: 0.5,
			BF: 1.5, WindowFrom: 5, WindowTo: 6,
			Note: "era=bear_2022 12/20 winning weeks (240 obs) — historical backfill, survivor universe"},
	} {
		if err := st.InsertLedgerEvidence(ctx, e); err != nil {
			t.Fatalf("evidence %s: %v", e.Kind, err)
		}
	}
	weekTs := int64(1_650_000_000) // April 2022 — inside bear_2022
	if err := st.UpsertResearchWeeks(ctx, []store.ResearchWeek{{
		SymbolID: 7, Week: weekTs / 604800, Ts: weekTs,
		Vec: map[string]float64{"pressure_score": -0.4}, FwdReturn: 0.012,
		Up: true, Era: "bear_2022",
	}}, now); err != nil {
		t.Fatalf("research week: %v", err)
	}
}

type researchLedgerResp struct {
	Hypotheses     []rl.Hypothesis         `json:"hypotheses"`
	Evidence       []rl.Evidence           `json:"evidence"`
	Weeks          store.ResearchWeekStats `json:"weeks"`
	EvidenceKinds  map[string]int          `json:"evidenceKinds"`
	LiveVsBacktest map[string]int          `json:"liveVsBacktest"`
	Decay          []decayRow              `json:"decay"`
	Meta           struct {
		Families []rl.FamilyStat `json:"families"`
		Attacks  []rl.AttackStat `json:"attacks"`
	} `json:"meta"`
	Discipline string `json:"discipline"`
}

// The /api/research-ledger handler returns hypotheses (posterior-ordered),
// evidence chains, the meta-analysis (families + attack lethality), the
// research-weeks stats, evidence-kind counts, the live-vs-backtest split, and
// per-hypothesis decay summaries.
func TestResearchLedgerEndpoint(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "rledger_api.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := int64(1_800_000_000)
	seedResearchLedger(t, ctx, st, now)

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	req := httptest.NewRequest(http.MethodGet, "/api/research-ledger", nil)
	rec := httptest.NewRecorder()
	d.researchLedger(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp researchLedgerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Hypotheses) != 2 || resp.Hypotheses[0].ID != "H002" || resp.Hypotheses[0].Posterior != 0.52 {
		t.Fatalf("hypotheses = %+v", resp.Hypotheses)
	}
	if len(resp.Hypotheses[0].OpenQuestions) != 1 {
		t.Errorf("open questions lost in round-trip: %+v", resp.Hypotheses[0])
	}
	if len(resp.Evidence) != 4 {
		t.Errorf("evidence rows = %d, want 4", len(resp.Evidence))
	}
	if len(resp.Meta.Families) != 2 || resp.Meta.Families[0].Family != "meanrev" {
		t.Errorf("meta families = %+v", resp.Meta.Families)
	}
	if len(resp.Meta.Attacks) != 1 || resp.Meta.Attacks[0].Attack != "single-regime" || resp.Meta.Attacks[0].Failed != 1 {
		t.Errorf("meta attacks = %+v", resp.Meta.Attacks)
	}

	if resp.Weeks.Rows != 1 || resp.Weeks.Symbols != 1 || resp.Weeks.Weeks != 1 ||
		resp.Weeks.ByEra["bear_2022"] != 1 {
		t.Errorf("weeks stats = %+v", resp.Weeks)
	}
	wantKinds := map[string]int{
		rl.KindExperiment: 1, rl.KindAttack: 1, rl.KindReplication: 1, rl.KindBacktest: 1,
	}
	for k, n := range wantKinds {
		if resp.EvidenceKinds[k] != n {
			t.Errorf("evidenceKinds[%s] = %d, want %d", k, resp.EvidenceKinds[k], n)
		}
	}
	if resp.LiveVsBacktest["live"] != 1 || resp.LiveVsBacktest["backtest"] != 1 {
		t.Errorf("liveVsBacktest = %+v", resp.LiveVsBacktest)
	}

	if len(resp.Decay) != 2 {
		t.Fatalf("decay rows = %d, want 2", len(resp.Decay))
	}
	// H002's chain: 1/3 × 5.2 × 0.6 × 2.0 × 1.5 odds ⇒ posterior ≈ 0.7573,
	// which is also the running peak (the chain ends at its maximum).
	h2 := resp.Decay[0]
	if h2.ID != "H002" || h2.Peak < 0.75 || h2.Peak > 0.76 ||
		math.Abs(h2.Peak-h2.Current) > 1e-9 {
		t.Errorf("H002 decay = %+v", h2)
	}
	if h2.EdgeWeakening || h2.Stale {
		t.Errorf("H002 decay flags = %+v (evidence is fresh, at peak)", h2)
	}
	// H008 was never graded: peak = current = clamped prior, stale by definition.
	h8 := resp.Decay[1]
	if h8.ID != "H008" || math.Abs(h8.Peak-0.20) > 1e-9 ||
		math.Abs(h8.Current-0.20) > 1e-9 || !h8.Stale || h8.EdgeWeakening {
		t.Errorf("H008 decay = %+v", h8)
	}

	if !strings.Contains(resp.Discipline, "BACKTEST evidence on a survivor universe") {
		t.Errorf("discipline line missing backtest caveat: %q", resp.Discipline)
	}
}

// ?id narrows the evidence chain AND the decay rows to one hypothesis — decay
// for hypotheses whose chains were not loaded must not be fabricated.
func TestResearchLedgerIDFilter(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "rledger_api_id.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	now := int64(1_800_000_000)
	seedResearchLedger(t, ctx, st, now)

	d := Deps{St: st, Cfg: baseCfg(), Version: "test", Started: time.Now()}
	req := httptest.NewRequest(http.MethodGet, "/api/research-ledger?id=H008", nil)
	rec := httptest.NewRecorder()
	d.researchLedger(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp researchLedgerResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Hypotheses) != 2 {
		t.Errorf("hypotheses = %d, want 2 (id filter narrows evidence only)", len(resp.Hypotheses))
	}
	if len(resp.Evidence) != 0 {
		t.Errorf("evidence rows = %d, want 0 for ungraded H008", len(resp.Evidence))
	}
	if len(resp.Decay) != 1 || resp.Decay[0].ID != "H008" || !resp.Decay[0].Stale {
		t.Errorf("decay = %+v, want single stale H008 row", resp.Decay)
	}
	if resp.LiveVsBacktest["live"] != 0 || resp.LiveVsBacktest["backtest"] != 0 {
		t.Errorf("liveVsBacktest = %+v, want zeros under H008 filter", resp.LiveVsBacktest)
	}
}
