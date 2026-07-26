// Persistence adapters: Claim ↔ store rows. The store stays judgement-free
// (see store/evidence.go); Put is the ONLY write path for whole claims and it
// validates first, so an unjustified tier can never reach the database.
package evidence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// Put validates and persists a claim (insert or replace by id).
func Put(ctx context.Context, st *store.Store, c Claim) error {
	if err := c.Validate(); err != nil {
		return err
	}
	row, err := toRow(c)
	if err != nil {
		return err
	}
	return st.PutEvidenceClaim(ctx, row)
}

// Get returns one claim by id.
func Get(ctx context.Context, st *store.Store, id string) (Claim, error) {
	row, err := st.EvidenceClaim(ctx, id)
	if err != nil {
		return Claim{}, err
	}
	return fromRow(row)
}

// List returns claims, optionally filtered by status and/or lineage feature.
func List(ctx context.Context, st *store.Store, status, feature string) ([]Claim, error) {
	rows, err := st.EvidenceClaims(ctx, status, feature)
	if err != nil {
		return nil, err
	}
	out := make([]Claim, 0, len(rows))
	for _, r := range rows {
		c, err := fromRow(r)
		if err != nil {
			return nil, fmt.Errorf("claim %s: %w", r.ID, err)
		}
		out = append(out, c)
	}
	return out, nil
}

func toRow(c Claim) (store.EvidenceClaimRow, error) {
	scope, err := json.Marshal(c.Scope)
	if err != nil {
		return store.EvidenceClaimRow{}, err
	}
	lineage, err := json.Marshal(c.Lineage)
	if err != nil {
		return store.EvidenceClaimRow{}, err
	}
	row := store.EvidenceClaimRow{
		ID:            c.ID,
		Text:          c.Text,
		ScopeJSON:     string(scope),
		Tier:          string(c.Tier),
		Status:        string(c.Status),
		LastValidated: c.LastValidated,
		RevalidateBy:  c.RevalidateBy,
		LineageJSON:   string(lineage),
		Seeded:        c.Seeded,
	}
	for _, it := range c.Items {
		row.Items = append(row.Items, store.EvidenceItemRow{
			Kind:       it.Kind,
			Value:      it.Value,
			NEffective: it.NEffective,
			Method:     it.Method,
			Correction: it.Correction,
			CILow:      it.CILow,
			CIHigh:     it.CIHigh,
			Baseline:   it.Baseline,
			SourceRef:  it.SourceRef,
		})
	}
	return row, nil
}

func fromRow(r store.EvidenceClaimRow) (Claim, error) {
	c := Claim{
		ID:            r.ID,
		Text:          r.Text,
		Tier:          Tier(r.Tier),
		Status:        Status(r.Status),
		LastValidated: r.LastValidated,
		RevalidateBy:  r.RevalidateBy,
		Seeded:        r.Seeded,
	}
	if err := json.Unmarshal([]byte(r.ScopeJSON), &c.Scope); err != nil {
		return Claim{}, fmt.Errorf("scope: %w", err)
	}
	if err := json.Unmarshal([]byte(r.LineageJSON), &c.Lineage); err != nil {
		return Claim{}, fmt.Errorf("lineage: %w", err)
	}
	for _, it := range r.Items {
		c.Items = append(c.Items, Item{
			Kind:       it.Kind,
			Value:      it.Value,
			NEffective: it.NEffective,
			Method:     it.Method,
			Correction: it.Correction,
			CILow:      it.CILow,
			CIHigh:     it.CIHigh,
			Baseline:   it.Baseline,
			SourceRef:  it.SourceRef,
		})
	}
	return c, nil
}
