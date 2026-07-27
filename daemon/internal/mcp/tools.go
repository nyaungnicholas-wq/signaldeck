// LAYER 2 (surface half) — the closed tool surface.
//
// Six tools. There is no seventh, and there is no generic one: no query, no
// sql, no eval, no run_backtest with a caller-supplied spec, no get_bars, no
// get_history, no export, and no bulk or list-all accessor over symbols or
// dates. A connected AI cannot use this server as a compute surface, because
// the dispatch table below is the entire set of things it can cause to happen.
//
// tools_test.go asserts the surface is EXACTLY these six by name and that each
// forbidden name resolves to nothing, so adding a tool without a written
// reason fails the build rather than quietly widening the interface.
//
// Two of the six touch data at all. explain_methodology and
// critique_research_design read fixed strings; get_regime_verdict reads one
// symbol from an in-memory snapshot; get_track_record, list_validated_findings
// and get_preregistration read already-aggregated platform state. None of them
// can be parameterised into a scan.
package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

type tool struct {
	Name        string
	Title       string
	Description string
	Scope       string
	Schema      map[string]any
	Parse       func(json.RawMessage) (toolArgs, error)
	Run         func(ctx context.Context, s *Server, cl *Client, a toolArgs) (map[string]any, error)
}

// ForbiddenToolNames are names this server must never implement. Kept as data
// so the test that enforces it cannot drift from the intent that wrote it.
var ForbiddenToolNames = []string{
	"query", "sql", "eval", "exec", "run_backtest", "backtest", "get_bars", "bars",
	"get_history", "history", "export", "list_symbols", "list_all", "dump", "search",
	"get_series", "read_file", "fetch",
}

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

var toolList = []tool{
	{
		Name:  "explain_methodology",
		Title: "Explain a research-methodology topic",
		Description: "Explains one of SignalDeck's research-methodology topics: matched nulls, " +
			"non-overlapping sampling, conviction banding, walk-forward validation, survivorship, " +
			"and pre-registration. Pure exposition — no market data is consulted.",
		Scope: ScopeMethodology,
		Schema: objSchema(map[string]any{
			"topic": map[string]any{
				"type": "string", "enum": methodologyTopics,
				"description": "Which methodology topic to explain.",
			},
		}, "topic"),
		Parse: parseTopic,
		Run:   runExplainMethodology,
	},
	{
		Name:  "critique_research_design",
		Title: "Critique your own proposed study design",
		Description: "Reviews a plain-prose description of YOUR OWN proposed study for the failure " +
			"modes SignalDeck has already hit: overlapping windows, unmatched nulls, day-0 " +
			"conditioning, population-average attribution, survivorship contamination, lookahead, " +
			"multiple testing, and treating a hit rate as a return. The description is never " +
			"executed, never stored, and never echoed back.",
		Scope: ScopeMethodology,
		Schema: objSchema(map[string]any{
			"description": map[string]any{
				"type": "string", "minLength": minDescription, "maxLength": maxDescription,
				"description": "Plain prose describing your study: the question, the sample, the " +
					"window, the baseline. Not code, not a query.",
			},
		}, "description"),
		Parse: parseDescription,
		Run:   runCritique,
	},
	{
		Name:  "get_regime_verdict",
		Title: "Today's structural regime verdict for one symbol",
		Description: "Returns the CURRENT structural regime verdict for one symbol, with its " +
			"conviction band, the measured accuracy for that band, the sample size behind it, and " +
			"the caveats that must travel with it. Never a forecast history, never a series, never " +
			"a price, and never a recommendation, target, entry, exit or size.",
		Scope: ScopeVerdicts,
		Schema: objSchema(map[string]any{
			"symbol": map[string]any{
				"type": "string", "pattern": `^[A-Za-z0-9]{1,6}([./-][A-Za-z0-9]{1,6})?$`,
				"description": "A single ticker, e.g. AAPL or BTC/USD. Not a list, pattern or filter.",
			},
		}, "symbol"),
		Parse: parseSymbol,
		Run:   runRegimeVerdict,
	},
	{
		Name:  "get_track_record",
		Title: "The honest aggregate track record",
		Description: "The platform's record, including the retired directional ensemble's NEGATIVE " +
			"live result. Structural claims are labelled as backtest until their first gradable date.",
		Scope:  ScopeRecord,
		Schema: objSchema(map[string]any{}),
		Parse:  parseNone,
		Run:    runTrackRecord,
	},
	{
		Name:  "list_validated_findings",
		Title: "What survived validation and what was killed",
		Description: "Both halves of the research record. Rejections carry equal weight to survivals " +
			"and are listed with the reason each was killed.",
		Scope:  ScopeRecord,
		Schema: objSchema(map[string]any{}),
		Parse:  parseNone,
		Run:    runValidatedFindings,
	},
	{
		Name:  "get_preregistration",
		Title: "The frozen, hash-chained pre-registration",
		Description: "The predictors' claims as frozen before any forecast resolved, with the hash " +
			"chain's verification status.",
		Scope:  ScopeRecord,
		Schema: objSchema(map[string]any{}),
		Parse:  parseNone,
		Run:    runPreregistration,
	},
}

