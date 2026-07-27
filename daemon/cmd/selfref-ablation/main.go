// selfref-ablation retrains the feature-store model legs twice on one frozen
// snapshot — once on the shipped post-A7 layout (forecast_prob, forecast_lift,
// expectancy_hit_rate and n_used excluded by gbm.SelfReferentialKey) and once
// with those four keys restored — and prints the per-leg before/after OOS
// admission-gate table. A7 removed the keys on principle; this measures what
// the removal cost, so "the edge was not the shortcut" is a claim with a
// number attached (EDGE_PLAN.md publishes the result).
//
// The measurement (+0.026 mean lift, zero gate flips) then justified going
// further: pipeline.buildFeatureVector no longer writes the four keys at all,
// so this harness is now the standing REGRESSION harness for that deletion.
// It keeps working because the WITH arm restores the keys from historical
// snapshot rows, which carry them; rows logged after the deletion simply have
// nothing to restore, exactly like rows where an optional signal was absent.
//
// The WITH arm restores the four keys by RENAMING them (k -> k+"__preA7") in a
// copy of each row's vector, so they pass the shared exclusion predicate while
// production code stays untouched: the two arms then differ in nothing but the
// availability of those four features (and their derived presence bits).
//
// Freezing: the source DB is copied via VACUUM INTO from a read-only
// connection, and both arms train from the copy. The snapshot's sha256 plus a
// per-dataset row hash (symbol|ts|up|fwd over the exact labeled rows consumed)
// are printed so the run is reproducible against the same inputs.
//
// Legs covered: the per-symbol GBM leg (mirrors pipeline.GBMTrainer: version-
// pinned rows, gbmMaxRows, WithLabelSpan purge, 5 folds, gbm.Defaults) and the
// pooled cross-sectional alphax leg (mirrors pipeline.AlphaXTrainer: v3+ pool,
// 50k cap, day-purged Evaluate with the horizon-aware embargo). The
// mean-reversion and linear forecast legs never read the feature store through
// the exclusion predicate (meanrev consumes pressure_score only; forecast
// trains on its own indicator inputs), so the ablation cannot move them.
//
// Usage:
//
//	go run ./cmd/selfref-ablation -db ../data/signaldeck.db -snapshot /tmp/ablate.db
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/alphax"
	"github.com/nyaungnicholas-wq/signaldeck/internal/gbm"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"

	_ "modernc.org/sqlite"
)

// Mirrored trainer constants (pipeline/gbmtrain.go, pipeline/alphax.go). Kept
// as literals here so this harness reproduces the production training pass
// exactly; if the trainers change these, change them here too.
const (
	gbmMaxRows              = 5000
	gbmFolds                = 5
	alphaXMaxRows           = 50000
	alphaXMinFeatureVersion = 3
	alphaXFolds             = 5
)

// ablatedKeys are exactly the four feature keys A7 removed from training.
var ablatedKeys = []string{"forecast_prob", "forecast_lift", "expectancy_hit_rate", "n_used"}

// preA7Suffix disguises an ablated key from gbm.SelfReferentialKey in the WITH
// arm. Chosen so the renamed key trips none of the predicate's rules (it is not
// a listed name and ends in none of _lift / _hit_rate / __has).
const preA7Suffix = "__preA7"

func horizonSecs(h md.Horizon) int64 {
	if h == md.H1w {
		return 7 * 86400
	}
	return 86400
}

// alphaXEmbargoDays mirrors pipeline.alphaXEmbargoDays (H1: label span in whole
// days plus one).
func alphaXEmbargoDays(h md.Horizon) int {
	return int((horizonSecs(h)+86399)/86400) + 1
}

// restoreAblated returns rows whose vectors carry the four ablated keys under
// their preA7Suffix aliases (values untouched); rows lacking a key are left
// lacking it, so the derived __has presence bits are faithful too.
func restoreAblated(rows []store.LabeledFeature) []store.LabeledFeature {
	out := make([]store.LabeledFeature, len(rows))
	for i, r := range rows {
		vec := make(map[string]float64, len(r.Vec))
		for k, v := range r.Vec {
			vec[k] = v
		}
		for _, k := range ablatedKeys {
			if v, ok := r.Vec[k]; ok {
				vec[k+preA7Suffix] = v
			}
		}
		out[i] = r
		out[i].Vec = vec
	}
	return out
}

// modelKeys mirrors pipeline.modelFeatureKeysExcluding(rows, nil): the sorted
// base-key union minus self-referential keys and reserved-suffix raw keys, each
// base followed by its derived presence indicator.
func modelKeys(rows []store.LabeledFeature) []string {
	set := map[string]struct{}{}
	for _, r := range rows {
		for k := range r.Vec {
			if gbm.SelfReferentialKey(k) || strings.HasSuffix(k, gbm.PresenceSuffix) {
				continue
			}
			set[k] = struct{}{}
		}
	}
	keys := make([]string, 0, 2*len(set))
	for k := range set {
		keys = append(keys, k, k+gbm.PresenceSuffix)
	}
	sort.Strings(keys)
	return keys
}

