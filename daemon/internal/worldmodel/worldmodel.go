// Package worldmodel is a curated "world model" of the macro economy: a
// DIRECTIONAL causal graph that wires macro drivers → sectors → representative
// liquid symbols, classifies each driver's current state from live FRED values,
// and propagates a named shock along the cause-and-effect chains to say which
// symbols move which way.
//
// # What it is (and is NOT)
//
// The graph encodes the DIRECTION (a signed edge) and the RATIONALE of
// well-established macro relationships — "higher oil feeds inflation",
// "higher rates widen bank net interest margins", "higher mortgage rates cool
// housing". It does NOT encode learned or calibrated MAGNITUDES: there is no
// fitted beta, no elasticity, no probability. An edge says "these move the same
// way" (+1) or "these move opposite ways" (-1) and nothing more. Treat the whole
// thing as a transparent reasoning scaffold, not a forecast.
//
// # Honesty doctrine (mirrors internal/scenario, internal/graph, internal/sectors)
//
//   - Every edge carries a plain-English Rationale naming the mechanism. The sign
//     is a directional claim the mechanism justifies, never a hidden weight.
//
//   - Driver states are classified from live FRED values against DOCUMENTED,
//     fixed thresholds (constants below), so a state means the same thing across
//     time. A series that is missing from the input map leaves its driver
//     "unknown"/Known=false — the model never fabricates a value or guesses a
//     state it cannot ground.
//
//   - Some drivers are STRUCTURAL: they have no live series in this macro set
//     (geopolitical_risk, growth, usd, consumer, housing). They are honest about
//     it — Series "", Known=false, State "unknown" — rather than being proxied
//     onto an unrelated series. CPIAUCSL is a price-INDEX level (not a YoY rate),
//     so inflation's live state is a coarse presence flag with a loud caveat in
//     its Note, never a fabricated inflation rate.
//
// # Propagation
//
// Propagate walks the graph from a shock node by a DETERMINISTIC shortest-path
// breadth-first search. Each reached node's effect is the shock's direction
// multiplied by the product of the edge signs on the path to it, so an inverse
// edge flips the direction. Competing causal paths are NOT netted (there are no
// magnitudes to net); the first, shortest path to a node wins. The result is a
// map of who-moves-which-way, not a sized projection.
//
// Every function is pure and deterministic: values in, graph/propagation out. No
// I/O, no persistence, no clock, no randomness. Ordering is made deterministic by
// explicit sorting, never by relying on map iteration order.
package worldmodel

import (
	"sort"
	"strings"
)

// Node kinds. Kept plain strings for easy JSON serialization; these constants are
// the canonical values Node.Kind takes.
const (
	KindDriver = "driver"
	KindSector = "sector"
	KindSymbol = "symbol"
)

// DriverState is one macro driver's live classification. Value/Known are only set
// when a live series classified the state; a structural or missing driver stays
// State "unknown", Known=false, Value 0.
type DriverState struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	State  string  `json:"state"`  // e.g. "low"/"normal"/"elevated"/"high"/"inverted"/"wide"/"tight"/"unknown"
	Series string  `json:"series"` // the FRED series that feeds it ("" if not live-fed)
	Value  float64 `json:"value"`  // the live value (0 if unknown)
	Known  bool    `json:"known"`  // true if a live value classified the state
	Note   string  `json:"note"`
}

// Node is one vertex of the causal graph.
type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // "driver" | "sector" | "symbol"
}

// Edge is a directed causal link. Sign is the DIRECTION of the relationship
// (+1 same-direction, -1 inverse); Rationale names the mechanism. No magnitude.
type Edge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Sign      int    `json:"sign"` // +1 same-direction, -1 inverse
	Rationale string `json:"rationale"`
}

// Graph is the assembled world model: classified driver states, every node, and
// every signed edge, plus a Note stating the model's directional-not-magnitude
// contract.
type Graph struct {
	Drivers []DriverState `json:"drivers"`
	Nodes   []Node        `json:"nodes"`
	Edges   []Edge        `json:"edges"`
	Note    string        `json:"note"`
}

