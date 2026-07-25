// Package pairsstudy serves the frozen result of the cointegration pairs-trading
// study that resolved research hypothesis H018 (CORR63) DO-NOT-SHIP on
// 2026-07-25.
//
// The study is an offline walk-forward backtest (tools/pairs_trading.py, ~45
// minutes over 918 SIC-sectored symbols), not a live worker, so the numbers are
// embedded exactly as the tool emitted them rather than re-derived here. A
// re-derivation could drift from the writeup and the ledger row, and the whole
// value of this surface is that the three agree.
//
// The reason a NEGATIVE result gets a product surface at all: H018 sat parked
// for eight days at 73.1% with a CI whose lower bound straddled the product bar,
// and the thing that killed it was not more data — it was building the tradable
// form of the claim. That failure mode (a well-estimated statistic that no
// position can monetize) is the one this platform is most exposed to, so it is
// worth showing rather than filing.
package pairsstudy

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

//go:embed result.json
var resultJSON []byte

// RanOn is the date the embedded run was produced. The study is frozen: it does
// not re-run on a cadence, so a timestamp from the file's mtime would be a lie
// about freshness.
const RanOn = "2026-07-25"

// Persistence is the mechanism finding — the contrast that explains the whole
// verdict. Formation-window rank vs next-window rank, Spearman, over
// non-overlapping walk-forward blocks.
type Persistence struct {
	// CorrRho is how well trailing CORRELATION rank predicts forward
	// correlation rank. High: pairs that moved together keep moving together.
	CorrRho   float64  `json:"corrRho"`
	CorrRhoLo *float64 `json:"corrRhoLo"`
	CorrRhoHi *float64 `json:"corrRhoHi"`
	// CointRho is the same statistic for COINTEGRATION rank — the property a
	// spread trade actually needs. Near zero: it does not persist at all.
	CointRho float64 `json:"cointRho"`
	Blocks   int     `json:"blocks"`

	// Binary framings of forward co-movement persistence. Corroborating, NOT
	// restatements of the ledgered 73.1% (which measured SPY-correlation
	// tiering, a different quantity).
	AboveMedian    float64 `json:"aboveMedian"`
	AboveMedianLo  float64 `json:"aboveMedianLo"`
	AboveMedianHi  float64 `json:"aboveMedianHi"`
	TopTercile     float64 `json:"topTercile"`
	TopDecile      float64 `json:"topDecile"`
	PairsEvaluated int     `json:"pairsEvaluated"`
}

// Exits records how trades ended. A book that mostly stops out and relies on a
// few large convergences has a fat left tail, which matters more than the mean.
type Exits struct {
	Revert    int `json:"revert"`
	Stop      int `json:"stop"`
	WindowEnd int `json:"windowEnd"`
	Delisted  int `json:"delisted"`
}

// Arm is one selection rule traded on identical rules at one cost level.
type Arm struct {
	Arm       string  `json:"arm"`
	Trades    int     `json:"trades"`
	MeanRet   float64 `json:"meanRet"`
	MeanLo    float64 `json:"meanLo"`
	MeanHi    float64 `json:"meanHi"`
	MedianRet float64 `json:"medianRet"`
	WinRate   float64 `json:"winRate"`
	WinLo     float64 `json:"winLo"`
	WinHi     float64 `json:"winHi"`
	Sharpe    float64 `json:"sharpe"`
	TotalRet  float64 `json:"totalRet"`
	AvgBars   float64 `json:"avgBars"`
	Exits     Exits   `json:"exits"`
	// ExcludesZero is the only question that decides shipping. The CI is a
	// BLOCK bootstrap (one cluster per walk-forward window), because trades
	// opened in the same quarter share a regime.
	ExcludesZero bool `json:"excludesZero"`
}

// CostLevel is the three arms at one friction assumption. Pairs trading is
// cost-dominated, so a single optimistic cost number would be the finding.
type CostLevel struct {
	CostBps float64 `json:"costBps"`
	Coint   Arm     `json:"coint"`
	Random  Arm     `json:"random"`
	Worst   Arm     `json:"worst"`
	// SelectionEdge is cointegrated minus random-same-sector, per trade. This
	// is what the selection rule is worth. Everything else the arms share.
	SelectionEdge float64 `json:"selectionEdge"`
}

// Study is the whole payload.
type Study struct {
	Hypothesis  string       `json:"hypothesis"`
	Verdict     string       `json:"verdict"`
	RanOn       string       `json:"ranOn"`
	Persistence Persistence  `json:"persistence"`
	Costs       []CostLevel  `json:"costs"`
	Method      Method       `json:"method"`
	Limitations []string     `json:"limitations"`
	Mechanism   string       `json:"mechanism"`
	Why         string       `json:"why"`
	Lessons     []string     `json:"lessons"`
	Rules       TradingRules `json:"rules"`
}

