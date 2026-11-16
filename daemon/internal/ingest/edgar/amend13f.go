package edgar

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Parse13FAmendmentType extracts the amendment type from a 13F primary document XML.
// The amendment type is found at formData>coverPage>amendmentInfo>amendmentType.
// Returns uppercase trimmed value, or empty string with nil error when absent.
func Parse13FAmendmentType(data []byte) (string, error) {
	type cover struct {
		AmendmentType string `xml:"formData>coverPage>amendmentInfo>amendmentType"`
	}
	var c cover
	if err := decodeXML(data, &c); err != nil {
		return "", fmt.Errorf("edgar: parse 13F amendment type: %w", err)
	}
	return strings.ToUpper(strings.TrimSpace(c.AmendmentType)), nil
}

// Fetch13FAmendmentType retrieves and parses the amendment type for a 13F/A filing.
func (c *FilingsClient) Fetch13FAmendmentType(ctx context.Context, cik int64, accession, primaryDoc string) (string, error) {
	if strings.TrimSpace(primaryDoc) == "" {
		return "", errors.New("edgar: 13F/A has no primary document")
	}
	body, err := c.get(ctx, c.FilingDocURL(cik, accession, RawXMLDoc(primaryDoc)))
	if err != nil {
		return "", err
	}
	return Parse13FAmendmentType(body)
}

// Part13F represents one 13F filing (original or amendment) for a single report period.
type Part13F struct {
	Amendment bool         // true if this filing is an amendment (13F/A)
	Type      string       // amendment type from cover page ("RESTATEMENT", "NEW HOLDINGS", etc.)
	Holdings  []Holding13F // holdings reported in this part
}

// Fold13F combines a sequence of 13F parts (oldest first) into a final holdings list.
// Original filings and RESTATEMENTs reset state; NEW HOLDINGS amendments add to state.
// Option lines (PutCall != "") and lines with empty CUSIP are skipped.
// Holdings with the same CUSIP are merged by summing Value and Shares.
// Returns holdings in first-seen CUSIP order, or nil if empty.
func Fold13F(parts []Part13F) []Holding13F {
	type entry struct {
		h *Holding13F
	}
	state := make(map[string]*entry)
	order := make([]string, 0)

	for _, p := range parts {
		if p.Amendment && p.Type == "NEW HOLDINGS" {
			// add to current state
		} else {
			// reset state
			state = make(map[string]*entry)
			order = order[:0]
		}

		for _, h := range p.Holdings {
			if h.PutCall != "" || strings.TrimSpace(h.CUSIP) == "" {
				continue
			}
			cusip := strings.TrimSpace(h.CUSIP)
			if e, ok := state[cusip]; ok {
				e.h.Value += h.Value
				e.h.Shares += h.Shares
			} else {
				hc := h // copy to avoid aliasing input
				state[cusip] = &entry{h: &hc}
				order = append(order, cusip)
			}
		}
	}

	if len(order) == 0 {
		return nil
	}
	out := make([]Holding13F, 0, len(order))
	for _, cusip := range order {
		out = append(out, *state[cusip].h)
	}
	return out
}
