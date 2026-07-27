// LAYER 3 — the data boundary. This is the important one.
//
// Every response passes through Sanitize before it can be serialized, and
// Sanitize is an ALLOWLIST: a field whose path is not explicitly permitted is
// dropped, not passed. The difference matters because the failure mode being
// defended against is a future edit — someone adds a field to a tool, forgets
// it carries a symbol's raw close, and ships it. A blocklist has to have
// anticipated that field. An allowlist has not.
//
// Three properties are enforced here rather than remembered:
//
//  1. Raw records are structurally unrepresentable. Only plain maps, strings,
//     numbers and bools survive; a Go struct is dropped wholesale, so a store
//     row cannot be handed to the serializer by accident. A map that looks
//     like a bar (three or more of open/high/low/close/volume/bid/ask) is
//     dropped even if some future editor allowlists its path.
//  2. Series are unrepresentable. A list of more than a handful of numbers is
//     a time series however it is named, and it is dropped. Combined with (1),
//     "give me the history" has no shape it can arrive in.
//  3. Licensed and Restricted sources cannot surface. Anything naming a
//     non-redistributable provider — read from internal/datalicense, not from
//     a hardcoded list here, so the classification has exactly one home — is
//     dropped.
//
// allowlist_test.go fails the build if a tool emits a field with no allowlist
// entry, which is what makes this a layer rather than a convention.
package mcp

import (
	"sort"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/datalicense"
)

// Structural caps. Deliberately mean: nothing this server legitimately returns
// comes close to them.
const (
	maxStringLen = 6000 // one exposition paragraph set, generously
	maxListLen   = 64   // hard ceiling regardless of the per-response cap
	maxNumbers   = 8    // more numbers than this in one list is a series
	maxDepth     = 6
)

// responseFields is the allowlist, one entry per tool, as dotted paths. A list
// of objects uses the same path for its elements (no index), so
// "verdicts.regime" describes the regime field of every element of verdicts.
//
// Note what is ABSENT and cannot be added without a written reason: any price,
// any timestamp finer than a day, any raw conviction (the band is the unit),
// any count of the universe, any symbol list, any series.
var responseFields = map[string]map[string]bool{
	"explain_methodology": pathSet(
		"topic", "title", "summary", "whyItMatters", "howSignaldeckAppliesIt",
		"failureModeItPrevents", "relatedTopics", "disclaimer",
	),
	"critique_research_design": pathSet(
		"method", "findingsCount", "findings", "findings.code", "findings.concern",
		"findings.why", "findings.fix", "noFindingsNote", "limits", "disclaimer",
	),
	"get_regime_verdict": pathSet(
		"symbol", "asOfDay", "verdicts", "verdicts.kind", "verdicts.question",
		"verdicts.regime", "verdicts.convictionBand", "verdicts.bandAccuracy",
		"verdicts.bandSampleN", "verdicts.horizonDays", "verdicts.evidence",
		"verdicts.evidenceCaveat", "verdicts.firstGradableOn", "verdicts.tradeability",
		"headlineGuard", "scope", "truncated", "disclaimer",
	),
	"get_track_record": pathSet(
		"directional", "directional.model", "directional.horizon", "directional.liveAccuracy",
		"directional.independentObservations", "directional.baselineAccuracy",
		"directional.brierSkill", "directional.verdict", "directional.emitting",
		"directional.source", "directional.note",
		"structural", "structural.status", "structural.firstGradableOn", "structural.note",
		"discrimination", "discrimination.claim", "discrimination.lowBand",
		"discrimination.veryHighBand", "discrimination.spreadPP", "discrimination.observations",
		"discrimination.quarters", "discrimination.survivorshipClean", "discrimination.evidence",
		"headlineGuard", "theHonestSummary", "disclaimer",
	),
	"list_validated_findings": pathSet(
		"survived", "survived.name", "survived.claim", "survived.evidence", "survived.caveat",
		"killed", "killed.name", "killed.whatWasClaimed", "killed.whyKilled", "killed.killedOn",
		"principle", "counts", "counts.survived", "counts.killed", "truncated", "disclaimer",
	),
	"get_preregistration": pathSet(
		"chainVerified", "brokenAtSeq", "registeredBefore", "firstGradableOn",
		"claims", "claims.kind", "claims.question", "claims.baseline",
		"claims.horizonDays", "claims.topBandClaimedAccuracy",
		"claims.registeredOn", "claims.specHash",
		"whatThisIs", "whyChained", "truncated", "disclaimer",
	),
}

func pathSet(paths ...string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		m[p] = true
	}
	return m
}

// barFieldNames are the field names that together describe a raw market
// record. Any map carrying three or more of them is a bar, whatever it calls
// itself, and never leaves this process.
var barFieldNames = map[string]bool{
	"open": true, "high": true, "low": true, "close": true, "volume": true,
	"o": true, "h": true, "l": true, "c": true, "v": true,
	"bid": true, "ask": true, "price": true, "vwap": true, "last": true,
	"trades": true, "openinterest": true,
}

