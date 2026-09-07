// GET /api/accuracy — the public accuracy registry, with an explicit
// publication status on every row.
//
// WHY THIS ROUTE EXISTS. Three audits referenced /api/accuracy and it had never
// been implemented; the path returned 404 while prose described its behaviour.
// Worse, the surfaces that DID exist could disagree: on 2026-08-03 the registry
// carried retire=false on every directional row (its window had shrunk below
// the 10-block floor, so no interval published and the auto-retire rule could
// not fire) while evidence_claims still carried those same models as refuted.
//
// This handler is the single place those two facts are reconciled, and it
// reconciles them through publication.BuildVerdict rather than by reimplementing
// the rules — there must not be a second copy of the verdict logic.
//
// It is FAIL-CLOSED in both directions:
//   - registry unreadable        -> 503 REFUSED, no rows
//   - grader heartbeat stale     -> 503 REFUSED_STALE, no rows
//
// A refusal carries a reason. A status a reader cannot act on is barely better
// than silence, and silence is what let a 33-hour outage go unnoticed.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/forecastmon"
	"github.com/nyaungnicholas-wq/signaldeck/internal/prereg"
	"github.com/nyaungnicholas-wq/signaldeck/internal/publication"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// GraderMaxAge is how old the last successful grade may be before this surface
// refuses to publish. The grader runs daily; two hours past a run is generous,
// and anything beyond it means the schedule is broken rather than merely late.
const GraderMaxAge = 26 * time.Hour

// GraderTask is the heartbeat key the accuracy grader writes under.
const GraderTask = "accuracy_registry"

func (d Deps) registerAccuracy(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/accuracy", d.accuracy)
}

// registryFile is the on-disk shape this handler reads. Only the fields the
// verdict needs are modelled.
//
// THIS HANDLER DOES NOT PASS THE REST THROUGH, and the comment here used to say
// it did. The response is the closed accuracyRow struct below, built field by
// field, so every other registry key — honesty, breadth, design_effect, null_ci,
// claimed, the survivorship_* and settlement_* blocks, and the grader's own
// verdict string and CI — is dropped from /api/accuracy. That matters most for
// honesty and breadth, which the grader publishes specifically to QUALIFY a
// verdict: an API client gets "FAILED" without the two fields that say how far
// that verdict reaches.
//
// Nothing renders those fields today — web/src/app/accuracy/page.tsx reads
// data/accuracy_registry.json from disk and uses this endpoint only as the
// publication gate — so no displayed number is wrong. Read the registry file
// directly if you need a field that is not modelled here, and widen accuracyRow
// (not this comment) if a client ever needs one served.
type registryFile struct {
	GradedAt          string        `json:"graded_at"`
	RefusedSince      *string       `json:"refused_since"`
	GraderSHA256      string        `json:"grader_sha256"`
	MinIndependentN   float64       `json:"min_independent_n"`
	MinDistinctBlocks int           `json:"min_distinct_blocks"`
	Rows              []registryRow `json:"rows"`
}

type registryRow struct {
	Predictor    string      `json:"predictor"`
	Family       string      `json:"family"`
	Band         string      `json:"band"`
	LiveN        int         `json:"live_n"`
	LiveAcc      *float64    `json:"live_acc"`
	CI           *[2]float64 `json:"ci"`
	CIMethod     string      `json:"ci_method"`
	DistinctDays *int        `json:"distinct_days"`
	EffectiveN   *float64    `json:"effective_n"`
	NullAcc      *float64    `json:"null_acc"`
	Skill        *float64    `json:"skill"`
	Retire       bool        `json:"retire"`
	Verdict      *string     `json:"verdict"`
	Note         string      `json:"note"`
}

type accuracyResponse struct {
	Status       string        `json:"status"`
	GraderFresh  bool          `json:"grader_fresh"`
	Reason       string        `json:"reason,omitempty"`
	GeneratedAt  time.Time     `json:"generated_at"`
	GradedAt     string        `json:"graded_at,omitempty"`
	GraderSHA256 string        `json:"grader_sha256,omitempty"`
	Rows         []accuracyRow `json:"rows,omitempty"`
}