// Method records the choices that decide whether a pairs backtest is honest at
// all, because every one of them is a documented way the literature inflates
// this exact strategy family.
type Method struct {
	Window          string `json:"window"`
	Universe        string `json:"universe"`
	FrozenParams    string `json:"frozenParams"`
	CriticalValues  string `json:"criticalValues"`
	MatchedNulls    string `json:"matchedNulls"`
	BlockBootstrap  string `json:"blockBootstrap"`
	CostSweepReason string `json:"costSweepReason"`
}

// TradingRules are the entry/exit thresholds, stated so the numbers above are
// reproducible rather than merely quoted.
type TradingRules struct {
	Entry        string `json:"entry"`
	Exit         string `json:"exit"`
	Stop         string `json:"stop"`
	ForceClose   string `json:"forceClose"`
	PairsPerFold int    `json:"pairsPerFold"`
}

// raw mirrors the JSON the Python tool writes. Kept unexported and separate
// from the API shape so a change in the tool's output is a compile-time or
// decode-time problem here, not a silently wrong number on the page.
type raw struct {
	H018 struct {
		MeanRho  float64   `json:"mean_rho"`
		RhoCI    []float64 `json:"rho_ci"`
		CointRho float64   `json:"coint_rho"`
		Blocks   int       `json:"blocks"`
		TopAcc   float64   `json:"top_acc"`
		TopCI    []float64 `json:"top_ci"`
		TopK     int       `json:"top_k"`
		TopN     int       `json:"top_n"`
		TerAcc   float64   `json:"ter_acc"`
		DecAcc   float64   `json:"dec_acc"`
	} `json:"h018"`
	ByCost map[string]map[string]rawArm `json:"by_cost"`
}

type rawArm struct {
	NTrades   int       `json:"n_trades"`
	MeanRet   float64   `json:"mean_ret"`
	MeanCI    []float64 `json:"mean_ci"`
	WinRate   float64   `json:"win_rate"`
	WinWilson []float64 `json:"win_wilson"`
	MedianRet float64   `json:"median_ret"`
	Sharpe    float64   `json:"sharpe"`
	TotalRet  float64   `json:"total_ret"`
	AvgBars   float64   `json:"avg_bars"`
	Exits     struct {
		Revert    int `json:"revert"`
		Stop      int `json:"stop"`
		WindowEnd int `json:"window_end"`
		Delisted  int `json:"delisted"`
	} `json:"exits"`
}

