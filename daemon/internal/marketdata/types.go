// Package marketdata defines the shared value types of SignalDeck. Every
// worker and the API code against these — they are the contract.
package marketdata

// Market distinguishes asset classes (and therefore data sources).
type Market string

const (
	Crypto Market = "crypto"
	Stocks Market = "stocks"
)

// Timeframe is a bar resolution.
type Timeframe string

const (
	TF1m Timeframe = "1m"
	TF1h Timeframe = "1h"
	TF1d Timeframe = "1d"
)

// Horizon is a score/expectancy forward-looking window.
type Horizon string

const (
	H1h Horizon = "1h"
	H1d Horizon = "1d"
	H1w Horizon = "1w"
)

// Horizons in display order.
var Horizons = []Horizon{H1h, H1d, H1w}

// BarsPerHorizon maps a horizon to (timeframe, number of bars forward) used
// when resolving forward returns. 1w uses 5 trading days.
func BarsPerHorizon(h Horizon) (Timeframe, int) {
	switch h {
	case H1h:
		return TF1m, 60
	case H1d:
		return TF1d, 1
	default: // H1w
		return TF1d, 5
	}
}

// Symbol is one tracked instrument.
type Symbol struct {
	ID      int64  `json:"id"`
	Symbol  string `json:"symbol"` // canonical: "BTC/USD", "AAPL"
	Market  Market `json:"market"`
	Name    string `json:"name"`
	Active  bool   `json:"active"` // live subscription on
	AddedAt int64  `json:"addedAt"`
	// Stream marks the STREAMED HOT SET (live websocket + full 1m pipeline),
	// as opposed to the BROAD DAILY-ONLY universe (REST daily bars only).
	// Broad-universe wave; legacy/daily-only symbols are false.
	Stream bool `json:"stream"`
}

// Bar is one OHLCV bar. Ts is the bar OPEN time, unix seconds UTC.
type Bar struct {
	SymbolID int64     `json:"-"`
	TF       Timeframe `json:"-"`
	Ts       int64     `json:"ts"`
	Open     float64   `json:"o"`
	High     float64   `json:"h"`
	Low      float64   `json:"l"`
	Close    float64   `json:"c"`
	Volume   float64   `json:"v"`
}

// Snap1s is one crypto microstructure snapshot (1-second cadence).
type Snap1s struct {
	SymbolID   int64   `json:"-"`
	Ts         int64   `json:"ts"`
	Bid        float64 `json:"bid"`
	Ask        float64 `json:"ask"`
	Mid        float64 `json:"mid"`
	WMid       float64 `json:"wmid"`
	ImbSigned  float64 `json:"imb"`
	Spread     float64 `json:"spread"`
	ApplyLatNs int64   `json:"applyLatNs"`
}

// ScoreComponent is one signal's contribution to a Pressure Score.
type ScoreComponent struct {
	Name    string  `json:"name"`    // e.g. "rsi", "trend_sma", "imbalance"
	Value   float64 `json:"value"`   // raw indicator value (display)
	Norm    float64 `json:"norm"`    // normalized to [-1,+1] (sell..buy)
	Weight  float64 `json:"weight"`  // weight in the composite
	Contrib float64 `json:"contrib"` // norm*weight (sums to score)
	Note    string  `json:"note"`    // one-line human explanation
}

// Score is a persisted per-symbol, per-horizon Pressure Score.
type Score struct {
	SymbolID   int64            `json:"-"`
	Symbol     string           `json:"symbol,omitempty"`
	Ts         int64            `json:"ts"`
	Horizon    Horizon          `json:"horizon"`
	Score      float64          `json:"score"` // [-1,+1]
	Components []ScoreComponent `json:"components"`
}

// ScoreOutcome pairs a past score with its realized forward return —
// the honesty backtest's raw material.
type ScoreOutcome struct {
	SymbolID   int64    `json:"-"`
	Symbol     string   `json:"symbol,omitempty"`
	Ts         int64    `json:"ts"`
	Horizon    Horizon  `json:"horizon"`
	Score      float64  `json:"score"`
	FwdReturn  *float64 `json:"fwdReturn"` // nil until resolved
	ResolvedAt *int64   `json:"resolvedAt"`
	// SettleTs is the base bar this outcome was graded from — the unit of
	// independent evidence (SettleDay). 0 = unknown, folds back to the day.
	SettleTs int64 `json:"-"`
}

// Expectancy is a conditional forward-return statistic: "when this symbol was
// in state X over the lookback, the next <horizon> returned …". This is the
// app's honest 'prediction': a measured tendency with sample size attached,
// never a point forecast.
type Expectancy struct {
	SymbolID  int64   `json:"-"`
	Horizon   Horizon `json:"horizon"`
	StateKey  string  `json:"stateKey"` // e.g. "rsi_low|above_200sma"
	N         int     `json:"n"`
	MeanFwd   float64 `json:"meanFwd"`
	MedianFwd float64 `json:"medianFwd"`
	HitRate   float64 `json:"hitRate"` // fraction of positive fwd returns
	Stdev     float64 `json:"stdev"`
	UpdatedAt int64   `json:"updatedAt"`
}

// Insight is a stored plain-English read with its evidence attached.
type Insight struct {
	ID       int64  `json:"id"`
	Scope    string `json:"scope"` // "symbol" | "market"
	SymbolID *int64 `json:"-"`
	Symbol   string `json:"symbol,omitempty"`
	Ts       int64  `json:"ts"`
	Headline string `json:"headline"`
	Body     string `json:"body"`
	Data     string `json:"data"` // JSON evidence blob
}

// WorkerRun is one execution record of an in-app agent.
type WorkerRun struct {
	ID         int64  `json:"id"`
	Worker     string `json:"worker"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt *int64 `json:"finishedAt"`
	Status     string `json:"status"` // running | ok | error
	Detail     string `json:"detail"`
}

// DQEvent is one data-quality incident.
type DQEvent struct {
	ID       int64  `json:"id"`
	SymbolID *int64 `json:"-"`
	Symbol   string `json:"symbol,omitempty"`
	Ts       int64  `json:"ts"`
	Kind     string `json:"kind"` // gap | stale | resync | drop | error
	Detail   string `json:"detail"`
}