type accuracyRow struct {
	Predictor         string   `json:"predictor"`
	Horizon           string   `json:"horizon"`
	Variant           string   `json:"variant"`
	Family            string   `json:"family"`
	PublicationStatus string   `json:"publication_status"`
	Retired           bool     `json:"retired"`
	RetirementSticky  bool     `json:"retirement_sticky"`
	RetirementSource  string   `json:"retirement_source,omitempty"`
	Reasons           []string `json:"reasons"`
	EvidenceRefs      []string `json:"evidence_refs"`
	LiveN             int      `json:"live_n"`
	LiveAcc           *float64 `json:"live_acc"`
	NullAcc           *float64 `json:"null_acc"`
	Skill             *float64 `json:"skill"`
	EffectiveN        *float64 `json:"effective_n"`
	DistinctDays      *int     `json:"distinct_days"`
	CIMethod          string   `json:"ci_method,omitempty"`
	Note              string   `json:"note,omitempty"`
}

func (d Deps) accuracy(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := d.now().UTC()

	reg, err := loadRegistry(d.RegistryPath)
	if err != nil {
		writeAccuracyRefusal(w, accuracyResponse{
			Status: "REFUSED", GraderFresh: false, GeneratedAt: now,
			Reason: "accuracy registry unavailable: " + err.Error(),
		})
		return
	}

	// The grader's own refusal marker outranks anything in the rows.
	if reg.RefusedSince != nil && *reg.RefusedSince != "" {
		writeAccuracyRefusal(w, accuracyResponse{
			Status: "REFUSED", GraderFresh: false, GeneratedAt: now,
			GradedAt: reg.GradedAt,
			Reason:   "grader has been refusing since " + *reg.RefusedSince,
		})
		return
	}

	stale, why, err := d.St.GraderStale(ctx, GraderTask, GraderMaxAge, now)
	if err != nil {
		writeAccuracyRefusal(w, accuracyResponse{
			Status: "REFUSED", GraderFresh: false, GeneratedAt: now,
			Reason: "grader heartbeat unreadable: " + err.Error(),
		})
		return
	}
	if stale {
		writeAccuracyRefusal(w, accuracyResponse{
			Status: "REFUSED_STALE", GraderFresh: false, GeneratedAt: now,
			GradedAt: reg.GradedAt, Reason: why,
		})
		return
	}

	// COLLAPSE GATE. Refuse to publish figures computed over a window whose
	// cross-section had collapsed.
	//
	// On a collapsed day the model hands the whole universe a handful of
	// distinct probabilities, so the record is one market-wide call repeated per
	// symbol while n reads as hundreds of independent trials. Measured
	// 2026-07-27..08-04: 5-12 distinct values across ~329 symbols, with a
	// day-clustered design effect of 27.9 — 2,626 rows carrying the information
	// of 94. Every figure covering those days grades a dead configuration, and
	// the registry published FAILED/retire=true on what were really 11 market
	// calls.
	//
	// The registry already reports distinct_days per row, so the window is
	// known rather than assumed. This refuses on the SAME evidence
	// internal/forecastmon uses, so the publication surface and the monitor
	// cannot disagree about whether a day was usable.
	if reason, collapsed, err := d.collapsedGradingWindow(ctx, reg, now); err == nil && collapsed {
		writeAccuracyRefusal(w, accuracyResponse{
			Status: "REFUSED", GraderFresh: false, GeneratedAt: now,
			GradedAt: reg.GradedAt, Reason: reason,
		})
		return
	}

	// Refuse rather than publish, for the same reason RetirementHistory does
	// twelve lines below: an unreadable evidence ledger must not read as "no
	// evidence against this model". `claims` is the ONLY input that can set
	// retired=true from SourceEvidence (publication/verdict.go:155-164), so
	// swallowing this error meant a transient DB failure could silently
	// UN-RETIRE a model a historical claim had already refuted — and publish it
	// as live, with HTTP 200 and nothing in the payload saying the read failed.
	claims, err := d.St.EvidenceClaims(ctx, "", "")
	if err != nil {
		writeAccuracyRefusal(w, accuracyResponse{
			Status: "REFUSED", GraderFresh: false, GeneratedAt: now,
			GradedAt: reg.GradedAt,
			Reason:   "evidence claims unreadable: " + err.Error(),
		})
		return
	}

	rows := make([]accuracyRow, 0, len(reg.Rows))
	for _, rr := range reg.Rows {
		predictor, horizon, variant := splitPredictor(rr.Predictor)

		priorRetired, priorReason, priorSource, err := d.St.RetirementHistory(ctx, predictor, horizon, variant)
		if err != nil {
			// An unreadable history must not read as "never retired".
			writeAccuracyRefusal(w, accuracyResponse{
				Status: "REFUSED", GraderFresh: false, GeneratedAt: now,
				Reason: "retirement history unreadable: " + err.Error(),
			})
			return
		}

		v := publication.BuildVerdict(
			graderResultOf(rr, reg),
			matchingClaims(claims, predictor, horizon),
			publication.PriorVerdict{
				Retired:       priorRetired,
				RetireReason:  priorReason,
				RetirementFor: priorSource,
			},
			now,
		)

		// ARM THE STICKINESS. publication_verdicts had ZERO rows and
		// PutPublicationVerdict had no production caller, so RetirementHistory
		// always answered "never retired", PriorVerdict.Retired was always
		// false, and BuildVerdict's SourceHistory branch — the one whose own
		// comment says "retirement is sticky: this row was retired by an earlier
		// grade and cannot be un-retired by a later one" — could never fire.
		// The layer this route exists for (see the header: the 2026-08-03 defect
		// where the registry carried retire=false on every directional row once
		// its window shrank below the block floor) was inert.
		//
		// Write only on the FALSE->TRUE transition: once per retirement, not
		// once per GET, and never a not-retired row on top of a retired one
		// (the table's trigger refuses that as ErrUnretireRefused, correctly).
		// Failure to persist must not fail the response — the verdict being
		// served is still right — but it must not be silent either, because a
		// verdict that did not stick is a verdict that will not be sticky next
		// time.
		if v.Retired && !priorRetired {
			if err := d.St.PutPublicationVerdict(ctx, store.PublicationVerdictRow{
				Predictor: predictor, Horizon: horizon, Variant: variant,
				PublicationStatus: v.PublicationStatus,
				Retired:           true,
				RetirementSticky:  v.RetirementSticky,
				RetireReason:      strings.Join(v.Reasons, "; "),
				RetirementSource:  v.RetirementSource,
				CurrentNEff:       rr.EffectiveN,
				CurrentBlocks:     rr.DistinctDays,
				CIMethod:          rr.CIMethod,
				NullRate:          rr.NullAcc,
				SkillPP:           rr.Skill,
				Reasons:           v.Reasons,
				EvidenceRefs:      v.EvidenceRefs,
			}); err != nil {
				slog.Error("accuracy: could not persist retirement verdict — "+
					"retirement will NOT be sticky for this row",
					"predictor", predictor, "horizon", horizon, "variant", variant, "err", err)
			}
		}

		rows = append(rows, accuracyRow{
			Predictor: predictor, Horizon: horizon, Variant: variant,
			Family:            rr.Family,
			PublicationStatus: v.PublicationStatus,
			Retired:           v.Retired,
			RetirementSticky:  v.RetirementSticky,
			RetirementSource:  v.RetirementSource,
			Reasons:           v.Reasons,
			EvidenceRefs:      v.EvidenceRefs,
			LiveN:             rr.LiveN,
			LiveAcc:           rr.LiveAcc,
			NullAcc:           rr.NullAcc,
			Skill:             rr.Skill,
			EffectiveN:        rr.EffectiveN,
			DistinctDays:      rr.DistinctDays,
			CIMethod:          rr.CIMethod,
			Note:              rr.Note,
		})
	}

	writeJSONStatus(w, http.StatusOK, accuracyResponse{
		Status: "OK", GraderFresh: true, GeneratedAt: now,
		GradedAt: reg.GradedAt, GraderSHA256: reg.GraderSHA256, Rows: rows,
	})
}