// Load parses the embedded run. It returns an error rather than panicking so a
// malformed embed degrades to one failed endpoint instead of a dead daemon.
func Load() (Study, error) {
	var r raw
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		return Study{}, fmt.Errorf("pairsstudy: decode embedded result: %w", err)
	}
	if len(r.ByCost) == 0 {
		return Study{}, fmt.Errorf("pairsstudy: embedded result has no cost levels")
	}

	arm := func(name string, m map[string]rawArm, key string) Arm {
		a := m[key]
		lo, hi := 0.0, 0.0
		if len(a.MeanCI) == 2 {
			lo, hi = a.MeanCI[0], a.MeanCI[1]
		}
		wlo, whi := 0.0, 0.0
		if len(a.WinWilson) == 2 {
			wlo, whi = a.WinWilson[0], a.WinWilson[1]
		}
		return Arm{
			Arm:       name,
			Trades:    a.NTrades,
			MeanRet:   a.MeanRet,
			MeanLo:    lo,
			MeanHi:    hi,
			MedianRet: a.MedianRet,
			WinRate:   a.WinRate,
			WinLo:     wlo,
			WinHi:     whi,
			Sharpe:    a.Sharpe,
			TotalRet:  a.TotalRet,
			AvgBars:   a.AvgBars,
			Exits: Exits{
				Revert:    a.Exits.Revert,
				Stop:      a.Exits.Stop,
				WindowEnd: a.Exits.WindowEnd,
				Delisted:  a.Exits.Delisted,
			},
			// Both bounds on the same side of zero, and only then.
			ExcludesZero: (lo > 0 && hi > 0) || (lo < 0 && hi < 0),
		}
	}

	costs := make([]CostLevel, 0, len(r.ByCost))
	for k, arms := range r.ByCost {
		bps, err := strconv.ParseFloat(k, 64)
		if err != nil {
			return Study{}, fmt.Errorf("pairsstudy: cost key %q: %w", k, err)
		}
		c := arm("cointegrated", arms, "coint")
		rnd := arm("random same-sector", arms, "random")
		costs = append(costs, CostLevel{
			CostBps:       bps,
			Coint:         c,
			Random:        rnd,
			Worst:         arm("least cointegrated", arms, "worst"),
			SelectionEdge: c.MeanRet - rnd.MeanRet,
		})
	}
	sort.Slice(costs, func(i, j int) bool { return costs[i].CostBps < costs[j].CostBps })

	p := Persistence{
		CorrRho:        r.H018.MeanRho,
		CointRho:       r.H018.CointRho,
		Blocks:         r.H018.Blocks,
		AboveMedian:    r.H018.TopAcc,
		TopTercile:     r.H018.TerAcc,
		TopDecile:      r.H018.DecAcc,
		PairsEvaluated: r.H018.TopN,
	}
	if len(r.H018.RhoCI) == 2 {
		lo, hi := r.H018.RhoCI[0], r.H018.RhoCI[1]
		p.CorrRhoLo, p.CorrRhoHi = &lo, &hi
	}
	if len(r.H018.TopCI) == 2 {
		p.AboveMedianLo, p.AboveMedianHi = r.H018.TopCI[0], r.H018.TopCI[1]
	}

	return Study{
		Hypothesis: "H018 / CORR63 — \"63-day SPY-correlation regime persists (trailing rank is predictable).\" " +
			"Ledgered at 73.1% with a quarter-clustered CI of [0.671, 0.795] and parked on 2026-07-17 because " +
			"the lower bound straddled the 0.70 product bar.",
		Verdict: "DO NOT SHIP. The persistence is real and strong. It is also not harvestable. " +
			"More quarters would never have settled it, because a rank-persistence statistic is not a " +
			"decision anyone can act on — so the hypothesis was resolved by building its tradable form " +
			"instead, and the tradable form does not pay.",
		RanOn:       RanOn,
		Persistence: p,
		Costs:       costs,
		Method: Method{
			Window: "Walk-forward: 252 sessions of formation, then the next 63 sessions traded, stepping a " +
				"full 63 so no two trade windows share a day. 26 non-overlapping blocks, 2019 to 2026.",
			Universe: "918 SIC-sectored symbols, including names the platform no longer tracks, so the " +
				"result is not conditioned on the current watchlist.",
			FrozenParams: "Hedge ratio (OLS on log prices), spread mean and spread standard deviation all " +
				"come from the FORMATION window. Recomputing them inside the trade window is the classic " +
				"pairs lookahead and it manufactures most published Sharpes in this family.",
			CriticalValues: "Engle-Granger critical values (-3.34 at 5%), not standard ADF values. Applying " +
				"-2.86 to a FITTED regression residual is the most common way a pairs study finds " +
				"cointegration that is not there.",
			MatchedNulls: "The selected arm is compared against random same-sector pairs and against the " +
				"LEAST cointegrated pairs, traded on identical rules. If the selection rule carries no " +
				"information, the three arms agree — and here they do.",
			BlockBootstrap: "Each walk-forward block is one cluster. Trades opened in the same quarter share " +
				"a regime; resampling individual trades would treat hundreds of correlated outcomes as " +
				"hundreds of independent ones and return an interval several times too narrow.",
			CostSweepReason: "A sweep rather than one number, because pairs trading is cost-dominated and " +
				"the break-even friction level IS the finding.",
		},
		Rules: TradingRules{
			Entry:        "|spread z| >= 2",
			Exit:         "|spread z| <= 0.5",
			Stop:         "|spread z| >= 4",
			ForceClose:   "at the end of the 63-session trade window",
			PairsPerFold: 20,
		},
		Mechanism: "The persistent thing is shared market BETA. Correlation rank persists hard and " +
			"cointegration rank does not persist at all — a pair that tests beautifully cointegrated over " +
			"252 sessions is exactly as likely as any other pair to test cointegrated over the next 63. " +
			"Every pair inside a sector already carries the market beta, so it confers no advantage in " +
			"choosing WHICH pair to trade, and a dollar-neutral spread is constructed precisely to cancel " +
			"it out. The component a spread position actually monetizes — the stationary idiosyncratic " +
			"residual — is the component with zero persistence.",
		Why: "This also explains why the beta-regime variant of the hypothesis failed at 64.1% while the " +
			"correlation variant reached 73.1%: correlation persistence and beta persistence are the same " +
			"underlying fact measured two ways, and correlation just measures it with less noise. H018 was " +
			"never close to a product. It was a well-estimated measurement of market beta.",
		Limitations: []string{
			"DELISTING RISK IS NOT TESTED. delisted_at is NULL for every symbol in this database and only " +
				"about 18 of 746 inactive names ever stop printing bars — \"inactive\" means dropped from the " +
				"watchlist, not delisted. Including inactive names removes watchlist-selection bias, but the " +
				"trade that matters most to a pairs book, the leg that goes to zero and never converges, is " +
				"essentially absent: zero legs died mid-trade across 938 trades. Tail risk is understated by " +
				"an unknown amount — which only makes a do-not-ship verdict safer.",
			"Daily closes only. Real pairs desks trade intraday; a 63-day horizon on daily bars is the slow " +
				"end of this strategy family.",
			"Sharpe is portfolio-level with idle capital — flat days are included.",
			"P&L is size-not-frequency and concentrated: winners are far larger than losers, roughly half of " +
				"all trades exit on the stop, and the return lives in 2020Q1, 2022Q3 and 2023Q4. That is a " +
				"regime bet, not a strategy.",
		},
		Lessons: []string{
			"A 4-sector smoke test returned +0.860% per trade with an interval EXCLUDING zero. The full " +
				"918-symbol universe reversed it completely. The small-sample version was the flattering one, " +
				"and stopping early would have shipped a fake.",
			"A statistic can be well estimated, out-of-sample stable, and still have no tradable content. " +
				"The test that settles it is not more data — it is constructing the position that would have " +
				"to make the money.",
		},
	}, nil
}
