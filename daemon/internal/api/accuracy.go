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
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// verdict needs are modelled; the rest of the registry is passed through
// untouched so the API can never silently drop a field the grader published.
type registryFile struct {
	GradedAt          string          `json:"graded_at"`
	RefusedSince      *string         `json:"refused_since"`
	GraderSHA256      string          `json:"grader_sha256"`
	MinIndependentN   float64         `json:"min_independent_n"`
	MinDistinctBlocks int             `json:"min_distinct_blocks"`
	Rows              []registryRow   `json:"rows"`
	Raw               json.RawMessage `json:"-"`
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
	now := time.Now().UTC()

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

	claims, _ := d.St.EvidenceClaims(ctx, "", "")

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
		reg.Raw = b
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
