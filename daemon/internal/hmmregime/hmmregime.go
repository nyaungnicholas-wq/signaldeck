// Package hmmregime fits a Gaussian hidden Markov model to a bar series and
// labels each bar with a VOLATILITY state — calm through turbulent — ordered so
// that state 0 is always the quietest and state N-1 always the wildest.
//
// It sits beside internal/regime, which labels the same thing with fixed
// threshold rules. This package exists to be graded against that one, not to
// replace it on assertion: cmd/hmmbakeoff runs both over stored bars and
// compares how well each label predicts the size of the NEXT move.
//
// Every function is pure: bars in, labels out. No I/O, no clock, no network, no
// randomness. Initialisation is quantile-based rather than seeded, so two Fit
// calls on the same bars return identical models.
//
// # No lookahead
//
// This is the property the package is built around, because the textbook way to
// write it does not have it. Fitting Baum-Welch over a whole series and then
// decoding with Viterbi or smoothed posteriors produces labels at bar i that
// depend on bars after i — the label for last Tuesday keeps changing as this
// week arrives. Such a timeline cannot be graded honestly and cannot be traded.
//
// So the two halves are kept apart. Fit runs the full forward-backward EM and
// is the only place smoothed quantities appear; it is meant to be handed a
// TRAINING PREFIX. Filter then runs the causal forward recursion alone with
// those parameters frozen, so the label at bar i is a function of bars[0..i]
// and nothing else. Truncating Filter's input leaves every earlier label
// bit-identical — TestFilterIsCausal asserts exactly that.
package hmmregime

import (
	"math"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
)

// Label is a volatility-regime name. Ordering is meaningful: Calm is the
// lowest-variance state, Turbulent the highest.
type Label string

const (
	// Calm is the lowest-fitted-variance state.
	Calm Label = "calm"
	// Turbulent is the highest-fitted-variance state.
	Turbulent Label = "turbulent"
)

// Config holds the fit hyperparameters.
type Config struct {
	// NStates is the number of hidden states (>= 2).
	NStates int
	// MinBars is the shortest series Fit will accept. Below it Fit refuses
	// rather than emitting a confident label off a handful of bars.
	MinBars int
	// MaxIter caps EM iterations.
	MaxIter int
	// Tol stops EM once the log-likelihood improves by less than this.
	Tol float64
}

// Defaults returns the standard configuration: two states, 60-bar minimum.
func Defaults() Config {
	return Config{NStates: 2, MinBars: 60, MaxIter: 200, Tol: 1e-6}
}

// Timestamped is one point on the regime timeline, as of bar Ts, using only
// bars up to and including Ts.
type Timestamped struct {
	// Ts is the bar time this label is as-of.
	Ts int64
	// Label is the most likely state's name.
	Label Label
	// Prob is that state's filtered posterior, in [0,1].
	Prob float64
	// StateIdx is that state's index, 0 = calmest.
	StateIdx int
}

// Model is a fitted HMM. States are stored in ascending order of fitted
// standard deviation.
type Model struct {
	nStates int
	pi      []float64   // initial state distribution
	mu      []float64   // per-state mean log return
	sigma2  []float64   // per-state variance of log return
	a       [][]float64 // transition matrix
}

// varianceFloor keeps a degenerate state from producing infinite densities.
const varianceFloor = 1e-12