// PropStep is one driver/sector node reached during a shock walk, with its net
// direction.
type PropStep struct {
	Node   string `json:"node"`
	Label  string `json:"label"`
	Effect string `json:"effect"` // "up" | "down"
}

// Affected is one symbol reached by a shock, with its net direction and the
// human-readable causal path (drivers/sectors) that led to it.
type Affected struct {
	Symbol string `json:"symbol"`
	Market string `json:"market"` // "stocks"
	Effect string `json:"effect"` // "up" | "down"
	Path   string `json:"path"`   // human chain e.g. "oil→inflation→rates→banks"
}

// Propagation is the ordered chain of driver/sector effects plus the affected
// symbols for one named shock.
type Propagation struct {
	Shock    string     `json:"shock"`
	Label    string     `json:"label"`
	Chain    []PropStep `json:"chain"`
	Affected []Affected `json:"affected"`
	Note     string     `json:"note"`
}

const graphNote = "Curated DIRECTIONAL causal graph: drivers → sectors → representative symbols. " +
	"Edges encode a KNOWN sign and rationale (the direction of a well-established relationship), " +
	"NOT learned or calibrated magnitudes. Driver states are classified from live FRED values against " +
	"documented thresholds; a missing series stays 'unknown' rather than being fabricated. " +
	"Use it as a reasoning scaffold, not a forecast."

const propagationNote = "Deterministic shortest-path walk over the causal graph. Effects are DIRECTIONS only " +
	"(up/down) from multiplying edge signs — an inverse edge flips the direction. No magnitudes are modeled " +
	"and competing causal paths are NOT netted (the first, shortest path to each node wins). " +
	"A directional map of who-moves-which-way, not a sized forecast."

// --- Driver state thresholds -------------------------------------------------
//
// Fixed as constants so each state means the same thing across time (a threshold
// that drifts is not a classification). Bands are the conventional, widely-used
// ones for each series.

// VIX (VIXCLS) regime bands, index points: <15 low, [15,20) normal,
// [20,28] elevated, >28 high.
func classifyVIX(v float64) string {
	switch {
	case v < 15:
		return "low"
	case v < 20:
		return "normal"
	case v <= 28:
		return "elevated"
	default:
		return "high"
	}
}

// 10y yield (DGS10), percent: <2 low, [2,3.5) normal, [3.5,4.5) elevated,
// >=4.5 high.
func classifyRates(v float64) string {
	switch {
	case v < 2:
		return "low"
	case v < 3.5:
		return "normal"
	case v < 4.5:
		return "elevated"
	default:
		return "high"
	}
}

// 2s10s slope (T10Y2Y), percentage points: <0 inverted, [0,0.5) flat, else
// normal. A negative 10y-2y spread is the classic recession-warning inversion.
func classifyCurve(v float64) string {
	switch {
	case v < 0:
		return "inverted"
	case v < 0.5:
		return "flat"
	default:
		return "normal"
	}
}

// HY OAS (BAMLH0A0HYM2), percentage points: >5 wide, [3.5,5] normal, <3.5 tight.
func classifyCreditSpread(v float64) string {
	switch {
	case v > 5:
		return "wide"
	case v < 3.5:
		return "tight"
	default:
		return "normal"
	}
}

// NFCI: >0 tighter-than-average conditions ("tight"), else "loose". Zero and
// below read loose by this convention.
func classifyNFCI(v float64) string {
	if v > 0 {
		return "tight"
	}
	return "loose"
}

// WTI crude (DCOILWTICO), USD/barrel: <60 low, [60,80) normal, [80,100) elevated,
// >=100 high.
func classifyOil(v float64) string {
	switch {
	case v < 60:
		return "low"
	case v < 80:
		return "normal"
	case v < 100:
		return "elevated"
	default:
		return "high"
	}
}