// flatten mirrors pipeline.flatten: presence indicators derived, absent bases 0.
func flatten(vec map[string]float64, keys []string) []float64 {
	out := make([]float64, len(keys))
	for i, k := range keys {
		if b, ok := strings.CutSuffix(k, gbm.PresenceSuffix); ok {
			if _, present := vec[b]; present {
				out[i] = 1
			}
			continue
		}
		out[i] = vec[k]
	}
	return out
}

func gbmSamples(rows []store.LabeledFeature, keys []string) []gbm.Sample {
	out := make([]gbm.Sample, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		out = append(out, gbm.Sample{Ts: rows[i].Ts, Feat: flatten(rows[i].Vec, keys), Y: float64(rows[i].Up)})
	}
	return out
}

// arm accumulates one training arm's admission-gate outcomes across legs.
type arm struct {
	graded   int
	admitted int // lift > 0: the leg would enter the live blend
	sumLift  float64
	sumLiftN float64 // N-weighted
	sumN     int
	lifts    []float64
}

func (a *arm) add(g gbm.Grade) {
	a.graded++
	if g.Lift > 0 {
		a.admitted++
	}
	a.sumLift += g.Lift
	a.sumLiftN += g.Lift * float64(g.N)
	a.sumN += g.N
	a.lifts = append(a.lifts, g.Lift)
}

func (a *arm) meanLift() float64 {
	if a.graded == 0 {
		return 0
	}
	return a.sumLift / float64(a.graded)
}

func (a *arm) weightedLift() float64 {
	if a.sumN == 0 {
		return 0
	}
	return a.sumLiftN / float64(a.sumN)
}

