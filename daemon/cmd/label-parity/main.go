// label-parity is a research harness for daemon-vs-Python label parity.
// It reads JSON cases from stdin, calls the resolver functions from
// github.com/nyaungnicholas-wq/signaldeck/internal/structregime,
// and writes the results to stdout. It changes no production code and must be invoked as:
//   go run -C daemon ./cmd/label-parity < cases.json
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/nyaungnicholas-wq/signaldeck/internal/structregime"
)

type nfloat float64

func (nf *nfloat) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*nf = nfloat(math.NaN())
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*nf = nfloat(f)
	return nil
}

func toFloat64Slice(src []nfloat) []float64 {
	dst := make([]float64, len(src))
	for i, v := range src {
		dst[i] = float64(v)
	}
	return dst
}

func barReturns2(closes []float64) []float64 {
	if len(closes) < 2 {
		return nil
	}
	out := make([]float64, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		out[i-1] = closes[i]/closes[i-1] - 1
	}
	return out
}

type Input struct {
	Cases []Case `json:"cases"`
}
type Case struct {
	ID          string  `json:"id"`
	Closes      []nfloat `json:"closes"`
	Volumes     []nfloat `json:"volumes"`
	T           int     `json:"t"`
	HorizonDays int     `json:"horizon_days"`
}

type ResolutionOut struct {
	Ok       bool    `json:"ok"`
	Actual   string  `json:"actual"`
	KeyName  string  `json:"key_name"`
	KeyValue *float64 `json:"key_value,omitempty"`
}
type NaiveOut struct {
	Ok   bool   `json:"ok"`
	Label string `json:"label"`
}
type Result struct {
	ID            string         `json:"id"`
	Trend         ResolutionOut  `json:"trend"`
	Liquidity     ResolutionOut  `json:"liquidity"`
	Vol21         ResolutionOut  `json:"vol21"`
	NaiveTrend    NaiveOut       `json:"naive_trend"`
	NaiveLiquidity NaiveOut      `json:"naive_liquidity"`
	NaiveVol21    NaiveOut       `json:"naive_vol21"`
}
type Output struct {
	Results []Result `json:"results"`
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "label-parity: %v\n", err)
		os.Exit(2)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(os.Stderr, "label-parity: %v\n", err)
		os.Exit(2)
	}
	var out Output
	for _, c := range in.Cases {
		closes := toFloat64Slice(c.Closes)
		volumes := toFloat64Slice(c.Volumes)
		rets := barReturns2(closes)

		trend, ok1 := structregime.ResolveTrendAt(closes, c.T, c.HorizonDays)
		liq, ok2 := structregime.ResolveLiquidityAt(closes, volumes, c.T)
		vol21, ok3 := structregime.ResolveVol21At(rets, c.T-1)
		naiveTrend, ok4 := structregime.NaiveTrendAt(closes, c.T)
		naiveLiq, ok5 := structregime.NaiveLiquidityAt(closes, volumes, c.T)
		naiveVol21, ok6 := structregime.NaiveVol21At(rets, c.T-1)

		res := Result{
			ID: c.ID,
			Trend: ResolutionOut{
				Ok:       ok1,
				Actual:   trend.Actual,
				KeyName:  trend.KeyName,
				KeyValue: ptrIfValid(trend.KeyValue),
			},
			Liquidity: ResolutionOut{
				Ok:       ok2,
				Actual:   liq.Actual,
				KeyName:  liq.KeyName,
				KeyValue: ptrIfValid(liq.KeyValue),
			},
			Vol21: ResolutionOut{
				Ok:       ok3,
				Actual:   vol21.Actual,
				KeyName:  vol21.KeyName,
				KeyValue: ptrIfValid(vol21.KeyValue),
			},
			NaiveTrend: NaiveOut{
				Ok:   ok4,
				Label: naiveTrend,
			},
			NaiveLiquidity: NaiveOut{
				Ok:   ok5,
				Label: naiveLiq,
			},
			NaiveVol21: NaiveOut{
				Ok:   ok6,
				Label: naiveVol21,
			},
		}
		out.Results = append(out.Results, res)
	}
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "label-parity: %v\n", err)
		os.Exit(2)
	}
}

func ptrIfValid(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}