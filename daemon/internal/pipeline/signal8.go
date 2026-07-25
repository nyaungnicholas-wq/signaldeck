// Signal8 wave — Stage 1 workers: SEC filings intelligence.
//
//   - filings-poller (2h): sweeps a rotating window of universe stocks through
//     the EDGAR submissions API into the filings feed (plain-English labels),
//     fetches + parses NEW Form 4 documents into insider_trades (bounded per
//     run), and re-derives dilution flags for the swept symbols.
//   - 13f-poller (24h): rotates through the curated notable-manager list
//     (hardcoded CIKs), storing each manager's LATEST 13F-HR information table
//     once per report period (skip-if-stored, so steady state is ~zero work
//     between quarters).
//
// Rate budget (all through the shared edgar limiter, 150ms min interval,
// descriptive UA, 429 backoff — well under SEC's 10 req/s):
//
//	filings-poller: ≤40 submissions + ≤25 Form 4 docs per 2h run ⇒ ≤65 req/run,
//	≤780/day. 13f-poller: ≤5 managers × ≤3 req ⇒ ≤15 req/day. Combined worst
//	case ≈ 800 requests/day, spaced ≥150ms apart.
//
// Honesty: everything stored is public-domain government data; the LAGS are
// legal/procedural (Form 4 ~2 business days; 13F quarterly + ≤45 days) and are
// surfaced in the API notes. Dilution levels are descriptive evidence, not
// predictions.
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/edgar"
	md "github.com/nyaungnicholas-wq/signaldeck/internal/marketdata"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// ── filings-poller ────────────────────────────────────────────────────────

// FilingsPoller sweeps universe stocks through the EDGAR submissions API.
type FilingsPoller struct {
	St     *store.Store
	Client *edgar.FilingsClient // nil ⇒ no-op (no stock universe to sweep)
	// MaxSymbols bounds submissions requests per run (0 ⇒ 40).
	MaxSymbols int
	// MaxForm4 bounds Form 4 document fetches per run (0 ⇒ 25).
	MaxForm4 int
	// Now is a test hook; nil = time.Now.
	Now func() time.Time
}

func (w *FilingsPoller) Name() string            { return "filings-poller" }
func (w *FilingsPoller) Interval() time.Duration { return 2 * time.Hour }

// filingsCursorKey rotates the sweep window across runs (same pattern as the
// edgar-fetcher's fundamentals cursor, separate key).
const filingsCursorKey = "filings_sweep_cursor"

// dilutionForms are the dilution-shaped filing families.
var dilutionForms = []string{"S-1", "S-3", "424B"}

// dilutionWindow is how far back a dilution-shaped filing counts (180 days).
const dilutionWindow = 180 * 24 * time.Hour

// sharesIncreaseThreshold: shares-outstanding growth beyond this fraction
// (within the comparison window) counts as dilution evidence.
const sharesIncreaseThreshold = 0.02

