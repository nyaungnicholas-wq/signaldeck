// SmartMoneyScorer (SMART MONEY FACTS wave): the worker behind the per-symbol
// Smart Money Score. It turns ALREADY-INGESTED positioning data — SEC Form 4
// open-market insider trades, FINRA short interest / Reg SHO short volume,
// crypto perp funding, and SEC 13F holdings — into ONE transparent, decomposed
// score via the PURE internal/smartmoney engine, upserts it, and emits two
// deduped positioning events (insider-cluster buy, short-squeeze setup).
//
// HONESTY: the score is a read of what INFORMED PARTICIPANTS ARE DOING, NOT a
// forecast. Reads are best-effort per source (an error just makes that source
// absent this pass, never a fabricated 0), a symbol with no positioning data is
// simply not scored (Available=false → no row), and one symbol's failure never
// fails the whole run. Freshness/window gates reuse the SAME constants and
// helpers as the alphax feature builder (alphaxfeat.go).
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/smartmoney"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// SmartMoneyScorer computes and persists the per-symbol Smart Money Score.
type SmartMoneyScorer struct {
	St *store.Store
}

func (w *SmartMoneyScorer) Name() string            { return "smart-money-scorer" }
func (w *SmartMoneyScorer) Interval() time.Duration { return time.Hour }