// graderResultOf translates one registry row into the verdict builder's input.
//
// HasBaseline is false when the row has no comparable null: such a row is the
// quarantined NO BASELINE case and must not be scored against an assumed 0.5.
// HasInterval requires an actual interval — "withheld" is not one.
func graderResultOf(rr registryRow, reg *registryFile) publication.GraderResult {
	g := publication.GraderResult{
		Predictor:    firstWord(rr.Predictor),
		Retired:      rr.Retire,
		CIType:       rr.CIMethod,
		GraderSHA256: reg.GraderSHA256,
		Observations: rr.LiveN,
		HasBaseline:  rr.NullAcc != nil,
		HasInterval:  rr.CI != nil && !strings.EqualFold(rr.CIMethod, "withheld"),
	}
	if rr.EffectiveN != nil {
		g.NEff = *rr.EffectiveN
	} else {
		// No design-effect correction published means the effective sample is
		// unmeasured. Raw live_n would overstate it by the design effect —
		// 14.73x on this system's live record — so it is deliberately NOT
		// substituted here. Unmeasured resolves to insufficient.
		g.NEff = 0
	}
	if rr.DistinctDays != nil {
		g.DistinctBlocks = *rr.DistinctDays
	}
	if rr.CI != nil {
		g.WilsonUpper = rr.CI[1]
	}
	if rr.NullAcc != nil {
		g.NullRate = *rr.NullAcc
	}
	return g
}