// Unemployment rate (UNRATE), percent: <4 low, [4,5) normal, [5,6.5) elevated,
// >=6.5 high.
func classifyUnemployment(v float64) string {
	switch {
	case v < 4:
		return "low"
	case v < 5:
		return "normal"
	case v < 6.5:
		return "elevated"
	default:
		return "high"
	}
}

// CPIAUCSL is a price-INDEX LEVEL, not a YoY inflation rate, so a single reading
// cannot be classified high/low. This returns a coarse presence flag only; the
// honest caveat lives in the inflation driver's Note. Never treat "elevated" here
// as a measured high-inflation state.
func classifyInflationIndex(_ float64) string {
	return "elevated"
}

// driverDefs is the ordered driver table: id, display label, the FRED series that
// feeds it ("" = structural / not live-fed), its classifier (nil for structural),
// and a Note carrying its data semantics and any honesty caveat. Order is the
// causal reading order and fixes Graph.Drivers output order.
var driverDefs = []struct {
	id, label, series string
	classify          func(float64) string
	note              string
}{
	{"geopolitical_risk", "Geopolitical Risk", "", nil,
		"Structural driver with no FRED feed; set by world events, not a live series."},
	{"oil", "Oil (WTI)", "DCOILWTICO", classifyOil,
		"WTI crude spot (DCOILWTICO), USD/barrel; classified by level buckets."},
	{"inflation", "Inflation (CPI)", "CPIAUCSL", classifyInflationIndex,
		"CPIAUCSL is a price-INDEX level, not a YoY inflation rate; this model flags presence only and " +
			"cannot assert a true inflation rate from a single level — derive YoY for that."},
	{"growth", "Growth", "", nil,
		"No direct series in this macro set; growth is a structural node (proxy candidates: real GDP, PMI). Not live-fed."},
	{"rates", "10Y Treasury Yield", "DGS10", classifyRates,
		"10-year Treasury yield (DGS10), percent; classified by level buckets."},
	{"yield_curve", "Yield Curve (10y-2y)", "T10Y2Y", classifyCurve,
		"10y-minus-2y spread (T10Y2Y), percentage points; a negative spread is an inversion."},
	{"credit_spreads", "HY Credit Spread (OAS)", "BAMLH0A0HYM2", classifyCreditSpread,
		"ICE BofA US high-yield OAS (BAMLH0A0HYM2), percentage points; >5 reads wide."},
	{"financial_conditions", "Financial Conditions (NFCI)", "NFCI", classifyNFCI,
		"Chicago Fed NFCI; >0 is tighter-than-average conditions, <=0 looser."},
	{"volatility", "Volatility (VIX)", "VIXCLS", classifyVIX,
		"CBOE VIX close (VIXCLS); classified by this package's own regime bands."},
	{"usd", "US Dollar", "", nil,
		"No dollar-index series in this macro set (e.g. DTWEXBGS); structural node, not live-fed."},
	{"unemployment", "Unemployment Rate", "UNRATE", classifyUnemployment,
		"Civilian unemployment rate (UNRATE), percent; classified by level buckets."},
	{"consumer", "Consumer", "", nil,
		"No consumer-health series in this macro set (e.g. UMCSENT/PCE); structural node, not live-fed."},
	{"housing", "Housing", "", nil,
		"Housing-activity intermediate for the rates→housing→homebuilders chain " +
			"(proxy candidates: HOUST, mortgage rates); not live-fed."},
}