func (a *arm) medianLift() float64 {
	if len(a.lifts) == 0 {
		return 0
	}
	s := append([]float64(nil), a.lifts...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func main() {
	dbPath := flag.String("db", "../data/signaldeck.db", "source SQLite DB (opened read-only)")
	snapPath := flag.String("snapshot", "", "path for the frozen VACUUM INTO copy (required)")
	versions := flag.String("versions", "8,10", "featureVersions to ablate the per-symbol GBM leg on")
	flag.Parse()
	if *snapPath == "" {
		fmt.Fprintln(os.Stderr, "-snapshot is required")
		os.Exit(2)
	}

	ctx := context.Background()

	// Freeze: copy the live DB from a read-only connection, then never touch
	// the source again.
	if _, err := os.Stat(*snapPath); err == nil {
		fmt.Fprintf(os.Stderr, "reusing existing snapshot %s\n", *snapPath)
	} else {
		src, err := sql.Open("sqlite", "file:"+*dbPath+"?mode=ro&_pragma=busy_timeout(10000)")
		fatal(err, "open source")
		_, err = src.Exec("VACUUM INTO ?", *snapPath)
		fatal(err, "vacuum into snapshot")
		fatal(src.Close(), "close source")
	}
	snapSHA, err := sha256File(*snapPath)
	fatal(err, "hash snapshot")

	st, err := store.Open(*snapPath)
	fatal(err, "open snapshot store")

	fmt.Printf("# selfref ablation — %s\n", time.Now().UTC().Format("2006-01-02 15:04 MST"))
	fmt.Printf("source=%s\nsnapshot=%s\nsnapshot_sha256=%s\n", *dbPath, *snapPath, snapSHA)
	fmt.Printf("ablated keys (A7): %s\n\n", strings.Join(ablatedKeys, ", "))

	syms, err := st.ListSymbols(ctx, true)
	fatal(err, "list symbols")
	horizons := []md.Horizon{md.H1d, md.H1w}

	// ── per-symbol GBM leg ──────────────────────────────────────────────────
	for _, vs := range strings.Split(*versions, ",") {
		var version int
		fatal(scanInt(vs, &version), "parse -versions")
		for _, h := range horizons {
			var without, with arm
			var pairedDelta float64
			var paired int
			type legRow struct {
				sym                   string
				n                     int
				liftWith, liftWithout float64
			}
			var legs []legRow
			hash := sha256.New()
			rowsTotal := 0
			for _, s := range syms {
				rows, err := st.LabeledFeaturesBySymbolVersion(ctx, s.ID, h, version, gbmMaxRows)
				fatal(err, "labeled rows")
				if len(rows) == 0 {
					continue
				}
				rowsTotal += len(rows)
				for _, r := range rows {
					_, _ = fmt.Fprintf(hash, "%d|%d|%d|%.10g\n", r.SymbolID, r.Ts, r.Up, r.FwdReturn)
				}

				// WITHOUT: the shipped post-A7 path.
				keys := modelKeys(rows)
				var gWithout gbm.Grade
				okWithout := false
				if len(keys) > 0 {
					samples := gbm.WithLabelSpan(gbmSamples(rows, keys), horizonSecs(h))
					if _, g, ok := gbm.Run(samples, flatten(rows[0].Vec, keys), gbmFolds, gbm.Defaults()); ok {
						without.add(g)
						gWithout, okWithout = g, true
					}
				}

				// WITH: identical rows plus the four pre-A7 keys.
				pre := restoreAblated(rows)
				keys = modelKeys(pre)
				if len(keys) > 0 {
					samples := gbm.WithLabelSpan(gbmSamples(pre, keys), horizonSecs(h))
					if _, g, ok := gbm.Run(samples, flatten(pre[0].Vec, keys), gbmFolds, gbm.Defaults()); ok {
						with.add(g)
						if okWithout {
							paired++
							pairedDelta += g.Lift - gWithout.Lift
							legs = append(legs, legRow{sym: s.Symbol, n: g.N, liftWith: g.Lift, liftWithout: gWithout.Lift})
						}
					}
				}
			}
			fmt.Printf("## GBM leg · v%d · %s — labeled rows %d (hash %x)\n", version, h, rowsTotal, hash.Sum(nil)[:8])
			printArms(&with, &without, paired, pairedDelta)
			if len(legs) > 0 {
				sort.Slice(legs, func(a, b int) bool { return legs[a].liftWithout > legs[b].liftWithout })
				fmt.Printf("  per-leg (OOS lift; admitted = lift>0):\n")
				fmt.Printf("  %-12s %6s %12s %12s %8s %s\n", "symbol", "OOS-N", "with(preA7)", "without", "Δ", "gate flip?")
				for _, l := range legs {
					flip := "no"
					if (l.liftWith > 0) != (l.liftWithout > 0) {
						flip = "YES"
					}
					fmt.Printf("  %-12s %6d %+12.4f %+12.4f %+8.4f %s\n", l.sym, l.n, l.liftWith, l.liftWithout, l.liftWith-l.liftWithout, flip)
				}
				fmt.Println()
			}
		}
	}

	// ── pooled cross-sectional alphax leg ───────────────────────────────────
	for _, h := range horizons {
		rows, err := st.LabeledFeaturesAll(ctx, h, alphaXMinFeatureVersion, alphaXMaxRows)
		fatal(err, "alphax pool")
		if len(rows) == 0 {
			continue
		}
		hash := sha256.New()
		for _, r := range rows {
			_, _ = fmt.Fprintf(hash, "%d|%d|%d|%.10g\n", r.SymbolID, r.Ts, r.Up, r.FwdReturn)
		}
		build := func(rs []store.LabeledFeature) alphax.Dataset {
			l := make([]alphax.LabeledRow, len(rs))
			for i, r := range rs {
				l[i] = alphax.LabeledRow{SymbolID: r.SymbolID, Ts: r.Ts, Features: r.Vec, FwdReturn: r.FwdReturn}
			}
			return alphax.BuildDataset(l)
		}
		fmt.Printf("## alphax leg (pooled) · v%d+ · %s — pooled rows %d (hash %x)\n",
			alphaXMinFeatureVersion, h, len(rows), hash.Sum(nil)[:8])
		gc, okc, reasonc := alphax.Evaluate(build(rows), alphaXFolds, alphaXEmbargoDays(h), gbm.Defaults())
		gw, okw, reasonw := alphax.Evaluate(build(restoreAblated(rows)), alphaXFolds, alphaXEmbargoDays(h), gbm.Defaults())
		printAlphaXArm("with shortcuts (pre-A7)", gw, okw, reasonw)
		printAlphaXArm("without (shipped)      ", gc, okc, reasonc)
		if okc && okw {
			fmt.Printf("  Δlift (with − without): %+.4f\n", gw.Lift-gc.Lift)
		}
		fmt.Println()
	}
}

func printArms(with, without *arm, paired int, pairedDelta float64) {
	f := func(name string, a *arm) {
		fmt.Printf("  %-24s graded=%4d admitted(lift>0)=%4d (%.1f%%) meanLift=%+.4f medianLift=%+.4f NwLift=%+.4f OOS-N=%d\n",
			name, a.graded, a.admitted, pct(a.admitted, a.graded), a.meanLift(), a.medianLift(), a.weightedLift(), a.sumN)
	}
	f("with shortcuts (pre-A7)", with)
	f("without (shipped)", without)
	if paired > 0 {
		fmt.Printf("  paired legs=%d mean Δlift (with − without) = %+.4f\n", paired, pairedDelta/float64(paired))
	}
	fmt.Println()
}

func printAlphaXArm(name string, g alphax.Grade, ok bool, reason string) {
	if !ok {
		fmt.Printf("  %s refused: %s\n", name, reason)
		return
	}
	admitted := "GATED OUT"
	if g.Lift > 0 {
		admitted = "ADMITTED"
	}
	fmt.Printf("  %s lift=%+.4f acc=%.4f base=%.4f auc=%.4f N=%d → %s\n",
		name, g.Lift, g.Accuracy, g.BaseRate, g.AUC, g.N, admitted)
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func scanInt(s string, out *int) error {
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", out)
	return err
}

func fatal(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}
