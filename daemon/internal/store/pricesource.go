package store

import (
	"context"
	"fmt"
)

// PriceSourceMetaKey marks a database whose bars come from one named provider.
// cmd/iexhist-load writes "iexhist"; the production database carries no mark.
const PriceSourceMetaKey = "price_source"

// CheckPriceSource refuses a database that does not match the price source the
// daemon is configured to show publicly (SIGNALDECK_PUBLIC_PRICE_SOURCE).
//
// The licence gate opens price-derived routes on the word of that setting. It
// cannot see where the stored bars came from, so the setting and the database
// have to be checked against each other here, before anything is served:
//
//   - want names a displayable source (e.g. "iexhist"): the database must be
//     marked with exactly that source and hold no intraday bars (IEX history
//     writes daily bars only). Pointing such a daemon at the Alpaca database by
//     mistake would otherwise publish all of it.
//   - want is "" or "alpaca": the database must NOT be marked as another
//     source's, or Alpaca bars would be ingested into it.
func (s *Store) CheckPriceSource(ctx context.Context, want string) error {
	mark, err := s.GetMeta(ctx, PriceSourceMetaKey)
	if err != nil {
		return fmt.Errorf("read %s: %w", PriceSourceMetaKey, err)
	}
	if want == "" || want == "alpaca" {
		if mark != "" && mark != "alpaca" {
			return fmt.Errorf("this database is marked %s=%s; a daemon without a matching "+
				"SIGNALDECK_PUBLIC_PRICE_SOURCE must not write another provider's bars into it", PriceSourceMetaKey, mark)
		}
		return nil
	}
	if mark != want {
		return fmt.Errorf("SIGNALDECK_PUBLIC_PRICE_SOURCE=%s but this database is marked %s=%q: "+
			"refusing to show a database that was not built from that source alone", want, PriceSourceMetaKey, mark)
	}
	var intraday int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM bars WHERE tf != '1d' LIMIT 1)`).Scan(&intraday); err != nil {
		return fmt.Errorf("scan bars: %w", err)
	}
	if intraday > 0 {
		return fmt.Errorf("SIGNALDECK_PUBLIC_PRICE_SOURCE=%s but this database holds intraday bars, "+
			"which that source never writes: it is not a single-source database", want)
	}
	return nil
}