// sectorDefs is the ordered sector table with each sector's representative liquid
// symbols (a sector ETF plus mega-cap single names where liquid). Order fixes the
// node output order and the sector→symbol edge order.
var sectorDefs = []struct {
	id, label string
	symbols   []string
}{
	{"energy", "Energy", []string{"XLE", "XOM", "CVX"}},
	{"financials", "Financials", []string{"XLF", "JPM", "BAC"}},
	{"technology", "Technology", []string{"XLK", "AAPL", "MSFT"}},
	{"healthcare", "Healthcare", []string{"XLV"}},
	{"industrials", "Industrials", []string{"XLI"}},
	{"consumer_discretionary", "Consumer Discretionary", []string{"XLY"}},
	{"consumer_staples", "Consumer Staples", []string{"XLP"}},
	{"utilities", "Utilities", []string{"XLU"}},
	{"materials", "Materials", []string{"XLB"}},
	{"real_estate", "Real Estate", []string{"XLRE"}},
	{"homebuilders", "Homebuilders", []string{"XHB", "DHI", "LEN"}},
	{"semiconductors", "Semiconductors", []string{"SMH", "NVDA"}},
}

// causalEdges are the driver→driver and driver→sector edges — the known,
// signed macro relationships. Sector→symbol edges (all +1) are generated from
// sectorDefs in causalGraph. Healthcare and consumer_staples intentionally have
// no driver edges: they are defensives with no strong, single-signed macro driver
// in this model, and inventing one would be dishonest.
var causalEdges = []Edge{
	// Flagship chain: geopolitics → oil → inflation → rates → banks; and
	// rates → housing → homebuilders.
	{"geopolitical_risk", "oil", +1, "Conflict and supply disruptions push crude prices up."},
	{"geopolitical_risk", "volatility", +1, "Geopolitical shocks spike implied volatility."},
	{"oil", "inflation", +1, "Energy costs pass through into headline inflation."},
	{"inflation", "rates", +1, "Higher inflation lifts the 10y via Fed policy and term premium."},
	{"rates", "housing", -1, "Higher mortgage rates cool housing demand."},
	{"housing", "homebuilders", +1, "Homebuilder earnings track housing activity."},
	{"rates", "financials", +1, "Higher rates widen bank net interest margins."},

	// Other driver→driver links.
	{"inflation", "consumer", -1, "Inflation erodes real incomes and squeezes the consumer."},
	{"usd", "oil", -1, "A stronger dollar lowers dollar-priced crude."},
	{"rates", "growth", -1, "Tighter financial policy slows economic growth."},
	{"growth", "unemployment", -1, "Faster growth lowers unemployment (Okun's law)."},
	{"growth", "credit_spreads", -1, "Stronger growth narrows default risk and credit spreads."},
	{"unemployment", "consumer", -1, "Job losses cut household spending power."},

	// Rates → rate-sensitive sectors.
	{"rates", "technology", -1, "Higher discount rates compress long-duration growth valuations."},
	{"rates", "semiconductors", -1, "Capital-intensive, long-duration; sensitive to higher rates."},
	{"rates", "consumer_discretionary", -1, "Higher financing costs curb big-ticket demand."},
	{"rates", "real_estate", -1, "Higher rates lift cap rates and financing costs (bond proxy)."},
	{"rates", "utilities", -1, "Bond-proxy defensives de-rate when yields rise."},

	// Curve, credit, conditions → financials and cyclicals.
	{"yield_curve", "financials", +1, "A steeper curve helps banks (borrow short, lend long); inversion hurts."},
	{"credit_spreads", "financials", -1, "Widening spreads raise loan-loss and funding risk for lenders."},
	{"credit_spreads", "consumer_discretionary", -1, "Credit stress curbs consumer credit and cyclicals."},
	{"credit_spreads", "real_estate", -1, "Wider spreads raise REIT funding costs."},
	{"financial_conditions", "financials", -1, "Tighter overall financial conditions weigh on financials."},
	{"financial_conditions", "technology", -1, "Tighter conditions de-rate growth equities."},
	{"financial_conditions", "consumer_discretionary", -1, "Tighter conditions curb discretionary demand."},

	// Volatility → high-beta risk assets.
	{"volatility", "technology", -1, "Risk-off volatility hits high-beta tech first."},
	{"volatility", "semiconductors", -1, "High-beta; sold hard in volatility spikes."},
	{"volatility", "consumer_discretionary", -1, "High-beta cyclical; sold in risk-off."},

	// Oil → energy (direct); dollar → multinationals/commodities.
	{"oil", "energy", +1, "Higher crude directly lifts energy-sector revenue and margins."},
	{"usd", "energy", -1, "A strong dollar pressures dollar-priced oil and energy names."},
	{"usd", "materials", -1, "Commodities are dollar-priced; a strong dollar weighs on materials."},
	{"usd", "technology", -1, "A strong dollar cuts mega-cap multinationals' overseas earnings."},
	{"usd", "semiconductors", -1, "Chip revenue is largely export/overseas; dollar-sensitive."},
	{"usd", "industrials", -1, "A strong dollar hurts industrial exporters' competitiveness."},

	// Growth → cyclicals.
	{"growth", "energy", +1, "Stronger demand supports energy consumption."},
	{"growth", "industrials", +1, "Cyclical capex and shipping track the growth cycle."},
	{"growth", "materials", +1, "Industrial demand for metals and chemicals is cyclical."},
	{"growth", "semiconductors", +1, "Chips are a cyclical bellwether of global demand."},
	{"growth", "consumer_discretionary", +1, "Cyclical; benefits from expansion."},

	// Consumer → discretionary.
	{"consumer", "consumer_discretionary", +1, "Discretionary spending tracks household health."},
}

