package researchx

import (
	"sort"
	"strings"

	rl "github.com/nyaungnicholas-wq/signaldeck/internal/researchledger"
)

// GraphNode is one node of the evidence graph.
// Kind: "hypothesis" | "family" | "attack" | "era" | "evidence_kind".
// Posterior/Status are populated on hypothesis nodes only.
type GraphNode struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Label     string  `json:"label"`
	Posterior float64 `json:"posterior"`
	Status    string  `json:"status"`
}

// GraphEdge links a hypothesis to a concept node. Weight counts the linking
// evidence rows (1 for structural family membership); AvgBF is their mean
// Bayes factor (0 on structural edges — not applicable, not "neutral").
type GraphEdge struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Weight int     `json:"weight"`
	AvgBF  float64 `json:"avgBf"`
}

// Graph is the evidence graph: hypotheses linked to their families, the
// attacks run against them, the eras their evidence graded, and the kinds of
// evidence they carry.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// BuildGraph links each hypothesis to its family node, to attack:<name> nodes
// (name = the attack note's prefix before ':'), to era:<era> nodes (evidence
// notes carrying "era=<era>"), and to kind:<kind> nodes. Evidence whose HypID
// is not among hyps is ignored — no dangling edges. Ordering is
// deterministic: nodes by (kind group, ID), edges by (From, To).
func BuildGraph(hyps []rl.Hypothesis, evidence []rl.Evidence) Graph {
	known := map[string]bool{}
	var nodes []GraphNode
	families := map[string]bool{}
	for _, h := range hyps {
		known[h.ID] = true
		nodes = append(nodes, GraphNode{
			ID: h.ID, Kind: "hypothesis", Label: h.Statement,
			Posterior: h.Posterior, Status: h.Status,
		})
		if h.Family != "" {
			families[h.Family] = true
		}
	}

	type edgeAgg struct {
		weight int
		sumBF  float64
	}
	edges := map[[2]string]*edgeAgg{}
	bump := func(from, to string, bf float64, hasBF bool) {
		k := [2]string{from, to}
		e := edges[k]
		if e == nil {
			e = &edgeAgg{}
			edges[k] = e
		}
		e.weight++
		if hasBF {
			e.sumBF += bf
		}
	}

	for _, h := range hyps {
		if h.Family != "" {
			bump(h.ID, "family:"+h.Family, 0, false)
		}
	}

	attacks, eras, kinds := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range evidence {
		if !known[e.HypID] {
			continue
		}
		kinds[e.Kind] = true
		bump(e.HypID, "kind:"+e.Kind, e.BF, true)
		if e.Kind == rl.KindAttack {
			name := attackNoteName(e.Note)
			attacks[name] = true
			bump(e.HypID, "attack:"+name, e.BF, true)
		}
		if era := noteEra(e.Note); era != "" {
			eras[era] = true
			bump(e.HypID, "era:"+era, e.BF, true)
		}
	}

	for _, fam := range sortedKeys(families) {
		nodes = append(nodes, GraphNode{ID: "family:" + fam, Kind: "family", Label: fam})
	}
	for _, name := range sortedKeys(attacks) {
		nodes = append(nodes, GraphNode{ID: "attack:" + name, Kind: "attack", Label: name})
	}
	for _, era := range sortedKeys(eras) {
		nodes = append(nodes, GraphNode{ID: "era:" + era, Kind: "era", Label: era})
	}
	for _, kind := range sortedKeys(kinds) {
		nodes = append(nodes, GraphNode{ID: "kind:" + kind, Kind: "evidence_kind", Label: kind})
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		ri, rj := nodeRank(nodes[i].Kind), nodeRank(nodes[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return nodes[i].ID < nodes[j].ID
	})

	out := make([]GraphEdge, 0, len(edges))
	for k, e := range edges {
		avg := 0.0
		if !strings.HasPrefix(k[1], "family:") {
			avg = e.sumBF / float64(e.weight)
		}
		out = append(out, GraphEdge{From: k[0], To: k[1], Weight: e.weight, AvgBF: avg})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return Graph{Nodes: nodes, Edges: out}
}

func nodeRank(kind string) int {
	switch kind {
	case "hypothesis":
		return 0
	case "family":
		return 1
	case "attack":
		return 2
	case "era":
		return 3
	}
	return 4 // evidence_kind
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// attackNoteName mirrors researchledger's unexported attackName: an attack's
// name is its note prefix before the first ':'.
func attackNoteName(note string) string {
	if i := strings.IndexByte(note, ':'); i >= 0 {
		return note[:i]
	}
	return note
}

// noteEra extracts the "era=<era>" marker embedded in engine evidence notes;
// "" when absent. Era tokens are [a-z0-9_].
func noteEra(note string) string {
	i := strings.Index(note, "era=")
	if i < 0 {
		return ""
	}
	j := i + len("era=")
	k := j
	for k < len(note) && (note[k] == '_' || (note[k] >= 'a' && note[k] <= 'z') || (note[k] >= '0' && note[k] <= '9')) {
		k++
	}
	if k == j {
		return ""
	}
	return note[j:k]
}
