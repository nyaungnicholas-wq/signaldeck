package congress

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	// DefaultKadoaURL is the default endpoint for the Kadoa Congress trading monitor.
	DefaultKadoaURL = "https://www.kadoa.com/congress/data/trades.json"
	// KadoaURLEnv is the environment variable that can override the Kadoa URL.
	// Setting it to "off" disables the fallback.
	KadoaURLEnv = "SIGNALDECK_CONGRESS_KADOA_URL"
)

type kadoaRow struct {
	ID              string  `json:"id"`
	SourceID        string  `json:"source_id"`
	TransactionDate string  `json:"transaction_date"`
	FilingDate      string  `json:"filing_date"`
	Owner           *string `json:"owner"` // can be null
	Ticker          string  `json:"ticker"`
	AssetName       string  `json:"asset_name"`
	TransactionType string  `json:"transaction_type"`
	AmountLabel     string  `json:"amount_range_label"`
	FilerName       string  `json:"filer_name"`
	Chamber         string  `json:"chamber"`
}

// ParseKadoa converts the raw Kadoa JSON dump into []Trade.
// It wraps any JSON unmarshal error with context.
func ParseKadoa(data []byte) ([]Trade, error) {
	var rows []kadoaRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("congress: parse kadoa dump: %w", err)
	}
	var trades []Trade
	for _, r := range rows {
		var chamber string
		switch r.SourceID {
		case "senate_efd":
			chamber = ChamberSenate
		case "house_clerk":
			chamber = ChamberHouse
		default:
			continue // skip OGE/executive rows
		}
		ticker := SanitizeTicker(r.Ticker)
		if ticker == "" {
			continue
		}
		owner := ""
		if r.Owner != nil {
			owner = *r.Owner
		}
		txTs, _ := parseISODate(r.TransactionDate)
		disclosedTs, _ := parseISODate(r.FilingDate)
		trade := Trade{
			Chamber:     chamber,
			Member:      strings.TrimSpace(r.FilerName),
			Ticker:      ticker,
			Asset:       strings.TrimSpace(r.AssetName),
			TxType:      NormalizeTxType(r.TransactionType),
			Amount:      strings.TrimSpace(r.AmountLabel),
			TxTs:        txTs,
			DisclosedTs: disclosedTs,
		}
		trade.ID = TradeID(chamber, trade.Member, ticker, trade.TxType, trade.Amount, r.TransactionDate, r.FilingDate, owner)
		trades = append(trades, trade)
	}
	return trades, nil
}

// kadoaURL returns the Kadoa endpoint to use, honoring the override
// via $SIGNALDECK_CONGRESS_KADOA_URL. An empty value falls back to the default.
func (c *Client) kadoaURL() string {
	if v := os.Getenv(KadoaURLEnv); v != "" {
		return v
	}
	return DefaultKadoaURL
}

// FetchKadoa returns trades from the Kadoa fallback source.
// It is a volunteer‑maintained, keyless JSON dump refreshed daily.
// Only the newest ~5,000 rows are available; older history must be
// persisted by the caller. The chamber argument filters the result.
func (c *Client) FetchKadoa(ctx context.Context, chamber string) ([]Trade, error) {
	u := c.kadoaURL()
	if strings.EqualFold(u, "off") {
		return nil, fmt.Errorf("congress: kadoa fallback disabled (%s=off)", KadoaURLEnv)
	}
	data, err := c.get(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("congress: kadoa fetch: %w", err)
	}
	trades, err := ParseKadoa(data)
	if err != nil {
		return nil, err
	}
	var filtered []Trade
	for _, t := range trades {
		if strings.EqualFold(t.Chamber, chamber) {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}
