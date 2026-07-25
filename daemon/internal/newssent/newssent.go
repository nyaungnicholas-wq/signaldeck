// Package newssent scores a news headline's financial polarity with a
// deterministic lexicon — no LLM, no network, no randomness.
//
// WHY NOT THE LLM TAGGER: the platform already has one (internal/aiagents/
// sentiment), and it is strictly better at nuance. It is also useless for the
// question this package exists to answer. Measuring whether sentiment carries
// information about forward returns needs YEARS of scored headlines, and the
// LLM path is capped at ~2,000 calls/day and is not reproducible — rescoring
// the archive after a prompt change would silently change history. A lexicon is
// cheap enough to score the entire archive in one pass, gives the identical
// score for the identical headline forever, and is version-stamped so a scoring
// change is visible as a version bump rather than as drift.
//
// THE CENTRAL HONESTY RULE: a headline with no polarity words has NO sentiment.
// It does not have sentiment 0. Scoring "Apple to report earnings Thursday" as
// neutral-zero and averaging it against a real opinion dilutes the signal with
// noise and inflates the sample size, which is how a study reports a confident
// nothing. Score reports Polar=false for those, and every consumer must drop
// them rather than treat them as zeros.
//
// Method (deliberately simple, because a complicated lexicon is an
// unfalsifiable one): sum signed term weights over the headline, with negation
// flipping and hedging shrinking, then normalise by the square root of the
// matched-term count so a long list-y headline cannot outscore a decisive one
// purely by accumulating terms. Result is squashed into [-1,+1].
package newssent

import (
	"math"
	"strings"
	"unicode"
)

// Version stamps every score written to the database. BUMP IT whenever the
// lexicon or the arithmetic below changes: stored scores from different
// versions are not comparable, and the feature builder refuses to mix them.
const Version = 1

// Score is one headline's polarity read.
type Score struct {
	// Value in [-1,+1]. Meaningless unless Polar is true.
	Value float64
	// Polar reports whether the headline contained ANY polarity term. False
	// means "no opinion expressed", which is NOT the same as a neutral opinion
	// and must never be averaged in as a zero.
	Polar bool
	// Matched is how many lexicon terms fired — the evidence behind Value.
	Matched int
	// Hedged reports that the claim was qualified ("may", "reportedly"), which
	// shrinks the magnitude: a rumour is weaker evidence than a fact.
	Hedged bool
}

// Label buckets Value the way the existing news.sentiment column does, so
// lexicon rows and LLM rows can be displayed side by side. Empty string when
// the headline is not polar.
func (s Score) Label() string {
	if !s.Polar {
		return ""
	}
	switch {
	case s.Value >= 0.15:
		return "bullish"
	case s.Value <= -0.15:
		return "bearish"
	default:
		return "neutral"
	}
}

// Weights are deliberately coarse — 1.0 for a decisive move-the-stock word,
// 0.5 for a directional-but-mild one. Pretending to know that "surges" is 0.83
// and "climbs" is 0.61 would be false precision from an author with no data to
// support the difference.
const (
	strong = 1.0
	mild   = 0.5
)

// positive terms: the vocabulary financial headlines use for good news. Chosen
// in the spirit of the Loughran-McDonald finance sentiment word lists — general
// English lexicons misread finance ("liability", "crude", "short" are neutral
// domain terms, not sentiment).
var positive = map[string]float64{
	// earnings and guidance
	"beat": strong, "beats": strong, "tops": strong, "topped": strong,
	"exceeds": strong, "exceeded": strong, "outperform": strong, "outperforms": strong,
	"raises": strong, "raised": strong, "hikes": mild, "boosts": strong, "boosted": strong,
	"upgrade": strong, "upgraded": strong, "upgrades": strong,
	"record": strong, "profit": mild, "profitable": strong, "profits": mild,
	// price and demand
	"surge": strong, "surges": strong, "surged": strong, "soars": strong, "soared": strong,
	"jumps": strong, "jumped": strong, "rallies": strong, "rallied": strong,
	"climbs": mild, "climbed": mild, "gains": mild, "gained": mild, "rises": mild, "rose": mild,
	"rebound": mild, "rebounds": mild, "recovery": mild, "recovers": mild,
	"strong": mild, "strength": mild, "robust": mild, "solid": mild, "healthy": mild,
	"growth": mild, "grows": mild, "expanding": mild, "accelerating": mild,
	"demand": mild, "momentum": mild,
	// corporate events read as good
	"approval": strong, "approved": strong, "approves": strong, "wins": strong, "won": strong,
	"awarded": mild, "secures": mild, "secured": mild, "landed": mild,
	"buyback": strong, "repurchase": strong, "dividend": mild, "initiates": mild,
	"acquisition": mild, "acquires": mild, "partnership": mild, "expansion": mild,
	"breakthrough": strong, "launch": mild, "launches": mild, "unveils": mild,
	"bullish": strong, "optimistic": mild, "confidence": mild, "upside": strong,
}