// causalGraph assembles the full node and edge set. It is the single source of
// truth used by both Build and Propagate, returning fresh slices each call so no
// caller can mutate shared state. Edges are sorted by (From, To) for determinism.
func causalGraph() ([]Node, []Edge) {
	nodes := make([]Node, 0, len(driverDefs)+len(sectorDefs)+8)
	for _, d := range driverDefs {
		nodes = append(nodes, Node{ID: d.id, Label: d.label, Kind: KindDriver})
	}
	for _, s := range sectorDefs {
		nodes = append(nodes, Node{ID: s.id, Label: s.label, Kind: KindSector})
	}
	for _, s := range sectorDefs {
		for _, sym := range s.symbols {
			nodes = append(nodes, Node{ID: sym, Label: sym, Kind: KindSymbol})
		}
	}

	edges := make([]Edge, 0, len(causalEdges)+len(nodes))
	edges = append(edges, causalEdges...)
	for _, s := range sectorDefs {
		for _, sym := range s.symbols {
			edges = append(edges, Edge{
				From:      s.id,
				To:        sym,
				Sign:      +1,
				Rationale: "Representative liquid symbol for the " + s.label + " sector.",
			})
		}
	}
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})
	return nodes, edges
}

// Build returns the full graph with driver States classified from live macro
// values. macro is keyed by FRED series id (e.g. "VIXCLS","DGS10","DCOILWTICO",
// "T10Y2Y","BAMLH0A0HYM2","NFCI","CPIAUCSL","DFF","UNRATE"); a missing key => that
// driver's State is "unknown"/Known=false (never fabricate a value). Keys with no
// modeled driver (e.g. "DFF") are simply ignored. Safe with a nil or empty map.
func Build(macro map[string]float64) Graph {
	nodes, edges := causalGraph()

	drivers := make([]DriverState, 0, len(driverDefs))
	for _, d := range driverDefs {
		ds := DriverState{
			Key:    d.id,
			Label:  d.label,
			State:  "unknown",
			Series: d.series,
			Note:   d.note,
		}
		if d.series != "" && d.classify != nil {
			if v, ok := macro[d.series]; ok {
				ds.State = d.classify(v)
				ds.Value = v
				ds.Known = true
			}
		}
		drivers = append(drivers, ds)
	}

	return Graph{Drivers: drivers, Nodes: nodes, Edges: edges, Note: graphNote}
}

// shockDef names a shock: the graph node it hits, the initial direction, and a
// human label.
type shockDef struct {
	node  string
	sign  int
	label string
}