// matchingClaims selects the evidence claims scoped to this predictor+horizon.
// Claim ids are of the form "directional-ensemble-1d"; the scope JSON carries
// the horizons authoritatively, so both are checked and either may match.
func matchingClaims(claims []store.EvidenceClaimRow, predictor, horizon string) []publication.EvidenceClaim {
	out := []publication.EvidenceClaim{}
	for _, c := range claims {
		if !strings.HasPrefix(c.ID, predictor) {
			continue
		}
		if horizon != "" && !strings.Contains(c.ID, horizon) && !strings.Contains(c.ScopeJSON, `"`+horizon+`"`) {
			continue
		}
		out = append(out, publication.EvidenceClaim{ID: c.ID, Status: c.Status})
	}
	return out
}

// splitPredictor parses the registry's display label into its parts.
// "directional-ensemble (1d, high conviction)" -> ("directional-ensemble", "1d", "high conviction").
func splitPredictor(label string) (predictor, horizon, variant string) {
	predictor = firstWord(label)
	open := strings.Index(label, "(")
	if open < 0 {
		return predictor, "", ""
	}
	inner := strings.TrimSuffix(strings.TrimSpace(label[open+1:]), ")")
	parts := strings.SplitN(inner, ",", 2)
	horizon = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		variant = strings.TrimSpace(parts[1])
	}
	return predictor, horizon, variant
}

func firstWord(label string) string {
	if i := strings.Index(label, " ("); i > 0 {
		return strings.TrimSpace(label[:i])
	}
	return strings.TrimSpace(label)
}

// loadRegistry resolves the registry the same way ModelHealthWorker does:
// launchd/Task Scheduler run the daemon from <repo>/daemon, tools run from the
// repo root, so both are candidates. An explicit override wins, which is what
// keeps tests off the live artifact.
func loadRegistry(override string) (*registryFile, error) {
	candidates := []string{
		filepath.Join("..", prereg.RegistryRel),
		prereg.RegistryRel,
	}
	if override != "" {
		candidates = []string{override}
	}
	var lastErr error
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		var reg registryFile
		if err := json.Unmarshal(b, &reg); err != nil {
			return nil, err
		}
		return &reg, nil
	}
	return nil, lastErr
}

func writeAccuracyRefusal(w http.ResponseWriter, body accuracyResponse) {
	writeJSONStatus(w, http.StatusServiceUnavailable, body)
}