func (w *FilingsPoller) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *FilingsPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no EDGAR filings client", nil
	}
	all, err := w.St.ListSymbols(ctx, true)
	if err != nil {
		return "", err
	}
	stocks := make([]md.Symbol, 0, len(all))
	for _, s := range all {
		if s.Market == md.Stocks {
			stocks = append(stocks, s)
		}
	}
	if len(stocks) == 0 {
		return "no stocks to sweep", nil
	}

	cikMap, err := w.Client.CachedCIKMap(ctx, w.St)
	if err != nil {
		return "", err
	}

	batch := w.MaxSymbols
	if batch <= 0 {
		batch = 40
	}
	window := rotateWindow(ctx, w.St, stocks, batch, filingsCursorKey)

	now := w.now()
	inserted, form4Fetched, swept := 0, 0, 0
	for _, s := range window {
		cik, ok := cikMap[strings.ToUpper(s.Symbol)]
		if !ok {
			continue // not an SEC filer we can resolve (ETFs etc.) — honest skip
		}
		subs, prof, serr := w.Client.SubmissionsWithProfile(ctx, cik)
		if serr != nil {
			// One bad symbol never aborts the sweep; record and continue.
			id := s.ID
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				SymbolID: &id, Ts: now.Unix(), Kind: "filings_error",
				Detail: fmt.Sprintf("%s: %v", s.Symbol, serr),
			})
			continue
		}
		swept++
		// Companies-directory SIC enrichment (Stage 5): the submissions JSON we
		// JUST fetched carries the registrant's SIC industry classification —
		// store it at zero added request volume. When the directory has no row
		// for the CIK yet (companies-sync hasn't run, or the ticker is absent
		// from the exchange file) a minimal fallback row keeps the enrichment.
		if prof.SIC != "" {
			w.storeSIC(ctx, cik, s, prof, now.Unix())
		}
		for _, f := range subs {
			if !edgar.InterestingForm(f.Form) {
				continue
			}
			row := store.FilingRow{
				ID: f.Accession, SymbolID: s.ID, Form: f.Form, FiledTs: f.FiledTs,
				Title: f.PrimaryDesc,
				URL:   edgar.PublicFilingURL(cik, f.Accession, f.PrimaryDoc),
				Label: edgar.FormLabel(f.Form, f.Items),
			}
			if row.Title == "" {
				row.Title = f.Form
			}
			isNew, ierr := w.St.InsertFiling(ctx, row)
			if ierr != nil {
				return "", ierr
			}
			if isNew {
				inserted++
			}
			// New Form 4 → fetch + parse the ownershipDocument (bounded).
			maxF4 := w.MaxForm4
			if maxF4 <= 0 {
				maxF4 = 25
			}
			if isNew && f.Form == "4" && form4Fetched < maxF4 {
				form4Fetched++
				if perr := w.parseForm4(ctx, cik, s.ID, f); perr != nil {
					id := s.ID
					_ = w.St.InsertDQ(ctx, md.DQEvent{
						SymbolID: &id, Ts: now.Unix(), Kind: "form4_parse_error",
						Detail: fmt.Sprintf("%s %s: %v", s.Symbol, f.Accession, perr),
					})
				}
			}
		}
		// Re-derive this symbol's dilution flag from the fresh filing set +
		// stored shares-outstanding history.
		if derr := w.deriveDilution(ctx, s.ID, now); derr != nil {
			return "", derr
		}
	}
	// Backlog drain: Form 4s stored in EARLIER runs but never parsed (per-run
	// cap hit, or a transient fetch error) are retried here, inside the same
	// per-run budget, so nothing silently falls through the cracks.
	maxF4 := w.MaxForm4
	if maxF4 <= 0 {
		maxF4 = 25
	}
	if remaining := maxF4 - form4Fetched; remaining > 0 {
		backlog, berr := w.St.UnparsedForm4s(ctx, remaining)
		if berr != nil {
			return "", berr
		}
		for _, row := range backlog {
			cik, doc, ok := parsePublicFilingURL(row.URL)
			if !ok {
				continue
			}
			form4Fetched++
			if perr := w.parseForm4(ctx, cik, row.SymbolID,
				edgar.SubFiling{Accession: row.ID, PrimaryDoc: doc, FiledTs: row.FiledTs}); perr != nil {
				id := row.SymbolID
				_ = w.St.InsertDQ(ctx, md.DQEvent{
					SymbolID: &id, Ts: now.Unix(), Kind: "form4_parse_error",
					Detail: fmt.Sprintf("backlog %s: %v", row.ID, perr),
				})
			}
		}
	}
	return fmt.Sprintf("filings: %d new rows across %d/%d symbols (form4 parsed %d)",
		inserted, swept, len(window), form4Fetched), nil
}

// parsePublicFilingURL recovers (cik, primaryDoc) from a stored EDGAR archive
// link (…/Archives/edgar/data/{cik}/{accession-no-dashes}/{doc}) so the
// backlog drain can re-fetch a document without re-hitting the submissions
// API. ok=false on any unexpected shape (row is skipped, never guessed).
func parsePublicFilingURL(u string) (cik int64, doc string, ok bool) {
	const marker = "/Archives/edgar/data/"
	i := strings.Index(u, marker)
	if i < 0 {
		return 0, "", false
	}
	parts := strings.Split(u[i+len(marker):], "/")
	if len(parts) < 3 {
		return 0, "", false
	}
	var n int64
	if _, err := fmt.Sscanf(parts[0], "%d", &n); err != nil || n <= 0 {
		return 0, "", false
	}
	return n, strings.Join(parts[2:], "/"), true
}