// Fit learns an HMM from the log returns of bars via Baum-Welch. Hand it a
// TRAINING PREFIX: it uses the whole series it is given, including smoothed
// posteriors, so grading must happen on bars it never saw. Returns ok=false
// when the series is too short or the configuration is degenerate.
func Fit(bars []marketdata.Bar, cfg Config) (*Model, bool) {
	n := len(bars)
	if n < cfg.MinBars || cfg.NStates < 2 || cfg.MaxIter < 1 {
		return nil, false
	}
	nRet := n - 1
	N := cfg.NStates
	if nRet < N {
		return nil, false
	}
	rets := make([]float64, nRet)
	for i := 1; i < n; i++ {
		if bars[i-1].Close <= 0 || bars[i].Close <= 0 {
			return nil, false
		}
		rets[i-1] = math.Log(bars[i].Close / bars[i-1].Close)
	}

	// Deterministic init from quantiles of |return|, NOT of the signed return.
	// These are VOLATILITY states: banding on the signed level splits the
	// sample into "fell" and "rose" — two groups with different means and
	// nearly identical variances — and EM then converges to that mean split
	// and never finds the vol structure it was asked for. Banding on magnitude
	// seeds state 0 with the quietest returns and state N-1 with the wildest.
	sorted := make([]float64, nRet)
	copy(sorted, rets)
	sort.Slice(sorted, func(a, b int) bool {
		return math.Abs(sorted[a]) < math.Abs(sorted[b])
	})

	mu := make([]float64, N)
	sigma2 := make([]float64, N)
	for i := 0; i < N; i++ {
		start, end := i*nRet/N, (i+1)*nRet/N
		if end <= start {
			end = start + 1
		}
		if end > nRet {
			end = nRet
		}
		band := sorted[start:end]
		sum := 0.0
		for _, v := range band {
			sum += v
		}
		mu[i] = sum / float64(len(band))
		varSum := 0.0
		for _, v := range band {
			d := v - mu[i]
			varSum += d * d
		}
		sigma2[i] = math.Max(varSum/float64(len(band)), varianceFloor)
	}

	pi := make([]float64, N)
	for i := range pi {
		pi[i] = 1.0 / float64(N)
	}
	a := make([][]float64, N)
	off := 0.1 / float64(N-1)
	for i := range a {
		a[i] = make([]float64, N)
		for j := range a[i] {
			if i == j {
				a[i][j] = 0.9
			} else {
				a[i][j] = off
			}
		}
	}

	logAlpha := make([]float64, nRet*N)
	logBeta := make([]float64, nRet*N)
	logScales := make([]float64, nRet)
	work := make([]float64, N)
	prevLL := math.Inf(-1)

	for iter := 0; iter < cfg.MaxIter; iter++ {
		// Forward pass, rescaled at every step so alpha stays in range.
		for i := 0; i < N; i++ {
			logAlpha[i] = logSafe(pi[i]) + logGaussian(rets[0], mu[i], sigma2[i])
		}
		logScales[0] = logSumExp(logAlpha[:N])
		for i := 0; i < N; i++ {
			logAlpha[i] -= logScales[0]
		}
		for t := 1; t < nRet; t++ {
			for j := 0; j < N; j++ {
				for i := 0; i < N; i++ {
					work[i] = logAlpha[(t-1)*N+i] + logSafe(a[i][j])
				}
				logAlpha[t*N+j] = logSumExp(work) + logGaussian(rets[t], mu[j], sigma2[j])
			}
			logScales[t] = logSumExp(logAlpha[t*N : (t+1)*N])
			for i := 0; i < N; i++ {
				logAlpha[t*N+i] -= logScales[t]
			}
		}

		// Backward pass, rescaled by the SAME per-step factors. An unscaled
		// beta accumulates the entire series' magnitude, so exp(alpha+beta)
		// leaves float64 range long before gamma's normaliser can rescue it —
		// which zeroes gamma silently and freezes the M-step with every
		// variance pinned at the floor.
		for i := 0; i < N; i++ {
			logBeta[(nRet-1)*N+i] = 0
		}
		for t := nRet - 2; t >= 0; t-- {
			for i := 0; i < N; i++ {
				for j := 0; j < N; j++ {
					work[j] = logSafe(a[i][j]) +
						logGaussian(rets[t+1], mu[j], sigma2[j]) +
						logBeta[(t+1)*N+j]
				}
				logBeta[t*N+i] = logSumExp(work) - logScales[t+1]
			}
		}

		// gamma: smoothed state posteriors, normalised in LOG space.
		// Exponentiating first and dividing after is what overflows.
		gamma := make([][]float64, nRet)
		for t := 0; t < nRet; t++ {
			gamma[t] = make([]float64, N)
			for i := 0; i < N; i++ {
				work[i] = logAlpha[t*N+i] + logBeta[t*N+i]
			}
			ls := logSumExp(work)
			for i := 0; i < N; i++ {
				gamma[t][i] = math.Exp(work[i] - ls)
			}
		}

		// xi: smoothed transition posteriors, same treatment.
		xi := make([][][]float64, nRet-1)
		logXi := make([]float64, N*N)
		for t := 0; t < nRet-1; t++ {
			for i := 0; i < N; i++ {
				for j := 0; j < N; j++ {
					logXi[i*N+j] = logAlpha[t*N+i] + logSafe(a[i][j]) +
						logGaussian(rets[t+1], mu[j], sigma2[j]) +
						logBeta[(t+1)*N+j]
				}
			}
			ls := logSumExp(logXi)
			xi[t] = make([][]float64, N)
			for i := 0; i < N; i++ {
				xi[t][i] = make([]float64, N)
				for j := 0; j < N; j++ {
					xi[t][i][j] = math.Exp(logXi[i*N+j] - ls)
				}
			}
		}

		// M-step.
		copy(pi, gamma[0])
		for i := 0; i < N; i++ {
			den := 0.0
			for t := 0; t < nRet-1; t++ {
				den += gamma[t][i]
			}
			rowSum := 0.0
			for j := 0; j < N; j++ {
				num := 0.0
				for t := 0; t < nRet-1; t++ {
					num += xi[t][i][j]
				}
				if den > 0 {
					a[i][j] = num / den
				}
				rowSum += a[i][j]
			}
			if rowSum > 0 {
				for j := 0; j < N; j++ {
					a[i][j] /= rowSum
				}
			}
		}
		for i := 0; i < N; i++ {
			den, num := 0.0, 0.0
			for t := 0; t < nRet; t++ {
				num += gamma[t][i] * rets[t]
				den += gamma[t][i]
			}
			if den > 0 {
				mu[i] = num / den
			}
			varNum := 0.0
			for t := 0; t < nRet; t++ {
				d := rets[t] - mu[i]
				varNum += gamma[t][i] * d * d
			}
			s2 := varianceFloor
			if den > 0 {
				s2 = varNum / den
			}
			sigma2[i] = math.Max(s2, varianceFloor)
		}

		// The scale factors ARE the per-step log-likelihood contributions.
		ll := 0.0
		for _, s := range logScales {
			ll += s
		}
		if iter > 0 && math.Abs(ll-prevLL) < cfg.Tol {
			break
		}
		prevLL = ll
	}

	// Sort states by fitted volatility so "turbulent" means the same thing on
	// every symbol. Without this EM's arbitrary convergence order decides which
	// index is which, and the grader ends up comparing nothing.
	perm := make([]int, N)
	for i := range perm {
		perm[i] = i
	}
	sort.Slice(perm, func(i, j int) bool { return sigma2[perm[i]] < sigma2[perm[j]] })

	m := &Model{
		nStates: N,
		pi:      make([]float64, N),
		mu:      make([]float64, N),
		sigma2:  make([]float64, N),
		a:       make([][]float64, N),
	}
	for ni, oi := range perm {
		m.pi[ni] = pi[oi]
		m.mu[ni] = mu[oi]
		m.sigma2[ni] = sigma2[oi]
		m.a[ni] = make([]float64, N)
		for nj, oj := range perm {
			m.a[ni][nj] = a[oi][oj]
		}
	}
	return m, true
}