func (w *SmartMoneyScorer) Run(ctx context.Context) (string, error) {
	syms, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	now := time.Now()
	nowUnix := now.Unix()
	insiderCutoff := nowUnix - insiderWindowSecs

	clusterMin := envInt("SIGNALDECK_INSIDER_CLUSTER_MIN", 2)
	squeezeDTCMin := smEnvFloat("SIGNALDECK_SQUEEZE_DTC_MIN", 5)
	squeezeZMin := smEnvFloat("SIGNALDECK_SQUEEZE_Z_MIN", 1.5)

	scored, events, readErrs, writeErrs := 0, 0, 0, 0

	for _, s := range syms {
		in := smartmoney.Inputs{DaysToCover: -1} // -1 = no short-interest row (absent)
		pay := smartmoney.Payload{WindowDays: 90}

		// Per-source raw values kept for event gating + the payload.
		var netRatio float64
		var insiderTxDay string // latest open-market buy tx UTC day (event bucket)
		var shortVolDay string  // latest short-vol day (event bucket)

		if s.Market == md.Crypto {
			// Crypto: the only positioning source we hold is perp funding.
			if p, ok, err := w.St.LatestCryptoPerp(ctx, s.ID); err != nil {
				readErrs++
			} else if ok && nowUnix-p.Ts <= fundingMaxAgeSecs {
				in.Funding, in.FundingOK = p.Funding, true
				f := p.Funding
				pay.Funding = &f
			}
		} else {
			// Insider: open-market Form 4 net dollars + distinct buyers over 90d.
			if buys, sells, nBuys, nSells, err := w.St.InsiderNetActivity(ctx, s.ID, insiderCutoff); err != nil {
				readErrs++
			} else if nBuys+nSells > 0 && buys+sells > 0 {
				in.InsiderBuys, in.InsiderSells, in.InsiderSellTx = buys, sells, nSells
				netRatio = (buys - sells) / (buys + sells)
				pay.InsiderNet = buys - sells
				// Distinct buyers + latest buy tx day from the P (open-market
				// buy) transactions inside the 90d window.
				if trades, err := w.St.InsiderTrades(ctx, s.ID, "P", 500); err != nil {
					readErrs++
				} else {
					distinct := map[string]struct{}{}
					var maxTx int64
					for _, t := range trades {
						if t.TxTs < insiderCutoff {
							continue
						}
						distinct[t.Insider] = struct{}{}
						if t.TxTs > maxTx {
							maxTx = t.TxTs
						}
					}
					in.InsiderDistinctBuyers = len(distinct)
					pay.DistinctBuyers = len(distinct)
					if maxTx > 0 {
						insiderTxDay = time.Unix(maxTx, 0).UTC().Format("2006-01-02")
					}
				}
			}

			// Short interest → days-to-cover (settlement ≤20d old).
			if rows, err := w.St.ShortInterestRecent(ctx, s.ID, 1); err != nil {
				readErrs++
			} else if len(rows) > 0 && rows[0].DaysToCover > 0 &&
				dayWithinUTC(rows[0].Settlement, now, shortIntMaxAgeDays) {
				in.DaysToCover = rows[0].DaysToCover
				dtc := rows[0].DaysToCover
				pay.DaysToCover = &dtc
			}

			// Reg SHO short-volume ratio z vs the symbol's own trailing 30d.
			if series, err := w.St.ShortVolumeSeries(ctx, s.ID, 30); err != nil {
				readErrs++
			} else if len(series) > 0 {
				ratios := make([]float64, len(series))
				for i, r := range series {
					ratios[i] = r.ShortPct
				}
				if z, ok := trailingZ(ratios, shortVolMinPrior); ok {
					in.ShortVolZ, in.ShortVolZOK = z, true
					zz := z
					pay.ShortVolZ = &zz
				}
				shortVolDay = series[len(series)-1].Day
			}

			// 13F institutional holders (manager count + summed notional).
			if holds, err := w.St.InstHoldingsBySymbol(ctx, s.ID, 200); err != nil {
				readErrs++
			} else if len(holds) > 0 {
				var notional float64
				for _, h := range holds {
					notional += h.Value
				}
				in.InstManagers, in.InstNotional = len(holds), notional
				pay.InstManagers, pay.InstNotional = len(holds), notional
			}
		}

		res := smartmoney.Score(in)
		if !res.Available {
			continue // honest absence: no positioning data → no row
		}
		pay.Factors = res.Factors
		blob, err := json.Marshal(pay)
		if err != nil {
			writeErrs++
			continue
		}
		if err := w.St.UpsertSmartMoneyScore(ctx, store.SmartMoneyScore{
			SymbolID: s.ID, Ts: nowUnix, Score: res.Score, Label: res.Label, Payload: string(blob),
		}); err != nil {
			writeErrs++
			continue
		}
		scored++

		// ── EVENTS (day-bucket deduped) ────────────────────────────────────
		// insider_cluster: a cluster of DISTINCT insiders net-buying hard.
		if in.InsiderDistinctBuyers >= clusterMin && netRatio > 0.5 && insiderTxDay != "" {
			fresh, err := w.St.InsertSmartMoneyEvent(ctx, store.SmartMoneyEvent{
				SymbolID: s.ID, Ts: nowUnix, Kind: "insider_cluster",
				Detail: fmt.Sprintf("%s: %d insiders net-bought %s (Form 4)",
					s.Symbol, in.InsiderDistinctBuyers, smUSD(pay.InsiderNet)),
				DayBucket: insiderTxDay,
			})
			if err != nil {
				writeErrs++
			} else if fresh {
				events++
			}
		}
		// squeeze_setup: elevated days-to-cover AND elevated short-volume z.
		if in.DaysToCover >= squeezeDTCMin && in.ShortVolZOK && in.ShortVolZ >= squeezeZMin && shortVolDay != "" {
			fresh, err := w.St.InsertSmartMoneyEvent(ctx, store.SmartMoneyEvent{
				SymbolID: s.ID, Ts: nowUnix, Kind: "squeeze_setup",
				Detail: fmt.Sprintf("%s: squeeze setup — DTC %.1f, short-vol %+.1fσ",
					s.Symbol, in.DaysToCover, in.ShortVolZ),
				DayBucket: shortVolDay,
			})
			if err != nil {
				writeErrs++
			} else if fresh {
				events++
			}
		}
	}

	detail := fmt.Sprintf("scored %d symbol(s), %d new event(s)", scored, events)
	if readErrs > 0 {
		detail += fmt.Sprintf("; %d source read(s) unavailable this pass", readErrs)
	}
	if writeErrs > 0 {
		detail += fmt.Sprintf("; %d write error(s)", writeErrs)
	}
	return detail, nil
}

// smEnvFloat reads a float env override, falling back to def when unset or
// malformed (the float sibling of envInt; pipeline has no envFloat helper).
func smEnvFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// smUSD formats a dollar amount compactly for event detail strings.
func smUSD(v float64) string {
	a := v
	if a < 0 {
		a = -a
	}
	switch {
	case a >= 1e9:
		return fmt.Sprintf("$%.1fB", v/1e9)
	case a >= 1e6:
		return fmt.Sprintf("$%.1fM", v/1e6)
	case a >= 1e3:
		return fmt.Sprintf("$%.0fK", v/1e3)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}