// form4MaxAttempts caps genuine fetch/XML-error retries per accession. After
// this many failed attempts the filing is ABANDONED: a sentinel row marks it
// parsed-empty (so UnparsedForm4s stops re-serving it every run — each retry
// costs a real SEC request) and ONE dq event records the abandonment.
const form4MaxAttempts = 3

// form4AttemptsKey is the per-accession meta key holding the failure count.
func form4AttemptsKey(accession string) string { return "form4_attempts:" + accession }

// parseForm4 fetches one Form 4 XML, aggregates it, and stores the trade.
//
// TERMINATION CONTRACT (the backlog-drain loop depends on it): every path
// eventually leaves the accession OUT of the UnparsedForm4s set —
//   - parseable non-derivative transactions ⇒ the real insider_trades row;
//   - derivative-only filings and blank primary documents are SUCCESS-EMPTY,
//     not errors ⇒ a sentinel row (code=”) that reads exclude;
//   - genuine fetch/XML errors are retried up to form4MaxAttempts times, then
//     abandoned with the sentinel + one dq event.
//
// Without this, a permanently-unparseable Form 4 would be re-fetched every
// 2h forever, wasting the whole backlog budget on the same documents.
func (w *FilingsPoller) parseForm4(ctx context.Context, cik, symbolID int64, f edgar.SubFiling) error {
	if strings.TrimSpace(f.PrimaryDoc) == "" {
		// No primary document ⇒ nothing to fetch, ever. SUCCESS-EMPTY.
		return w.insertForm4Sentinel(ctx, symbolID, f, "", "")
	}
	parsed, err := w.Client.FetchForm4(ctx, cik, f.Accession, f.PrimaryDoc)
	if err != nil {
		return w.form4Failure(ctx, symbolID, f, err)
	}
	code, shares, price, value, txTs, ok := edgar.AggregateForm4(parsed)
	if !ok {
		// Derivative-only Form 4 (options/RSUs, no non-derivative table):
		// a legitimate, fully-parsed filing with nothing to show in the
		// insider-trades feed. SUCCESS-EMPTY, not an error.
		return w.insertForm4Sentinel(ctx, symbolID, f, parsed.Insider, parsed.Title)
	}
	return w.St.InsertInsiderTrade(ctx, store.InsiderTradeRow{
		Accession: f.Accession, SymbolID: symbolID,
		Insider: parsed.Insider, Title: parsed.Title,
		Code: code, Shares: shares, Price: price, Value: value,
		TxTs: txTs, FiledTs: f.FiledTs,
	})
}

// insertForm4Sentinel records the parsed-empty marker row: code=” shares=0
// value=0. store.InsiderTrades and the API exclude code=” rows, but the row's
// presence removes the accession from the UnparsedForm4s backlog for good.
func (w *FilingsPoller) insertForm4Sentinel(ctx context.Context, symbolID int64, f edgar.SubFiling, insider, title string) error {
	return w.St.InsertInsiderTrade(ctx, store.InsiderTradeRow{
		Accession: f.Accession, SymbolID: symbolID,
		Insider: insider, Title: title,
		Code: "", Shares: 0, Price: 0, Value: 0,
		TxTs: 0, FiledTs: f.FiledTs,
	})
}