// negative terms.
var negative = map[string]float64{
	// earnings and guidance
	"miss": strong, "misses": strong, "missed": strong, "shortfall": strong,
	"cuts": strong, "cut": strong, "slashes": strong, "slashed": strong, "lowers": strong,
	"lowered": strong, "downgrade": strong, "downgraded": strong, "downgrades": strong,
	"warns": strong, "warning": strong, "underperform": strong, "disappointing": strong,
	"loss": strong, "losses": strong, "unprofitable": strong, "writedown": strong,
	"impairment": strong, "restatement": strong,
	// price and demand
	"plunge": strong, "plunges": strong, "plunged": strong, "plummets": strong,
	"tumbles": strong, "tumbled": strong, "sinks": strong, "sank": strong,
	"slumps": strong, "slumped": strong, "slides": mild, "slid": mild,
	"falls": mild, "fell": mild, "drops": mild, "dropped": mild, "declines": mild,
	"declined": mild, "weak": mild, "weakness": mild, "softening": mild, "sluggish": mild,
	"slowdown": mild, "contraction": mild, "shrinking": mild,
	// corporate distress
	"bankruptcy": strong, "bankrupt": strong, "default": strong, "defaults": strong,
	"insolvency": strong, "delisting": strong, "delisted": strong,
	"layoffs": strong, "layoff": strong, "fires": mild, "restructuring": mild,
	"recall": strong, "recalls": strong, "halt": strong, "halted": strong, "halts": strong,
	"suspends": strong, "suspended": strong, "withdraws": strong, "withdrawn": strong,
	"lawsuit": strong, "sues": strong, "sued": mild, "probe": strong, "investigation": strong,
	"investigating": strong, "subpoena": strong, "fraud": strong, "charges": mild,
	"fined": strong, "fine": mild, "penalty": mild, "settlement": mild, "violation": strong,
	// "steps down" is a phrase, not a word: see phrases below.
	"resigns": mild, "resigned": mild,
	"dilution": strong, "offering": mild, "downtime": mild, "outage": strong,
	"breach": strong, "hack": strong, "shortage": mild, "delay": mild, "delays": mild,
	"delayed": mild, "rejected": strong, "rejects": strong, "denied": mild,
	"bearish": strong, "pessimistic": mild, "downside": strong, "risk": mild, "risks": mild,
	"concerns": mild, "fears": mild, "uncertainty": mild, "volatile": mild,
}

// phrases are multi-word patterns whose meaning is not the sum of their words.
// Matched before tokenisation, on the lowercased headline.
var phrases = map[string]float64{
	"steps down":        -mild,
	"stepping down":     -mild,
	"going concern":     -strong,
	"short seller":      -strong,
	"short report":      -strong,
	"class action":      -strong,
	"profit warning":    -strong,
	"guidance cut":      -strong,
	"price target cut":  -strong,
	"chapter 11":        -strong,
	"reverse split":     -strong,
	"all-time high":     strong,
	"record high":       strong,
	"price target rais": strong, // stem: raise/raised/raises
	"better than expec": strong,
	"worse than expec":  -strong,
	"ahead of expectat": strong,
	"below expectat":    -strong,
	"beats estimates":   strong,
	"misses estimates":  -strong,
}

