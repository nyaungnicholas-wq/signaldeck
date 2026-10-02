package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/ledgeranchor"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// TestTrackRecordLedgerCarriesFailingAnchors (PROOFSTRIP): the /today chip and
// /lab/track-record read the track-record ledger block. A regenerated chain
// still verifies intact, so the block must carry the failing-anchor count the
// /proof headline reads, or those chips stay green over a rewritten history.
func TestTrackRecordLedgerCarriesFailingAnchors(t *testing.T) {
	t.Setenv(ledgeranchor.EnvKeyPath, filepath.Join(t.TempDir(), "anchor.key"))
	srv, st, d := newTestServer(t, func(c *config.Config) { c.APIToken = ledgerTestToken })
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, "admin", "hash", true); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	d.registerLedger(mux)
	srv.Config.Handler = d.secure(mux)
	sym, err := st.UpsertSymbol(ctx, "AAPL", md.Stocks, "Apple")
	if err != nil {
		t.Fatal(err)
	}
	appendLedgerRows(t, st, sym.ID, 12, 0.5)
	if v := getLedgerVerify(t, srv, ""); !v.Tamper.Anchoring.Wrote {
		t.Fatalf("the honest chain was not anchored: %+v", v.Tamper.Anchoring)
	}
	ledger := func() map[string]any {
		t.Helper()
		resp, err := d.buildTrackRecord(ctx, md.H1d)
		if err != nil {
			t.Fatal(err)
		}
		l, ok := resp["ledger"].(map[string]any)
		if !ok {
			t.Fatalf("no ledger block: %v", resp["ledger"])
		}
		return l
	}
	failing := func(l map[string]any) string {
		te, _ := l["tamperEvidence"].(map[string]any)
		return fmt.Sprint(te["failingAnchors"])
	}

	if l := ledger(); l["intact"] != true || failing(l) != "0" {
		t.Errorf("honest anchored chain: %v", l)
	}
	for _, q := range []string{
		`DELETE FROM prediction_ledger`,
		`DELETE FROM meta WHERE k='ledger_verify_checkpoint'`,
		`DELETE FROM sqlite_sequence WHERE name='prediction_ledger'`,
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	appendLedgerRows(t, st, sym.ID, 12, 0.93)
	if l := ledger(); l["intact"] != true || failing(l) != "1" {
		t.Errorf("regenerated chain: want intact (the chain cannot see it) with 1 failing anchor: %v", l)
	}
}