// form4Failure counts a genuine fetch/parse failure against the per-accession
// attempt cap. Below the cap it returns the error (the caller records the
// usual form4_parse_error dq event and the backlog retries next run). At the
// cap it abandons: sentinel row + ONE form4_abandoned dq event, nil error.
func (w *FilingsPoller) form4Failure(ctx context.Context, symbolID int64, f edgar.SubFiling, ferr error) error {
	key := form4AttemptsKey(f.Accession)
	attempts := 0
	if v, _ := w.St.GetMeta(ctx, key); v != "" {
		attempts, _ = strconv.Atoi(v)
	}
	attempts++
	_ = w.St.SetMeta(ctx, key, strconv.Itoa(attempts))
	if attempts < form4MaxAttempts {
		return ferr
	}
	if err := w.insertForm4Sentinel(ctx, symbolID, f, "", ""); err != nil {
		return err
	}
	id := symbolID
	_ = w.St.InsertDQ(ctx, md.DQEvent{
		SymbolID: &id, Ts: w.now().Unix(), Kind: "form4_abandoned",
		Detail: fmt.Sprintf("%s: abandoned after %d failed attempts (sentinel stored): %v",
			f.Accession, attempts, ferr),
	})
	return nil
}

// storeSIC persists a swept symbol's SIC classification into the companies
// directory. Best-effort: an update failure is a dq event, never a sweep
// abort — the classification re-arrives on the next rotation anyway.
func (w *FilingsPoller) storeSIC(ctx context.Context, cik int64, s md.Symbol, prof edgar.CompanyProfile, ts int64) {
	n, err := w.St.UpdateCompanySICByCIK(ctx, cik, prof.SIC, prof.SICDesc, ts)
	if err == nil && n == 0 {
		// No directory row for this CIK yet — insert a minimal one keyed by the
		// swept ticker (name from EDGAR when present, else our symbol record;
		// exchange unknown here and left '' — the daily sync fills it in).
		name := prof.Name
		if name == "" {
			name = s.Name
		}
		err = w.St.UpsertCompanies(ctx, []store.CompanyRow{{
			CIK: cik, Ticker: strings.ToUpper(s.Symbol), Name: name,
			SIC: prof.SIC, SICDesc: prof.SICDesc, UpdatedTs: ts,
		}})
	}
	if err != nil {
		id := s.ID
		_ = w.St.InsertDQ(ctx, md.DQEvent{
			SymbolID: &id, Ts: ts, Kind: "companies_sic_error",
			Detail: fmt.Sprintf("%s: %v", s.Symbol, err),
		})
	}
}

// deriveDilution recomputes one symbol's dilution flag.
func (w *FilingsPoller) deriveDilution(ctx context.Context, symbolID int64, now time.Time) error {
	since := now.Add(-dilutionWindow).Unix()
	filings, err := w.St.FilingsSince(ctx, symbolID, dilutionForms, since)
	if err != nil {
		return err
	}
	hist, err := w.St.FundamentalHistory(ctx, symbolID, "SharesOutstanding")
	if err != nil {
		return err
	}
	level, reasons := DeriveDilution(filings, hist, now.Unix())
	raw, _ := json.Marshal(reasons)
	return w.St.UpsertDilutionFlag(ctx, store.DilutionFlagRow{
		SymbolID: symbolID, Level: level, Reasons: string(raw), UpdatedTs: now.Unix(),
	})
}

// DeriveDilution is the PURE dilution rule (descriptive, not predictive):
//   - evidence A: any S-1/S-3/424B filing within the last 180 days;
//   - evidence B: shares outstanding up more than 2% between the earliest and
//     latest observation inside a ~400-day trailing window (two observations
//     minimum — one data point proves nothing).
//
// high = A AND B; elevated = exactly one; low = neither. reasons lists the
// concrete evidence (empty for low).
func DeriveDilution(recent []store.FilingRow, sharesHist []store.FundamentalRow, nowTs int64) (string, []string) {
	reasons := []string{}

	// Evidence A — dilution-shaped filings in the window.
	if len(recent) > 0 {
		forms := make([]string, 0, len(recent))
		seen := map[string]bool{}
		for _, f := range recent {
			if !seen[f.Form] {
				seen[f.Form] = true
				forms = append(forms, f.Form)
			}
		}
		reasons = append(reasons, fmt.Sprintf("%d dilution-shaped filing(s) in last 180d: %s",
			len(recent), strings.Join(forms, ", ")))
	}

	// Evidence B — shares outstanding growth inside a ~400d trailing window.
	const lookback = int64(400 * 24 * 3600)
	var inWin []store.FundamentalRow
	for _, r := range sharesHist { // history is oldest-first
		if r.AsOf >= nowTs-lookback && r.AsOf <= nowTs {
			inWin = append(inWin, r)
		}
	}
	if len(inWin) >= 2 {
		sort.Slice(inWin, func(i, j int) bool { return inWin[i].AsOf < inWin[j].AsOf })
		first, last := inWin[0], inWin[len(inWin)-1]
		if first.Value > 0 {
			growth := last.Value/first.Value - 1
			if growth > sharesIncreaseThreshold {
				reasons = append(reasons, fmt.Sprintf(
					"shares outstanding +%.1f%% over the trailing window (%.3gB → %.3gB)",
					growth*100, first.Value/1e9, last.Value/1e9))
			}
		}
	}

	switch len(reasons) {
	case 0:
		return "low", reasons
	case 1:
		return "elevated", reasons
	default:
		return "high", reasons
	}
}

