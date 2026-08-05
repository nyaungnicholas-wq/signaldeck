package riskgate

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// LimitSource describes where a single limit value came from, for auditing and
// debugging environment-driven configuration. Without it, a run's risk envelope
// is invisible after the fact.
type LimitSource struct {
	Key          string
	Value        float64
	FromEnv      bool
	RawEnv       string
	Rejected     bool
	RejectReason string
}

// Provenance records the full risk envelope that produced a decision, including
// the source and status of every limit. Without it, there is no way to
// reproduce a result or diagnose a misconfiguration.
type Provenance struct {
	Limits     Limits
	Sources    []LimitSource
	Rejections int
}

// DescribeLimits re-resolves every limit the same way Defaults() does, but
// records for each one: the env var key, the final value, whether the env var
// was set, its raw string value if set, and whether the env value was rejected
// (set but unparseable, or out of the accepted domain) together with a one-line
// reason. It returns the resolved Limits plus the per-limit source records.
func DescribeLimits() Provenance {
	p := Provenance{}

	type limitDef struct {
		key      string
		frac     bool // true for fraction, false for integer
		def      float64
		min      float64 // inclusive lower bound (exclusive for >0 case)
		max      float64 // inclusive upper bound
		apply    func(l *Limits, v float64)
	}

	defs := []limitDef{
		{"SIGNALDECK_RISK_MAX_DRAWDOWN", true, 0.20, 0, 1, func(l *Limits, v float64) { l.MaxDrawdown = v }},
		{"SIGNALDECK_RISK_MAX_DAILY_LOSS", true, 0.05, 0, 1, func(l *Limits, v float64) { l.MaxDailyLoss = v }},
		{"SIGNALDECK_RISK_MAX_POSITION_WEIGHT", true, 0.10, 0, 1, func(l *Limits, v float64) { l.MaxPositionWeight = v }},
		{"SIGNALDECK_RISK_MAX_SECTOR_WEIGHT", true, 0.30, 0, 1, func(l *Limits, v float64) { l.MaxSectorWeight = v }},
		{"SIGNALDECK_RISK_MAX_POSITIONS", false, 10, 1, 0, func(l *Limits, v float64) { l.MaxPositions = int(v) }},
		{"SIGNALDECK_RISK_KELLY_FRACTION", true, 0.25, 0, 1, func(l *Limits, v float64) { l.KellyFraction = v }},
		{"SIGNALDECK_RISK_MIN_EDGE_TRIPS", false, 20, 1, 0, func(l *Limits, v float64) { l.MinEdgeTrips = int(v) }},
		{"SIGNALDECK_RISK_MIN_TICKET_FRAC", true, 0.005, 0, 1, func(l *Limits, v float64) { l.MinTicketFrac = v }},
		{"SIGNALDECK_RISK_MAX_CORR_TO_BOOK", true, 0.80, 0, 1, func(l *Limits, v float64) { l.MaxCorrToBook = v }},
		{"SIGNALDECK_RISK_MAX_GROSS_EXPOSURE", true, 1.00, 0, 1, func(l *Limits, v float64) { l.MaxGrossExposure = v }},
	}

	for _, d := range defs {
		raw := os.Getenv(d.key)
		src := LimitSource{Key: d.key}
		val := d.def
		if raw != "" {
			src.FromEnv = true
			src.RawEnv = raw
			if d.frac {
				f, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					src.Rejected = true
					src.RejectReason = fmt.Sprintf("parse error: %v", err)
				} else {
					if f <= 0 || f > 1 {
						src.Rejected = true
						src.RejectReason = "out of (0,1]"
					} else {
						val = f
					}
				}
			} else {
				n, err := strconv.Atoi(raw)
				if err != nil {
					src.Rejected = true
					src.RejectReason = fmt.Sprintf("parse error: %v", err)
				} else {
					if n <= 0 {
						src.Rejected = true
						src.RejectReason = "must be >0"
					} else {
						val = float64(n)
					}
				}
			}
		}
		src.Value = val
		p.Sources = append(p.Sources, src)
		d.apply(&p.Limits, val)
		if src.Rejected {
			p.Rejections++
		}
	}

	return p
}

// String returns a one-line-per-limit provenance summary, with stable ordering,
// of the form `key=value (default)` or `key=value (env)` or
// `key=value (env REJECTED "raw": reason)`. The whole thing is prefixed with a
// single summary line `risk-limits: N default, M env, R rejected`.
func (p Provenance) String() string {
	defaults := 0
	envs := 0
	rejected := 0
	for _, s := range p.Sources {
		switch {
		case s.Rejected:
			rejected++
		case s.FromEnv:
			envs++
		default:
			defaults++
		}
	}

	// Stable ordering: sort sources by key.
	sorted := make([]LimitSource, len(p.Sources))
	copy(sorted, p.Sources)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	var lines []string
	lines = append(lines, fmt.Sprintf("risk-limits: %d default, %d env, %d rejected", defaults, envs, rejected))
	for _, s := range sorted {
		if s.Rejected {
			lines = append(lines, fmt.Sprintf("%s=%v (env REJECTED %q: %s)", s.Key, s.Value, s.RawEnv, s.RejectReason))
		} else if s.FromEnv {
			lines = append(lines, fmt.Sprintf("%s=%v (env)", s.Key, s.Value))
		} else {
			lines = append(lines, fmt.Sprintf("%s=%v (default)", s.Key, s.Value))
		}
	}
	return strings.Join(lines, "\n")
}

// Rejected returns the subset of limit sources that were rejected (env var was
// set but the value was invalid or out of range). This lets a caller surface
// misconfigurations without parsing the string.
func (p Provenance) Rejected() []LimitSource {
	var rejected []LimitSource
	for _, s := range p.Sources {
		if s.Rejected {
			rejected = append(rejected, s)
		}
	}
	return rejected
}