var toolByName = func() map[string]tool {
	m := make(map[string]tool, len(toolList))
	for _, t := range toolList {
		m[t.Name] = t
	}
	return m
}()

// toolDescriptors renders tools/list. Every tool is listed with the scope it
// requires and whether THIS client holds it, because a client discovering its
// own authority by trial and error produces exactly the enumeration traffic
// layer 5 is watching for.
func toolDescriptors(cl *Client) []map[string]any {
	out := make([]map[string]any, 0, len(toolList))
	for _, t := range toolList {
		granted := "NOT granted to your credential"
		if cl.HasScope(t.Scope) {
			granted = "granted to your credential"
		}
		out = append(out, map[string]any{
			"name":  t.Name,
			"title": t.Title,
			// The scope is repeated in the description because MCP clients are
			// free to drop annotation keys they do not recognise (the reference
			// Inspector does), and a client that cannot see which scope a tool
			// needs will discover it by calling — which is the enumeration
			// traffic layer 5 exists to watch for.
			"description": t.Description + " Requires the \"" + t.Scope + "\" scope (" + granted + ").",
			"inputSchema": t.Schema,
			"annotations": map[string]any{
				"readOnlyHint":    true,
				"destructiveHint": false,
				"openWorldHint":   false,
				"requiredScope":   t.Scope,
				"grantedToYou":    cl.HasScope(t.Scope),
			},
		})
	}
	return out
}

// ── implementations ─────────────────────────────────────────────────────────

func runExplainMethodology(_ context.Context, _ *Server, _ *Client, a toolArgs) (map[string]any, error) {
	e := methodologyContent[a.Topic]
	related := make([]any, 0, len(e.Related))
	for _, r := range e.Related {
		related = append(related, r)
	}
	return map[string]any{
		"topic":                  a.Topic,
		"title":                  e.Title,
		"summary":                e.Summary,
		"whyItMatters":           e.WhyMatters,
		"howSignaldeckAppliesIt": e.HowApplied,
		"failureModeItPrevents":  e.Prevents,
		"relatedTopics":          related,
		"disclaimer":             disclaimerText,
	}, nil
}