// rotateWindow is the shared sweep-window rotation (alphabetical cursor in
// meta under key), generalized from the edgar-fetcher's selectWindow so each
// worker keeps its OWN cursor.
func rotateWindow(ctx context.Context, st *store.Store, stocks []md.Symbol, batch int, key string) []md.Symbol {
	sortSymbols(stocks)
	if len(stocks) <= batch {
		return stocks
	}
	cursor, _ := st.GetMeta(ctx, key)
	start := 0
	if cursor != "" {
		for i, s := range stocks {
			if s.Symbol > cursor {
				start = i
				break
			}
		}
	}
	end := start + batch
	var window []md.Symbol
	if end <= len(stocks) {
		window = stocks[start:end]
	} else {
		window = append(window, stocks[start:]...)
		window = append(window, stocks[:end-len(stocks)]...)
	}
	if len(window) > 0 {
		_ = st.SetMeta(ctx, key, window[len(window)-1].Symbol)
	}
	return window
}

// ── 13f-poller ────────────────────────────────────────────────────────────

// ThirteenFPoller stores the latest 13F-HR of each curated manager.
type ThirteenFPoller struct {
	St     *store.Store
	Client *edgar.FilingsClient // nil ⇒ no-op
	// Managers overrides the curated list (tests); empty ⇒ edgar.NotableManagers.
	Managers []edgar.Manager
	// PerRun bounds how many managers one run checks (0 ⇒ 5) — the list is
	// rotated via a persisted cursor, so all managers are covered over days
	// while each run stays tiny.
	PerRun int
}

func (w *ThirteenFPoller) Name() string            { return "13f-poller" }
func (w *ThirteenFPoller) Interval() time.Duration { return 24 * time.Hour }

const thirteenFCursorKey = "thirteenf_mgr_cursor"