func writeJSONStatus(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// collapsedGradingWindow reports whether the days the registry graded include a
// collapsed cross-section.
//
// The window is taken from the registry's own max distinct_days rather than a
// fixed lookback: a fixed one would either miss a collapse just outside it or
// refuse forever because of a collapse the grader never touched. An unreadable
// window is NOT treated as a collapse — the caller ignores the error and
// publishes, because refusing on a failed read would wedge the surface shut on
// a transient database error rather than on evidence.
func (d Deps) collapsedGradingWindow(ctx context.Context, reg *registryFile, now time.Time) (string, bool, error) {
	// Per-horizon windows. This used to take ONE global max distinct_days and
	// probe horizon "1d" only, so the 1w rows were gated by 1d evidence: a
	// collapse confined to the 1w cross-section could not refuse anything, and
	// a 1d collapse refused rows it had not measured. Each horizon present in
	// the registry is now checked against its OWN day stats and its own depth.
	depth := map[string]int{}
	for _, r := range reg.Rows {
		if r.DistinctDays == nil || *r.DistinctDays <= 0 {
			continue
		}
		_, horizon, _ := splitPredictor(r.Predictor)
		if horizon == "" {
			continue
		}
		if *r.DistinctDays > depth[horizon] {
			depth[horizon] = *r.DistinctDays
		}
	}
	if len(depth) == 0 {
		return "", false, nil
	}
	// Sorted so the refusal text is stable across polls.
	horizons := make([]string, 0, len(depth))
	for h := range depth {
		horizons = append(horizons, h)
	}
	sort.Strings(horizons)

	var bad []string
	total := 0
	for _, h := range horizons {
		days := depth[h]
		// Trading days are sparser than calendar days; widen so the calendar
		// window actually contains `days` sessions rather than stopping short.
		//
		// KNOWN APPROXIMATION, stated rather than papered over: the registry
		// publishes how MANY days it graded, not WHICH ones, so this reproduces
		// the window as "the newest `days` sessions". A collapsed day that sits
		// inside the graded window but outside that newest-N slice is missed.
		// The miss FAILS OPEN (publishes when it should refuse), which is the
		// wrong direction — closing it needs the grader to emit its graded day
		// list, not a smarter guess here.
		since := now.AddDate(0, 0, -(days*2 + 7))
		stats, err := d.St.ForecastDayStats(ctx, h, since)
		if err != nil {
			return "", false, err
		}
		// FAIL CLOSED (2026-09-07): the grader's set is the newest `days` SETTLE
		// days, and quarantined sessions can push it back further than this
		// call-day slice reaches. Check a SUPERSET (extra sessions of slack): a
		// collapsed day just outside the true window then over-refuses for a few
		// sessions, which is the right direction; it can no longer be missed.
		const gateSlackSessions = 10
		if keep := days + gateSlackSessions; len(stats) > keep {
			stats = stats[len(stats)-keep:]
		}
		total += len(stats)
		for _, st := range stats {
			fd := forecastmon.DayStat{Day: st.Day, Symbols: st.Symbols, DistinctProbs: st.DistinctProbs}
			if fd.Collapsed() {
				bad = append(bad, fmt.Sprintf("%s %s (%d distinct across %d symbols)",
					h, st.Day, st.DistinctProbs, st.Symbols))
			}
		}
	}
	if len(bad) == 0 {
		return "", false, nil
	}
	return buildCollapseReason(bad, total), true, nil
}

// buildCollapseReason is split out so the wording is assertable without a
// database: a refusal nobody can act on is barely better than silence.
func buildCollapseReason(bad []string, total int) string {
	return fmt.Sprintf(
		"the graded window contains %d collapsed cross-section(s) of %d day(s): %s. "+
			"On a collapsed day the whole universe receives a handful of distinct "+
			"probabilities, so these rows grade one market-wide call repeated per symbol, "+
			"not independent per-symbol forecasts. Figures over this window are withheld "+
			"until it clears.",
		len(bad), total, strings.Join(bad, ", "))
}