// shockDefs maps each supported shock key to its seed. Seeds are drivers; the
// sign is the shock's own direction (a "down" shock seeds -1).
var shockDefs = map[string]shockDef{
	"oil_up":        {"oil", +1, "Oil price shock — crude up"},
	"rates_up":      {"rates", +1, "Rates shock — 10y yields up"},
	"vol_up":        {"volatility", +1, "Volatility shock — VIX up"},
	"inflation_up":  {"inflation", +1, "Inflation shock — CPI up"},
	"credit_stress": {"credit_spreads", +1, "Credit stress — HY spreads widen"},
	"growth_down":   {"growth", -1, "Growth shock — activity down"},
}

// Shocks lists the supported shock keys in sorted order.
func Shocks() []string {
	keys := make([]string, 0, len(shockDefs))
	for k := range shockDefs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Propagate walks the causal chain from a named shock and returns the ordered
// chain of driver/sector effects plus the affected symbols with their net
// direction. ok=false for an unknown shock key.
//
// The walk is a deterministic shortest-path BFS: the first (shortest) path to a
// node fixes its effect and its causal path. An effect is the shock's direction
// times the product of edge signs along that path, so an inverse edge flips it.
// Competing paths are not netted — the model carries no magnitudes to net them.
func Propagate(shock string) (Propagation, bool) {
	sh, ok := shockDefs[shock]
	if !ok {
		return Propagation{}, false
	}

	nodes, edges := causalGraph()
	nodeByID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID] = n
	}

	// Adjacency, neighbours sorted by target id for a deterministic walk.
	adj := make(map[string][]Edge)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e)
	}
	for k := range adj {
		sort.SliceStable(adj[k], func(i, j int) bool { return adj[k][i].To < adj[k][j].To })
	}

	effect := map[string]int{sh.node: sh.sign}
	pathOf := map[string][]string{sh.node: {sh.node}}
	var order []string // non-seed nodes, in discovery order

	type item struct {
		node   string
		effect int
	}
	queue := []item{{sh.node, sh.sign}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range adj[cur.node] {
			if _, seen := effect[e.To]; seen {
				continue // first, shortest path wins
			}
			ne := cur.effect * e.Sign
			np := append(append([]string{}, pathOf[cur.node]...), e.To)
			effect[e.To] = ne
			pathOf[e.To] = np
			order = append(order, e.To)
			queue = append(queue, item{e.To, ne})
		}
	}

	// Chain: the seed then every driver/sector reached, in discovery order.
	chain := []PropStep{stepFor(sh.node, effect[sh.node], nodeByID)}
	for _, id := range order {
		if nodeByID[id].Kind == KindSymbol {
			continue
		}
		chain = append(chain, stepFor(id, effect[id], nodeByID))
	}

	// Affected: every symbol reached, path ending at its sector (symbol excluded).
	var affected []Affected
	for _, id := range order {
		if nodeByID[id].Kind != KindSymbol {
			continue
		}
		p := pathOf[id]
		affected = append(affected, Affected{
			Symbol: id,
			Market: "stocks",
			Effect: effectString(effect[id]),
			Path:   strings.Join(p[:len(p)-1], "→"),
		})
	}
	sort.SliceStable(affected, func(i, j int) bool { return affected[i].Symbol < affected[j].Symbol })

	return Propagation{
		Shock:    shock,
		Label:    sh.label,
		Chain:    chain,
		Affected: affected,
		Note:     propagationNote,
	}, true
}

// stepFor builds a PropStep for a node with a given signed effect.
func stepFor(id string, sign int, nodeByID map[string]Node) PropStep {
	return PropStep{Node: id, Label: nodeByID[id].Label, Effect: effectString(sign)}
}

// effectString renders a signed effect as a direction. Edge signs are ±1, so the
// product is never zero; a non-negative sign reads "up".
func effectString(sign int) string {
	if sign >= 0 {
		return "up"
	}
	return "down"
}
