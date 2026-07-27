// lineage_edges read/write helpers (Lineage spine, Layers 2+8).
//
// The store stays policy-free: kind validation lives in internal/lineage;
// this file only moves LineageEdge rows in and out. The upsert is idempotent
// on the full (src, dst, edge_kind) identity — re-wiring the same fact is
// free, and the FIRST created_at is kept so the edge records when the link
// was first established, not last confirmed.
package store

import "context"

// LineageEdge is one directed edge in the research lineage graph.
type LineageEdge struct {
	SrcKind   string `json:"srcKind"`
	SrcID     string `json:"srcId"`
	DstKind   string `json:"dstKind"`
	DstID     string `json:"dstId"`
	EdgeKind  string `json:"edgeKind"`
	CreatedAt int64  `json:"createdAt"`
	MetaJSON  string `json:"metaJson,omitempty"`
}

// UpsertLineageEdge writes one edge idempotently. A repeat write keeps the
// original created_at and only refreshes meta_json when the new write carries
// one (so a later run with a known git rev can enrich an edge, but an empty
// meta never erases a recorded rev).
func (s *Store) UpsertLineageEdge(ctx context.Context, e LineageEdge) error {
	_, err := s.w.ExecContext(ctx, `
		INSERT INTO lineage_edges (src_kind, src_id, dst_kind, dst_id, edge_kind, created_at, meta_json)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(src_kind, src_id, dst_kind, dst_id, edge_kind) DO UPDATE SET
		  meta_json = CASE WHEN excluded.meta_json != '' THEN excluded.meta_json ELSE lineage_edges.meta_json END`,
		e.SrcKind, e.SrcID, e.DstKind, e.DstID, e.EdgeKind, e.CreatedAt, e.MetaJSON)
	return err
}

// LineageEdgesTouching returns every edge where (kind,id) appears as either
// endpoint — the one query lineage.Trace's walk needs per node.
func (s *Store) LineageEdgesTouching(ctx context.Context, kind, id string) ([]LineageEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT src_kind, src_id, dst_kind, dst_id, edge_kind, created_at, meta_json
		FROM lineage_edges
		WHERE (src_kind = ? AND src_id = ?) OR (dst_kind = ? AND dst_id = ?)
		ORDER BY created_at, edge_kind`, kind, id, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	out := make([]LineageEdge, 0, 4)
	for rows.Next() {
		var e LineageEdge
		if err := rows.Scan(&e.SrcKind, &e.SrcID, &e.DstKind, &e.DstID, &e.EdgeKind, &e.CreatedAt, &e.MetaJSON); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountLineageEdges returns the total edge count (tests + datastats).
func (s *Store) CountLineageEdges(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lineage_edges`).Scan(&n)
	return n, err
}