func (w *ThirteenFPoller) Run(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "skipped: no EDGAR filings client", nil
	}
	managers := w.Managers
	if len(managers) == 0 {
		managers = edgar.NotableManagers
	}
	perRun := w.PerRun
	if perRun <= 0 {
		perRun = 5
	}
	if perRun > len(managers) {
		perRun = len(managers)
	}

	// Rotating start index (persisted) so every manager is visited over time.
	start := 0
	if v, _ := w.St.GetMeta(ctx, thirteenFCursorKey); v != "" {
		fmt.Sscanf(v, "%d", &start) //nolint:errcheck
		if start < 0 || start >= len(managers) {
			start = 0
		}
	}

	// Symbols once per run for best-effort issuer-name matching.
	all, err := w.St.ListSymbols(ctx, false)
	if err != nil {
		return "", err
	}
	var stocks []md.Symbol
	for _, s := range all {
		if s.Market == md.Stocks {
			stocks = append(stocks, s)
		}
	}

	stored, skipped, matched := 0, 0, 0
	for i := 0; i < perRun; i++ {
		m := managers[(start+i)%len(managers)]
		subs, serr := w.Client.Submissions(ctx, m.CIK)
		if serr != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: time.Now().Unix(), Kind: "13f_error",
				Detail: fmt.Sprintf("%s (CIK %d): %v", m.Name, m.CIK, serr),
			})
			continue
		}
		// Latest 13F-HR (subs are newest-first).
		var latest *edgar.SubFiling
		for j := range subs {
			if strings.TrimSuffix(subs[j].Form, "/A") == "13F-HR" {
				latest = &subs[j]
				break
			}
		}
		if latest == nil {
			skipped++
			continue
		}
		period := latest.ReportDate
		if period == "" {
			skipped++
			continue
		}
		if has, herr := w.St.HasInstPeriod(ctx, fmt.Sprint(m.CIK), period); herr != nil {
			return "", herr
		} else if has {
			skipped++ // this quarter already stored — steady state
			continue
		}
		holdings, ferr := w.Client.Fetch13FHoldings(ctx, m.CIK, latest.Accession)
		if ferr != nil {
			_ = w.St.InsertDQ(ctx, md.DQEvent{
				Ts: time.Now().Unix(), Kind: "13f_parse_error",
				Detail: fmt.Sprintf("%s %s: %v", m.Name, latest.Accession, ferr),
			})
			continue
		}
		for _, h := range holdings {
			row := store.InstHoldingRow{
				CIK: fmt.Sprint(m.CIK), Manager: m.Name, Period: period,
				CUSIP: h.CUSIP, Name: h.Issuer, Value: h.Value, Shares: h.Shares,
			}
			if sid, ok := MatchSymbolByName(h.Issuer, stocks); ok {
				row.SymbolID = &sid
				matched++
			}
			if uerr := w.St.UpsertInstHolding(ctx, row); uerr != nil {
				return "", uerr
			}
			stored++
		}
	}
	_ = w.St.SetMeta(ctx, thirteenFCursorKey, fmt.Sprint((start+perRun)%len(managers)))
	return fmt.Sprintf("13F: %d positions stored (%d symbol-matched), %d manager(s) already current/absent",
		stored, matched, skipped), nil
}

// MatchSymbolByName best-effort matches a 13F issuer name to a tracked stock.
// HONEST: there is no free authoritative CUSIP→ticker map, so this is a
// conservative normalized-name comparison — exact normalized equality, or one
// side being the other plus corporate suffixes. Unmatched issuers stay NULL.
func MatchSymbolByName(issuer string, syms []md.Symbol) (int64, bool) {
	in := normalizeIssuer(issuer)
	if in == "" {
		return 0, false
	}
	for _, s := range syms {
		sn := normalizeIssuer(s.Name)
		if sn == "" {
			continue
		}
		if in == sn {
			return s.ID, true
		}
	}
	return 0, false
}

// issuerNoise are corporate suffixes stripped before comparison.
var issuerNoise = []string{
	"INCORPORATED", "CORPORATION", "COMPANY", "HOLDINGS", "GROUP",
	"INC", "CORP", "LTD", "PLC", "CO", "SA", "NV", "AG", "LP", "LLC",
	"CL A", "CL B", "CL C", "CLASS A", "CLASS B", "CLASS C",
	"COM", "NEW", "DEL",
}

// normalizeIssuer uppercases, strips punctuation and corporate suffixes.
func normalizeIssuer(s string) string {
	up := strings.ToUpper(strings.TrimSpace(s))
	up = strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ':
			return r
		}
		return ' '
	}, up)
	words := strings.Fields(up)
	// Strip trailing noise words (repeatedly: "APPLE INC COM" → "APPLE").
	for len(words) > 1 {
		lastOne := words[len(words)-1]
		lastTwo := ""
		if len(words) > 2 {
			lastTwo = words[len(words)-2] + " " + lastOne
		}
		stripped := false
		for _, n := range issuerNoise {
			if lastTwo == n {
				words = words[:len(words)-2]
				stripped = true
				break
			}
			if lastOne == n {
				words = words[:len(words)-1]
				stripped = true
				break
			}
		}
		if !stripped {
			break
		}
	}
	return strings.Join(words, " ")
}