// Filter labels every bar using ONLY the causal forward recursion with the
// model's parameters frozen, so the label at index i is a function of
// bars[0..i]. There is deliberately no backward pass here: adding one would
// make earlier labels move when later bars arrive.
//
// The input may be longer than the series Fit trained on; parameters do not
// update. Index 0 has no return yet and carries the initial distribution.
func (m *Model) Filter(bars []marketdata.Bar) []Timestamped {
	T := len(bars)
	if T == 0 {
		return []Timestamped{}
	}
	N := m.nStates
	out := make([]Timestamped, T)

	logAlpha := make([]float64, N)
	for i := 0; i < N; i++ {
		logAlpha[i] = logSafe(m.pi[i])
	}
	normalize(logAlpha)
	out[0] = m.point(bars[0].Ts, logAlpha)

	work := make([]float64, N)
	next := make([]float64, N)
	for t := 1; t < T; t++ {
		if bars[t-1].Close <= 0 || bars[t].Close <= 0 {
			// Unusable bar: carry the previous state rather than inventing one.
			out[t] = out[t-1]
			out[t].Ts = bars[t].Ts
			continue
		}
		r := math.Log(bars[t].Close / bars[t-1].Close)
		for j := 0; j < N; j++ {
			for i := 0; i < N; i++ {
				work[i] = logAlpha[i] + logSafe(m.a[i][j])
			}
			next[j] = logSumExp(work) + logGaussian(r, m.mu[j], m.sigma2[j])
		}
		copy(logAlpha, next)
		normalize(logAlpha)
		out[t] = m.point(bars[t].Ts, logAlpha)
	}
	return out
}

