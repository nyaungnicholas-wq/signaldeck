package lineage

import (
	"context"
	"fmt"
	"sort"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// MaxTraceDepth caps a walk regardless of what the caller asks for — the
// graph is small but an API-supplied depth must not be able to force an
// unbounded traversal.
const MaxTraceDepth = 6

// Node is one visited node with its hop distance from the root.
type Node struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Depth int    `json:"depth"`
}

// Graph is the connected subgraph Trace returns.
type Graph struct {
	Root  Node                `json:"root"`
	Nodes []Node              `json:"nodes"`
	Edges []store.LineageEdge `json:"edges"`
}

// Trace walks the lineage graph breadth-first in BOTH edge directions from
// (kind,id), up to depth hops (capped at MaxTraceDepth). Cycle-safe: every
// node is visited once (a visited set keyed on kind|id), so a cycle
// terminates instead of recursing. Returns the subgraph with each node's hop
// distance; a root with no edges returns just the root.
func Trace(ctx context.Context, st *store.Store, kind, id string, depth int) (Graph, error) {
	if !nodeKinds[kind] {
		return Graph{}, fmt.Errorf("lineage: unknown node kind %q", kind)
	}
	if id == "" {
		return Graph{}, fmt.Errorf("lineage: empty node id")
	}
	if depth <= 0 || depth > MaxTraceDepth {
		depth = MaxTraceDepth
	}

	root := Node{Kind: kind, ID: id, Depth: 0}
	g := Graph{Root: root, Nodes: []Node{root}}
	visited := map[string]bool{kind + "|" + id: true}
	seenEdge := map[string]bool{}
	frontier := []Node{root}

	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []Node
		for _, n := range frontier {
			edges, err := st.LineageEdgesTouching(ctx, n.Kind, n.ID)
			if err != nil {
				return Graph{}, err
			}
			for _, e := range edges {
				ek := e.SrcKind + "|" + e.SrcID + "|" + e.DstKind + "|" + e.DstID + "|" + e.EdgeKind
				if !seenEdge[ek] {
					seenEdge[ek] = true
					g.Edges = append(g.Edges, e)
				}
				for _, end := range [][2]string{{e.SrcKind, e.SrcID}, {e.DstKind, e.DstID}} {
					key := end[0] + "|" + end[1]
					if visited[key] {
						continue
					}
					visited[key] = true
					nn := Node{Kind: end[0], ID: end[1], Depth: d}
					g.Nodes = append(g.Nodes, nn)
					next = append(next, nn)
				}
			}
		}
		frontier = next
	}
	sort.Slice(g.Nodes, func(i, j int) bool {
		if g.Nodes[i].Depth != g.Nodes[j].Depth {
			return g.Nodes[i].Depth < g.Nodes[j].Depth
		}
		if g.Nodes[i].Kind != g.Nodes[j].Kind {
			return g.Nodes[i].Kind < g.Nodes[j].Kind
		}
		return g.Nodes[i].ID < g.Nodes[j].ID
	})
	return g, nil
}