// protectedProviders is derived from the license table so this file cannot
// drift from internal/datalicense. Every provider whose data may not be
// redistributed contributes its provider name and its source key.
// Only tokens of six characters or more are used, which excludes the source
// key "news" and provider words like "Markets". That exclusion is deliberate
// and is the honest trade-off to state: a check that fires on the word
// "market" would silently gut every legitimate response, and a check nobody
// can leave switched on is worth less than a narrower one that stays on. What
// remains — alpaca, kraken, coinbase, hyperliquid, tradingview, stocktwits,
// cryptohist, cryptolive — is the set of names that identify a vendor.
// genericProviderWord holds the parts of a provider's name that are ordinary
// English. "Alpaca Markets" contributes "alpaca", which identifies a vendor,
// and "markets", which identifies nothing.
var genericProviderWord = map[string]bool{
	"markets": true, "market": true, "exchange": true, "capital": true,
	"financial": true, "securities": true, "holdings": true,
}

func protectedProviders() []string {
	var out []string
	for key, src := range datalicense.Sources {
		if src.Redistrib {
			continue
		}
		if len(key) >= 6 {
			out = append(out, strings.ToLower(key))
		}
		for _, w := range strings.FieldsFunc(strings.ToLower(src.Provider), func(r rune) bool {
			return r == '/' || r == ' '
		}) {
			if len(w) >= 6 && !genericProviderWord[w] {
				out = append(out, w)
			}
		}
	}
	sort.Strings(out)
	return out
}

var protectedTerms = protectedProviders()

// mentionsProtectedSource reports whether a string names a Licensed or
// Restricted provider. It is intentionally blunt: this server's entire text
// corpus is fixed exposition that has no reason to name a data vendor, so a
// false positive is a signal to reword, not to loosen the check.
func mentionsProtectedSource(s string) bool {
	l := strings.ToLower(s)
	for _, t := range protectedTerms {
		if strings.Contains(l, t) {
			return true
		}
	}
	return false
}

// Sanitize is the single serialization chokepoint. It returns the cleaned
// payload and the list of dropped paths (with a reason suffix), which the
// caller audits.
//
// An unknown tool yields an EMPTY response, not the input: a tool that is not
// in the allowlist has no permitted fields at all.
func Sanitize(tool string, in map[string]any) (map[string]any, []string) {
	allow, ok := responseFields[tool]
	var dropped []string
	if !ok {
		return map[string]any{}, []string{tool + ": no allowlist entry for this tool — entire response withheld"}
	}
	out := walkMap("", in, allow, 0, &dropped)
	sort.Strings(dropped)
	return out, dropped
}

func walkMap(prefix string, m map[string]any, allow map[string]bool, depth int, dropped *[]string) map[string]any {
	out := make(map[string]any, len(m))
	if depth > maxDepth {
		*dropped = append(*dropped, prefix+": nesting deeper than the permitted depth")
		return out
	}
	if barCount(m) >= 3 {
		*dropped = append(*dropped, prefix+": object has the shape of a raw market record")
		return out
	}
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if !allow[path] {
			*dropped = append(*dropped, path+": no allowlist entry")
			continue
		}
		cleaned, ok := walkValue(path, v, allow, depth+1, dropped)
		if !ok {
			continue
		}
		out[k] = cleaned
	}
	return out
}

func walkValue(path string, v any, allow map[string]bool, depth int, dropped *[]string) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, true
	case string:
		if len(t) > maxStringLen {
			*dropped = append(*dropped, path+": string exceeds the length cap")
			return nil, false
		}
		if mentionsProtectedSource(t) {
			*dropped = append(*dropped, path+": names a Licensed or Restricted data source")
			return nil, false
		}
		return t, true
	case bool:
		return t, true
	case int:
		return t, true
	case int64:
		return t, true
	case float64:
		return t, true
	case map[string]any:
		return walkMap(path, t, allow, depth, dropped), true
	case []any:
		if len(t) > maxListLen {
			*dropped = append(*dropped, path+": list exceeds the structural cap")
			return nil, false
		}
		nums := 0
		for _, e := range t {
			switch e.(type) {
			case int, int64, float64:
				nums++
			}
		}
		if nums > maxNumbers {
			*dropped = append(*dropped, path+": numeric list of this length is a series")
			return nil, false
		}
		out := make([]any, 0, len(t))
		for _, e := range t {
			// Elements share the parent's path: "verdicts.kind" describes
			// every element's kind, and there is no per-index authority.
			cleaned, ok := walkValue(path, e, allow, depth, dropped)
			if !ok {
				continue
			}
			out = append(out, cleaned)
		}
		return out, true
	case []string:
		if len(t) > maxListLen {
			*dropped = append(*dropped, path+": list exceeds the structural cap")
			return nil, false
		}
		out := make([]any, 0, len(t))
		for _, e := range t {
			if mentionsProtectedSource(e) || len(e) > maxStringLen {
				*dropped = append(*dropped, path+": element withheld")
				continue
			}
			out = append(out, e)
		}
		return out, true
	default:
		// Structs, pointers, maps with non-string keys, channels, functions:
		// all dropped. This is the property that makes a raw store row
		// unrepresentable rather than merely unlikely.
		*dropped = append(*dropped, path+": value is not a permitted plain type")
		return nil, false
	}
}

func barCount(m map[string]any) int {
	n := 0
	for k := range m {
		if barFieldNames[strings.ToLower(k)] {
			n++
		}
	}
	return n
}