func runCritique(_ context.Context, s *Server, _ *Client, a toolArgs) (map[string]any, error) {
	lower := strings.ToLower(a.Description)
	var hits []any
	for _, f := range designFindings {
		matched := false
		for _, t := range f.Triggers {
			if strings.Contains(lower, t) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		hits = append(hits, map[string]any{
			"code": f.Code, "concern": f.Concern, "why": f.Why, "fix": f.Fix,
		})
	}
	hits, truncNote := capList(hits, s.opts.resultCap())
	out := map[string]any{
		"method": "Your description was matched against a fixed checklist of failure modes this " +
			"platform has itself hit. It was not executed, not stored, and is not reproduced anywhere " +
			"in this response.",
		"findings":      hits,
		"findingsCount": len(hits),
		"limits": "This is shallow keyword matching against a fixed checklist, not a review of your " +
			"study. A clean result means nothing on the checklist was recognised in your wording — " +
			"it is not evidence your design is sound. The checklist covers: overlapping windows, " +
			"unmatched nulls, day-0 conditioning, population-average attribution, survivorship, " +
			"lookahead, multiple testing, and accuracy-versus-return." + truncNote,
		"disclaimer": disclaimerText,
	}
	if len(hits) == 0 {
		out["noFindingsNote"] = "Nothing on the checklist was recognised. Consider stating your null " +
			"explicitly, your independence unit, and how your universe handles instruments that no " +
			"longer exist — those three are where most designs fail, including ones described in " +
			"wording this checklist does not catch."
	}
	return out, nil
}

// kindQuestions is the exact yes/no question each predictor answers. A verdict
// without its question is uninterpretable, so it ships on every row.
var kindQuestions = map[string]string{
	"trend21": "Will the symbol still be on its current side of its 200-day average in 21 trading days?",
	"trend63": "Will the symbol still be on its current side of its 200-day average in 63 trading days?",
	"trend21-crypto": "Will the symbol still be on its current side of its 200-day average in 21 days? " +
		"(crypto calendar)",
	"liquidity21": "Will mean daily dollar volume over the next 21 sessions be above or below its " +
		"trailing 200-day median?",
	"liquidity21-crypto": "Will mean daily dollar volume over the next 21 days be above or below its " +
		"trailing 200-day median? (crypto calendar)",
	"vol21": "Will realised volatility over the next 21 sessions be elevated or calm relative to its " +
		"trailing median?",
}

func runRegimeVerdict(ctx context.Context, s *Server, _ *Client, a toolArgs) (map[string]any, error) {
	rows, err := s.cache.lookup(ctx, a.Symbol)
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Kind < rows[j].Kind })

	asOf := s.now().UTC().Format("2006-01-02")
	verdicts := make([]any, 0, len(rows))
	for _, r := range rows {
		q := kindQuestions[r.Kind]
		if q == "" {
			q = "structural regime call; see the preregistration for this predictor's exact question"
		}
		v := map[string]any{
			"kind":            r.Kind,
			"question":        q,
			"regime":          r.Regime,
			"convictionBand":  band(r.Conviction),
			"bandAccuracy":    r.HistoricalAccuracy,
			"bandSampleN":     r.N,
			"horizonDays":     r.HorizonDays,
			"evidence":        "backtest",
			"evidenceCaveat":  r.EvidenceCaveat,
			"firstGradableOn": r.FirstGradableOn,
		}
		if r.Tradeability != "" {
			v["tradeability"] = r.Tradeability
		}
		if r.AsOfDay != "" {
			asOf = r.AsOfDay
		}
		verdicts = append(verdicts, v)
	}
	verdicts, truncNote := capList(verdicts, s.opts.resultCap())

	out := map[string]any{
		"symbol":        a.Symbol,
		"asOfDay":       asOf,
		"verdicts":      verdicts,
		"headlineGuard": headlineGuard,
		"scope": "This is the CURRENT verdict only. No forecast history, no series, no price and no " +
			"horizon other than the one stated is available through this or any other tool here. " +
			"bandAccuracy is the measured hit rate for this conviction band on the stated question — " +
			"it is not a probability of profit and not a return.",
		"disclaimer": disclaimerText,
	}
	if truncNote != "" {
		out["truncated"] = truncNote
	}
	if len(verdicts) == 0 {
		out["scope"] = "No current structural verdict exists for this symbol. That may mean it is not " +
			"tracked, or that its history is too short for the 200-day warm-up these predictors " +
			"require. This server does not disclose which, and does not list what is tracked."
	}
	return out, nil
}

func runTrackRecord(ctx context.Context, s *Server, _ *Client, _ toolArgs) (map[string]any, error) {
	var directional []any
	for _, m := range []struct{ key, horizon string }{
		{"directional-ensemble-1d", "1d"},
		{"directional-ensemble-1w", "1w"},
	} {
		raw, err := s.src.ModelHealth(ctx, m.key)
		if err != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		var v map[string]any
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		row := map[string]any{
			"model": m.key, "horizon": m.horizon, "source": "live graded record",
			"note": directionalNote,
		}
		copyNum(v, row, "accuracy", "liveAccuracy")
		copyNum(v, row, "observations", "independentObservations")
		copyNum(v, row, "baseline", "baselineAccuracy")
		if s, ok := v["verdict"].(string); ok {
			row["verdict"] = s
		}
		if b, ok := v["emitting"].(bool); ok {
			row["emitting"] = b
		}
		directional = append(directional, row)
	}
	if len(directional) == 0 {
		// The live record must be retrievable and must never be hidden. When
		// the health worker has written nothing, the figures of record stand
		// in — labelled as such. An empty directional block would read as
		// "no bad news", which is the one reading this platform must not offer.
		directional = append(directional, map[string]any{
			"model": "directional-ensemble", "horizon": "1d",
			"liveAccuracy":            directionalLiveAccuracy,
			"independentObservations": directionalObservations,
			"brierSkill":              directionalBrierSkill,
			"verdict":                 "retired",
			"emitting":                false,
			"source":                  "documented figure of record (the grading worker has not written a verdict on this daemon)",
			"note":                    directionalNote,
		})
	}

	return map[string]any{
		"directional": directional,
		"structural": map[string]any{
			"status": "BACKTEST ONLY. Six structural predictors carry outstanding forecasts, none of " +
				"which has resolved. Every structural accuracy this platform quotes is a backtest " +
				"measurement until the live record arrives.",
			"firstGradableOn": "2026-08-07",
			"note": "The claims were frozen and hash-chained before any of them could resolve, so the " +
				"eventual comparison is a measurement rather than a story. See get_preregistration.",
		},
		"discrimination": map[string]any{
			"claim":             "Accuracy rises with the model's own stated conviction. That spread, not the average, is the result.",
			"lowBand":           0.729,
			"veryHighBand":      0.976,
			"spreadPP":          24.7,
			"observations":      54969,
			"quarters":          24,
			"survivorshipClean": true,
			"evidence":          "non-overlapping 21-session sampling, quarter-block bootstrap intervals, universe including delisted names, independently re-implemented",
		},
		"headlineGuard": headlineGuard,
		"theHonestSummary": "One flagship model was tested live and FAILED, and was switched off " +
			"automatically. The structural claims that survived validation have not been tested live " +
			"yet. Anyone quoting this platform as predictive of price is quoting the part that was " +
			"already retired.",
		"disclaimer": disclaimerText,
	}, nil
}