func (m *Model) point(ts int64, logAlpha []float64) Timestamped {
	best, bestLog := 0, math.Inf(-1)
	for i, v := range logAlpha {
		if v > bestLog {
			best, bestLog = i, v
		}
	}
	p := math.Exp(bestLog)
	if math.IsNaN(p) {
		p = 0
	}
	return Timestamped{Ts: ts, Label: m.LabelOf(best), Prob: math.Min(math.Max(p, 0), 1), StateIdx: best}
}

// NStates is the fitted state count.
func (m *Model) NStates() int { return m.nStates }

// Transitions returns a copy of the transition matrix, rows summing to 1.
func (m *Model) Transitions() [][]float64 {
	out := make([][]float64, m.nStates)
	for i := range out {
		out[i] = append([]float64(nil), m.a[i]...)
	}
	return out
}

// StateSDs returns each state's fitted standard deviation, ascending.
func (m *Model) StateSDs() []float64 {
	out := make([]float64, m.nStates)
	for i, v := range m.sigma2 {
		out[i] = math.Sqrt(v)
	}
	return out
}

// LabelOf names a state index. 0 is always Calm and the last is always
// Turbulent; any states between are mid1, mid2, ... ascending.
func (m *Model) LabelOf(i int) Label {
	switch {
	case i <= 0:
		return Calm
	case i >= m.nStates-1:
		return Turbulent
	default:
		return Label("mid" + itoa(i))
	}
}

// normalize rescales log-weights in place so their exponentials sum to 1.
func normalize(logW []float64) {
	ls := logSumExp(logW)
	for i := range logW {
		logW[i] -= ls
	}
}

// logGaussian is the log density of x under N(mu, sigma2).
func logGaussian(x, mu, sigma2 float64) float64 {
	if sigma2 < varianceFloor {
		sigma2 = varianceFloor
	}
	d := x - mu
	return -0.5 * (math.Log(2*math.Pi*sigma2) + d*d/sigma2)
}

// logSafe is log(x) with a finite floor, so a zero probability contributes a
// very negative term instead of a -Inf that turns into NaN on subtraction.
func logSafe(x float64) float64 {
	if x <= 0 || math.IsNaN(x) {
		return -700
	}
	return math.Log(x)
}

// logSumExp computes log(sum(exp(x))) by factoring out the max first.
func logSumExp(x []float64) float64 {
	if len(x) == 0 {
		return -700
	}
	max := math.Inf(-1)
	for _, v := range x {
		if v > max {
			max = v
		}
	}
	if math.IsInf(max, -1) || math.IsNaN(max) {
		return -700
	}
	sum := 0.0
	for _, v := range x {
		sum += math.Exp(v - max)
	}
	if sum <= 0 {
		return -700
	}
	return max + math.Log(sum)
}

// itoa renders a small non-negative int without pulling in strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