// negators flip the polarity of the next few terms. "Not profitable" and
// "fails to beat" are the failure mode a bag-of-words scorer gets backwards.
var negators = map[string]bool{
	"not": true, "no": true, "never": true, "without": true, "fails": true,
	"fail": true, "failed": true, "failing": true, "denies": true, "denied": true,
	"less": true, "lacks": true, "lack": true, "unable": true, "cannot": true,
	"isn't": true, "wasn't": true, "won't": true, "doesn't": true, "didn't": true,
}

// negationWindow is how many following tokens a negator flips. Three covers
// "fails to beat estimates" without reaching across a whole clause.
const negationWindow = 3

// hedges mark a claim as unconfirmed. They shrink magnitude rather than zeroing
// it: "may cut guidance" is real information, just weaker than "cuts guidance".
var hedges = map[string]bool{
	"may": true, "might": true, "could": true, "reportedly": true, "rumor": true,
	"rumour": true, "rumored": true, "speculation": true, "considering": true,
	"weighs": true, "weighing": true, "explores": true, "exploring": true,
	"potential": true, "possible": true, "expected": true, "forecast": true,
	"if": true, "unconfirmed": true, "alleged": true, "allegedly": true,
}

// hedgeFactor scales a hedged headline's magnitude.
const hedgeFactor = 0.6

// intensifiers scale the term that follows.
var intensifiers = map[string]float64{
	"sharply": 1.3, "steeply": 1.3, "massively": 1.4, "significantly": 1.2,
	"surprisingly": 1.2, "unexpectedly": 1.2, "record": 1.2, "huge": 1.3,
	"slightly": 0.6, "marginally": 0.6, "modestly": 0.7, "somewhat": 0.7,
}

// Rate scores one headline. It is pure: same input, same output, forever.
func Rate(headline string) Score {
	h := strings.ToLower(headline)

	var sum float64
	matched := 0

	// Phrases first, and their spans are BLANKED so their component words are
	// not also counted individually ("guidance cut" must not additionally score
	// "cut").
	for p, wgt := range phrases {
		for {
			i := strings.Index(h, p)
			if i < 0 {
				break
			}
			sum += wgt
			matched++
			h = h[:i] + strings.Repeat(" ", len(p)) + h[i+len(p):]
		}
	}

	toks := tokenize(h)
	hedged := false
	for _, t := range toks {
		if hedges[t] {
			hedged = true
		}
	}

	negLeft := 0
	scale := 1.0
	for _, t := range toks {
		if f, ok := intensifiers[t]; ok {
			scale = f
			// "record" is both an intensifier and a positive term; fall through
			// so it still scores as polarity.
			if _, alsoTerm := positive[t]; !alsoTerm {
				continue
			}
		}
		if negators[t] {
			negLeft = negationWindow
			continue
		}

		w, ok := positive[t]
		if !ok {
			if nw, isNeg := negative[t]; isNeg {
				w = -nw
				ok = true
			}
		}
		if !ok || w == 0 {
			if negLeft > 0 {
				negLeft--
			}
			continue
		}

		v := w * scale
		scale = 1.0
		if negLeft > 0 {
			// A negated positive is negative, but weakly: "not profitable" is
			// bad news, "fails to beat" is mildly bad, neither is as decisive as
			// an outright "bankruptcy".
			v = -v * 0.8
			negLeft--
		}
		sum += v
		matched++
	}

	if matched == 0 {
		return Score{Polar: false}
	}

	// Normalise by sqrt(matched): three mildly positive words should not beat
	// one decisively positive one on count alone, but genuine agreement across
	// terms should still register.
	v := sum / math.Sqrt(float64(matched))
	if hedged {
		v *= hedgeFactor
	}
	// Squash into range. Division by 2 puts a single strong term (~1.0) at
	// roughly ±0.46 and saturates only on genuinely emphatic headlines.
	v = math.Tanh(v / 2)

	return Score{Value: v, Polar: true, Matched: matched, Hedged: hedged}
}

// tokenize splits on any non-letter/digit, keeping apostrophes so contracted
// negators ("isn't") survive as single tokens.
func tokenize(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\''
	})
}