func copyNum(src map[string]any, dst map[string]any, from, to string) {
	if f, ok := src[from].(float64); ok {
		dst[to] = f
	}
}

func runValidatedFindings(_ context.Context, s *Server, _ *Client, _ toolArgs) (map[string]any, error) {
	survived := make([]any, 0, len(survivedFindings))
	for _, f := range survivedFindings {
		survived = append(survived, map[string]any{
			"name": f.Name, "claim": f.Claim, "evidence": f.Evidence, "caveat": f.Caveat,
		})
	}
	killed := make([]any, 0, len(killedFindings))
	for _, f := range killedFindings {
		killed = append(killed, map[string]any{
			"name": f.Name, "whatWasClaimed": f.WhatWasClaimed,
			"whyKilled": f.WhyKilled, "killedOn": f.KilledOn,
		})
	}
	survived, n1 := capList(survived, s.opts.resultCap())
	killed, n2 := capList(killed, s.opts.resultCap())
	out := map[string]any{
		"survived": survived,
		"killed":   killed,
		"counts":   map[string]any{"survived": len(survived), "killed": len(killed)},
		"principle": "Rejections carry equal weight to survivals. A research record that lists only " +
			"what worked is a marketing document, and its survivals cannot be evaluated without " +
			"knowing how many things were tried.",
		"disclaimer": disclaimerText,
	}
	if n1 != "" || n2 != "" {
		out["truncated"] = strings.TrimSpace(n1 + " " + n2)
	}
	return out, nil
}

func runPreregistration(ctx context.Context, s *Server, _ *Client, _ toolArgs) (map[string]any, error) {
	sum, err := s.src.Preregistration(ctx)
	if err != nil {
		return nil, err
	}
	claims := make([]any, 0, len(sum.Claims))
	for _, c := range sum.Claims {
		claims = append(claims, map[string]any{
			"kind": c.Kind, "question": c.Question, "baseline": c.Baseline,
			"horizonDays":            c.HorizonDays,
			"topBandClaimedAccuracy": c.TopBandClaim,
			"registeredOn":           c.RegisteredOn, "specHash": c.SpecHash,
		})
	}
	claims, truncNote := capList(claims, s.opts.resultCap())
	out := map[string]any{
		"chainVerified":    sum.ChainVerified,
		"brokenAtSeq":      sum.BrokenAtSeq,
		"registeredBefore": sum.RegisteredBefore,
		"firstGradableOn":  sum.FirstGradableOn,
		"claims":           claims,
		"whatThisIs": "What each structural predictor claimed, frozen before any of its forecasts " +
			"could resolve. Until the first gradable date every one of these is a BACKTEST number.",
		"whyChained": "Each record links to the previous by hash, so rewriting an old claim breaks " +
			"every link after it and the break is found by recomputation rather than by trust. " +
			"chainVerified false means exactly that happened, and brokenAtSeq is where.",
		"disclaimer": disclaimerText,
	}
	if truncNote != "" {
		out["truncated"] = truncNote
	}
	return out, nil
}
