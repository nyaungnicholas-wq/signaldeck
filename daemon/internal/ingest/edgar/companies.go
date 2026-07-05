// Signal8 wave — Stage 5: the COMPANIES DIRECTORY source. One more free SEC
// file on the SAME rate-limited client (shared pace()/get() limiter, same
// descriptive UA, same 429/503 backoff):
//
//	https://www.sec.gov/files/company_tickers_exchange.json
//
// Shape (verified live 2026-07-04, ~10.4k rows):
//
//	{"fields":["cik","name","ticker","exchange"],
//	 "data":[[1045810,"NVIDIA CORP","NVDA","Nasdaq"], ...]}
//
// exchange is one of Nasdaq/NYSE/OTC/CBOE or null (~189 registrants) — null
// is stored as '' and rendered "—", never guessed. The whole directory is ONE
// request; the companies-sync worker refreshes it every 24h.
package edgar

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// exchangeTickersURL is the production companies-directory file.
const exchangeTickersURL = "https://www.sec.gov/files/company_tickers_exchange.json"

func (c *Client) exchangeEndpoint() string {
	if c.ExchangeURL != "" {
		return c.ExchangeURL
	}
	return exchangeTickersURL
}

// exchangeFileResp is the columnar shape of company_tickers_exchange.json.
type exchangeFileResp struct {
	Fields []string          `json:"fields"`
	Data   []json.RawMessage `json:"data"`
}

// ParseCompanyTickersExchange parses the company_tickers_exchange.json body
// into directory rows. Pure function (fixture-tested). Defensive on shape:
// column order is resolved from the "fields" array rather than assumed, a
// null exchange becomes '' (honest absence), tickers are uppercased, and rows
// missing a ticker or CIK are skipped. updatedTs is stamped by the caller.
func ParseCompanyTickersExchange(body []byte) ([]store.CompanyRow, error) {
	var resp exchangeFileResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("edgar: parse company_tickers_exchange.json: %w", err)
	}
	idx := map[string]int{}
	for i, f := range resp.Fields {
		idx[strings.ToLower(strings.TrimSpace(f))] = i
	}
	for _, need := range []string{"cik", "name", "ticker", "exchange"} {
		if _, ok := idx[need]; !ok {
			return nil, fmt.Errorf("edgar: company_tickers_exchange.json missing field %q (fields=%v)", need, resp.Fields)
		}
	}
	out := make([]store.CompanyRow, 0, len(resp.Data))
	seen := make(map[string]bool, len(resp.Data))
	for _, raw := range resp.Data {
		var row []any
		if err := json.Unmarshal(raw, &row); err != nil {
			continue // one malformed row never aborts the 10k-row map
		}
		at := func(field string) any {
			i := idx[field]
			if i < len(row) {
				return row[i]
			}
			return nil
		}
		cik, _ := at("cik").(float64)
		name, _ := at("name").(string)
		ticker, _ := at("ticker").(string)
		exchange, _ := at("exchange").(string) // nil (JSON null) → '' via failed assert
		ticker = strings.ToUpper(strings.TrimSpace(ticker))
		if ticker == "" || cik <= 0 || seen[ticker] {
			// seen: keep the FIRST occurrence should the file ever duplicate a
			// ticker (it is ticker-unique today; first row = primary listing).
			continue
		}
		seen[ticker] = true
		out = append(out, store.CompanyRow{
			CIK:      int64(cik),
			Ticker:   ticker,
			Name:     strings.TrimSpace(name),
			Exchange: strings.TrimSpace(exchange),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("edgar: company_tickers_exchange.json parsed to zero rows")
	}
	return out, nil
}

// CompanyTickersExchange fetches and parses the full SEC company directory —
// ONE paced request through the shared limiter.
func (c *Client) CompanyTickersExchange(ctx context.Context) ([]store.CompanyRow, error) {
	body, err := c.get(ctx, c.exchangeEndpoint())
	if err != nil {
		return nil, err
	}
	return ParseCompanyTickersExchange(body)
}
