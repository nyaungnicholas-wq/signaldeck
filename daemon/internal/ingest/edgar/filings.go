// Signal8 wave — Stage 1: SEC FILINGS INTELLIGENCE client extensions.
// This file extends the edgar package (same rate limiter, same descriptive
// User-Agent, same CIK map) with:
//   - the EDGAR submissions API (per-company recent filings feed),
//   - a plain-English form-type → label map (with 8-K item refinement),
//   - Form 4 (ownershipDocument XML) fetch + parse for insider trades,
//   - 13F-HR information-table discovery (index.json) + parse,
//   - the curated list of ~25 notable 13F managers (hardcoded CIKs).
//
// Everything here is public-domain US government data. Honesty: filings LAG
// by law/process (Form 4 ~2 business days after the trade; 13F quarterly with
// up to a 45-day lag) — callers surface those notes to the user.
package edgar

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

const (
	submissionsBaseURL = "https://data.sec.gov/submissions"
	archivesBaseURL    = "https://www.sec.gov/Archives/edgar/data"
)

// FilingsClient layers the submissions/archives endpoints on the shared
// rate-limited Client. Zero-value bases fall back to production hosts; tests
// point them at an httptest server. Because it EMBEDS *Client, every request
// flows through the same pace()/get() limiter + UA + 429 backoff.
type FilingsClient struct {
	*Client
	SubmissionsBase string
	ArchivesBase    string
}

// NewFilings returns a FilingsClient wired to SEC production hosts.
func NewFilings() *FilingsClient { return &FilingsClient{Client: New()} }

func (c *FilingsClient) submissionsBase() string {
	if c.SubmissionsBase != "" {
		return strings.TrimSuffix(c.SubmissionsBase, "/")
	}
	return submissionsBaseURL
}

func (c *FilingsClient) archivesBase() string {
	if c.ArchivesBase != "" {
		return strings.TrimSuffix(c.ArchivesBase, "/")
	}
	return archivesBaseURL
}

// CachedCIKMap exposes the shared, meta-cached ticker→CIK map to workers
// outside this package (the filings poller reuses the fundamentals fetcher's
// daily cache instead of re-pulling the large file).
func (c *Client) CachedCIKMap(ctx context.Context, st *store.Store) (map[string]int64, error) {
	return c.cachedCIKMap(ctx, st)
}

// ── submissions API ───────────────────────────────────────────────────────

// SubFiling is one row of a company's recent-filings list.
type SubFiling struct {
	Accession  string // e.g. 0000320193-26-000005
	Form       string // raw form type ("4", "8-K", "S-3", …)
	FiledTs    int64  // acceptance time when parseable, else filing date (unix s)
	ReportDate string // period of report (YYYY-MM-DD; 13F quarter end)
	Items      string // 8-K item codes ("2.02,9.01")
	PrimaryDoc string // primary document path within the filing folder
	PrimaryDesc string
}

// submissionsResp is the (partial) shape of CIK##########.json we consume.
// sic/sicDescription feed the companies-directory SIC enrichment: the
// filings-poller ALREADY fetches this document per swept symbol, so the
// industry classification rides along at zero added request volume.
type submissionsResp struct {
	CIK     json.RawMessage `json:"cik"` // string or number depending on file
	Name    string          `json:"name"`
	SIC     string          `json:"sic"`
	SICDesc string          `json:"sicDescription"`
	Filings struct {
		Recent struct {
			AccessionNumber    []string `json:"accessionNumber"`
			FilingDate         []string `json:"filingDate"`
			AcceptanceDateTime []string `json:"acceptanceDateTime"`
			ReportDate         []string `json:"reportDate"`
			Form               []string `json:"form"`
			Items              []string `json:"items"`
			PrimaryDocument    []string `json:"primaryDocument"`
			PrimaryDocDescription []string `json:"primaryDocDescription"`
		} `json:"recent"`
	} `json:"filings"`
}

// CompanyProfile is the company-level header of a submissions response —
// the registrant name and its SIC industry classification (the directory's
// "sector" column). Fields may be "" when EDGAR has none (honest absence).
type CompanyProfile struct {
	Name    string
	SIC     string
	SICDesc string
}

// Submissions fetches a company's recent filings (newest first, as EDGAR
// returns them). The columnar JSON is zipped into row structs; rows with a
// blank accession or form are skipped.
func (c *FilingsClient) Submissions(ctx context.Context, cik int64) ([]SubFiling, error) {
	subs, _, err := c.SubmissionsWithProfile(ctx, cik)
	return subs, err
}

// SubmissionsWithProfile is Submissions plus the company profile header
// (name + SIC), parsed from the SAME response — the filings-poller uses this
// so SIC enrichment costs zero extra requests.
func (c *FilingsClient) SubmissionsWithProfile(ctx context.Context, cik int64) ([]SubFiling, CompanyProfile, error) {
	u := fmt.Sprintf("%s/%s.json", c.submissionsBase(), CIKPadded(cik))
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, CompanyProfile{}, err
	}
	var resp submissionsResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, CompanyProfile{}, fmt.Errorf("edgar: parse submissions CIK %d: %w", cik, err)
	}
	prof := CompanyProfile{
		Name:    strings.TrimSpace(resp.Name),
		SIC:     strings.TrimSpace(resp.SIC),
		SICDesc: strings.TrimSpace(resp.SICDesc),
	}
	rec := resp.Filings.Recent
	at := func(s []string, i int) string {
		if i < len(s) {
			return s[i]
		}
		return ""
	}
	out := make([]SubFiling, 0, len(rec.AccessionNumber))
	for i := range rec.AccessionNumber {
		f := SubFiling{
			Accession:   at(rec.AccessionNumber, i),
			Form:        strings.TrimSpace(at(rec.Form, i)),
			ReportDate:  at(rec.ReportDate, i),
			Items:       at(rec.Items, i),
			PrimaryDoc:  at(rec.PrimaryDocument, i),
			PrimaryDesc: at(rec.PrimaryDocDescription, i),
		}
		if f.Accession == "" || f.Form == "" {
			continue
		}
		// Acceptance time is the precise moment EDGAR accepted the filing;
		// fall back to the (date-only) filing date.
		if ts, ok := parseAcceptance(at(rec.AcceptanceDateTime, i)); ok {
			f.FiledTs = ts
		} else if ts, ok := parseDay(at(rec.FilingDate, i)); ok {
			f.FiledTs = ts
		}
		out = append(out, f)
	}
	return out, prof, nil
}

// parseAcceptance parses EDGAR's acceptanceDateTime (RFC3339-ish, Z-suffixed).
func parseAcceptance(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

// FilingIndexURL / FilingDocURL build EDGAR archive links. The folder name is
// the accession number without dashes.
func (c *FilingsClient) FilingIndexURL(cik int64, accession string) string {
	return fmt.Sprintf("%s/%d/%s/index.json", c.archivesBase(), cik, strings.ReplaceAll(accession, "-", ""))
}

func (c *FilingsClient) FilingDocURL(cik int64, accession, doc string) string {
	return fmt.Sprintf("%s/%d/%s/%s", c.archivesBase(), cik, strings.ReplaceAll(accession, "-", ""), doc)
}

// PublicFilingURL is the browser-facing link stored in the filings feed —
// production host regardless of test overrides, because it's FOR the user.
func PublicFilingURL(cik int64, accession, doc string) string {
	return fmt.Sprintf("%s/%d/%s/%s", archivesBaseURL, cik, strings.ReplaceAll(accession, "-", ""), doc)
}

// RawXMLDoc strips the inline-XSL viewer prefix EDGAR puts on Form 3/4/5
// primary documents ("xslF345X05/wk-form4.xml" → "wk-form4.xml"), yielding the
// raw XML path the parser needs.
func RawXMLDoc(primaryDoc string) string {
	if i := strings.LastIndex(primaryDoc, "/"); i >= 0 {
		return primaryDoc[i+1:]
	}
	return primaryDoc
}

// ── plain-English form labels ─────────────────────────────────────────────

// eightKItems maps 8-K item codes to short human phrases.
var eightKItems = map[string]string{
	"1.01": "material agreement",
	"1.02": "agreement terminated",
	"1.03": "bankruptcy",
	"2.01": "acquisition or disposition completed",
	"2.02": "earnings release",
	"2.03": "new debt obligation",
	"2.05": "restructuring costs",
	"2.06": "material impairment",
	"3.01": "listing non-compliance",
	"3.02": "unregistered equity sale (dilution)",
	"4.01": "auditor change",
	"4.02": "financials no longer reliable (restatement)",
	"5.01": "change of control",
	"5.02": "officer/director change",
	"5.03": "charter/bylaws change",
	"5.07": "shareholder vote results",
	"7.01": "Reg FD disclosure",
	"8.01": "other material event",
}

// FormLabel maps a raw SEC form type (plus 8-K item codes when present) to a
// plain-English label, signal8-style. Amendments ("/A") are marked. Unknown
// forms fall back to "<form> — SEC filing" — honest, never invented.
func FormLabel(form, items string) string {
	f := strings.ToUpper(strings.TrimSpace(form))
	amended := strings.HasSuffix(f, "/A")
	base := strings.TrimSuffix(f, "/A")

	label := ""
	switch {
	case base == "3":
		label = "Form 3 — new insider registered"
	case base == "4":
		label = "Form 4 — insider transaction"
	case base == "5":
		label = "Form 5 — annual insider report"
	case base == "8-K":
		label = "8-K — material event"
		if phrases := eightKItemPhrases(items); phrases != "" {
			label = "8-K — " + phrases
		}
	case base == "10-Q":
		label = "10-Q — quarterly report"
	case base == "10-K":
		label = "10-K — annual report"
	case base == "S-8" || base == "S-8 POS":
		label = "S-8 — employee stock plan"
	case base == "S-1":
		label = "S-1 — new share registration (dilution watch)"
	case base == "S-3" || base == "S-3ASR":
		label = "S-3 — shelf registration (dilution watch)"
	case strings.HasPrefix(base, "424B"):
		label = base + " — offering prospectus (dilution)"
	case base == "SC 13D":
		label = "13D — activist stake >5%"
	case base == "SC 13G":
		label = "13G — passive large holder >5%"
	case base == "13F-HR":
		label = "13F — institutional holdings (quarterly, ≤45d lag)"
	case base == "DEF 14A":
		label = "DEF 14A — proxy statement"
	case base == "6-K":
		label = "6-K — foreign issuer report"
	case base == "20-F":
		label = "20-F — foreign annual report"
	case base == "144":
		label = "Form 144 — proposed insider sale notice"
	default:
		label = base + " — SEC filing"
	}
	if amended {
		label += " (amended)"
	}
	return label
}

// eightKItemPhrases renders up to three known item codes as a phrase list.
func eightKItemPhrases(items string) string {
	var phrases []string
	for _, it := range strings.Split(items, ",") {
		it = strings.TrimSpace(it)
		if p, ok := eightKItems[it]; ok {
			phrases = append(phrases, p+" (Item "+it+")")
		}
		if len(phrases) == 3 {
			break
		}
	}
	return strings.Join(phrases, ", ")
}

// interestingForms is the curated feed allowlist: the intelligence-bearing
// form families. Everything else (CERTs, 25-NSEs, …) is noise for this feed.
var interestingForms = []string{
	"3", "4", "5", "8-K", "10-Q", "10-K", "S-8", "S-1", "S-3", "424B",
	"SC 13D", "SC 13G", "13F-HR", "DEF 14A", "6-K", "20-F", "144",
}

// InterestingForm reports whether a raw form type belongs in the feed
// (amendments of allowlisted forms included).
func InterestingForm(form string) bool {
	f := strings.TrimSuffix(strings.ToUpper(strings.TrimSpace(form)), "/A")
	for _, p := range interestingForms {
		if f == p || (p == "424B" && strings.HasPrefix(f, "424B")) ||
			(p == "S-3" && f == "S-3ASR") || (p == "S-8" && f == "S-8 POS") {
			return true
		}
	}
	return false
}

// ── Form 4 (insider transactions) ─────────────────────────────────────────

// Form4Tx is one non-derivative transaction inside a Form 4.
type Form4Tx struct {
	Code       string  // P/S/A/M/G/F/…
	Shares     float64
	Price      float64
	AcqDisp    string // A (acquired) / D (disposed)
	TxTs       int64  // transaction date, unix seconds
	PostShares float64
}

// Form4 is the parsed essence of one ownershipDocument.
type Form4 struct {
	Insider      string
	Title        string // officer title / "Director" / "10% owner"
	Transactions []Form4Tx
}

// form4XML mirrors the ownershipDocument XML (fields we consume).
type form4XML struct {
	Owners []struct {
		ID struct {
			Name string `xml:"rptOwnerName"`
		} `xml:"reportingOwnerId"`
		Rel struct {
			IsDirector   string `xml:"isDirector"`
			IsOfficer    string `xml:"isOfficer"`
			IsTenPercent string `xml:"isTenPercentOwner"`
			OfficerTitle string `xml:"officerTitle"`
		} `xml:"reportingOwnerRelationship"`
	} `xml:"reportingOwner"`
	NonDeriv struct {
		Txs []struct {
			Date struct {
				Value string `xml:"value"`
			} `xml:"transactionDate"`
			Coding struct {
				Code string `xml:"transactionCode"`
			} `xml:"transactionCoding"`
			Amounts struct {
				Shares struct {
					Value string `xml:"value"`
				} `xml:"transactionShares"`
				Price struct {
					Value string `xml:"value"`
				} `xml:"transactionPricePerShare"`
				AcqDisp struct {
					Value string `xml:"value"`
				} `xml:"transactionAcquiredDisposedCode"`
			} `xml:"transactionAmounts"`
			Post struct {
				Shares struct {
					Value string `xml:"value"`
				} `xml:"sharesOwnedFollowingTransaction"`
			} `xml:"postTransactionAmounts"`
		} `xml:"nonDerivativeTransaction"`
	} `xml:"nonDerivativeTable"`
}

// ParseForm4 parses an ownershipDocument XML into its insider + transactions.
// Pure function — tests feed it fixture files from testdata/.
func ParseForm4(data []byte) (Form4, error) {
	var doc form4XML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return Form4{}, fmt.Errorf("edgar: parse form 4: %w", err)
	}
	var out Form4
	if len(doc.Owners) > 0 {
		o := doc.Owners[0]
		out.Insider = strings.TrimSpace(o.ID.Name)
		switch {
		case xmlBool(o.Rel.IsOfficer) && strings.TrimSpace(o.Rel.OfficerTitle) != "":
			out.Title = strings.TrimSpace(o.Rel.OfficerTitle)
		case xmlBool(o.Rel.IsOfficer):
			out.Title = "Officer"
		case xmlBool(o.Rel.IsDirector):
			out.Title = "Director"
		case xmlBool(o.Rel.IsTenPercent):
			out.Title = "10% owner"
		}
	}
	for _, tx := range doc.NonDeriv.Txs {
		t := Form4Tx{
			Code:       strings.ToUpper(strings.TrimSpace(tx.Coding.Code)),
			Shares:     xmlFloat(tx.Amounts.Shares.Value),
			Price:      xmlFloat(tx.Amounts.Price.Value),
			AcqDisp:    strings.ToUpper(strings.TrimSpace(tx.Amounts.AcqDisp.Value)),
			PostShares: xmlFloat(tx.Post.Shares.Value),
		}
		if ts, ok := parseDay(tx.Date.Value); ok {
			t.TxTs = ts
		}
		if t.Code == "" && t.Shares == 0 {
			continue
		}
		out.Transactions = append(out.Transactions, t)
	}
	return out, nil
}

// FetchForm4 downloads and parses one Form 4's raw XML document.
func (c *FilingsClient) FetchForm4(ctx context.Context, cik int64, accession, primaryDoc string) (Form4, error) {
	body, err := c.get(ctx, c.FilingDocURL(cik, accession, RawXMLDoc(primaryDoc)))
	if err != nil {
		return Form4{}, err
	}
	return ParseForm4(body)
}

// AggregateForm4 collapses a (possibly multi-transaction) Form 4 to ONE
// headline row keyed by the DOMINANT transaction code — the code with the
// largest total dollar value (total shares when no prices are reported).
// Shares are summed over that code; price is the share-weighted average;
// value = shares × weighted price. Deterministic (code ties break
// alphabetically) and honest: the code is stored raw, and only P/S are ever
// presented as open-market conviction trades.
func AggregateForm4(f Form4) (code string, shares, price, value float64, txTs int64, ok bool) {
	type agg struct {
		shares, dollars float64
		ts              int64
	}
	byCode := map[string]*agg{}
	for _, t := range f.Transactions {
		if t.Code == "" || t.Shares <= 0 {
			continue
		}
		a := byCode[t.Code]
		if a == nil {
			a = &agg{}
			byCode[t.Code] = a
		}
		a.shares += t.Shares
		a.dollars += t.Shares * t.Price
		if a.ts == 0 || (t.TxTs != 0 && t.TxTs < a.ts) {
			a.ts = t.TxTs
		}
	}
	if len(byCode) == 0 {
		return "", 0, 0, 0, 0, false
	}
	codes := make([]string, 0, len(byCode))
	for k := range byCode {
		codes = append(codes, k)
	}
	sort.Strings(codes) // deterministic tie-break
	best := codes[0]
	for _, k := range codes[1:] {
		a, b := byCode[k], byCode[best]
		ka, kb := a.dollars, b.dollars
		if ka == 0 && kb == 0 { // no prices anywhere: fall back to shares
			ka, kb = a.shares, b.shares
		}
		if ka > kb {
			best = k
		}
	}
	a := byCode[best]
	wavg := 0.0
	if a.shares > 0 && a.dollars > 0 {
		wavg = a.dollars / a.shares
	}
	return best, a.shares, wavg, a.dollars, a.ts, true
}

// CodeLabel is the honest plain-English reading of a Form 4 transaction code.
// Only P and S are open-market conviction trades; everything else is
// compensation mechanics and is labeled as such.
func CodeLabel(code string) string {
	switch strings.ToUpper(code) {
	case "P":
		return "buy (open market)"
	case "S":
		return "sell (open market)"
	case "A":
		return "grant/award (not an open-market trade)"
	case "M":
		return "option exercise (not an open-market trade)"
	case "G":
		return "gift (not an open-market trade)"
	case "F":
		return "tax withholding (not an open-market trade)"
	case "D":
		return "disposition to issuer (not an open-market trade)"
	case "C":
		return "conversion (not an open-market trade)"
	case "X":
		return "in-the-money exercise (not an open-market trade)"
	default:
		return "code " + strings.ToUpper(code) + " (see SEC Form 4 codes)"
	}
}

// xmlBool parses SEC's mixed "1"/"true"/"0"/"false" booleans.
func xmlBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// xmlFloat parses a numeric value string leniently ("" → 0).
func xmlFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

// ── 13F (institutional holdings) ──────────────────────────────────────────

// Manager is one curated 13F filer.
type Manager struct {
	CIK  int64
	Name string
}

// NotableManagers is the hardcoded watch list of well-known 13F filers. CIKs
// are stable SEC identifiers; a wrong/renamed entity simply yields no 13F-HR
// filings (graceful absence, never an error).
var NotableManagers = []Manager{
	{1067983, "Berkshire Hathaway"},
	{1350694, "Bridgewater Associates"},
	{1037389, "Renaissance Technologies"},
	{1423053, "Citadel Advisors"},
	{1273087, "Millennium Management"},
	{1009207, "D. E. Shaw & Co"},
	{1179392, "Two Sigma Investments"},
	{1167557, "AQR Capital Management"},
	{1061768, "Baupost Group"},
	{1336528, "Pershing Square Capital"},
	{1040273, "Third Point"},
	{1167483, "Tiger Global Management"},
	{1135730, "Coatue Management"},
	{1061165, "Lone Pine Capital"},
	{1103804, "Viking Global Investors"},
	{1029160, "Soros Fund Management"},
	{1536411, "Duquesne Family Office"},
	{1649339, "Scion Asset Management"},
	{1656456, "Appaloosa LP"},
	{1603466, "Point72 Asset Management"},
	{1791786, "Elliott Investment Management"},
	{1166559, "Gates Foundation Trust"},
	{921669, "Icahn Carl C"},
	{1364742, "BlackRock Inc"},
	{102909, "Vanguard Group"},
}

// Holding13F is one information-table position as filed.
type Holding13F struct {
	Issuer string
	Class  string
	CUSIP  string
	Value  float64 // as reported (USD on current filings)
	Shares float64
	Type   string // SH / PRN
}

// infoTableXML mirrors the 13F information table (namespace-agnostic:
// encoding/xml matches local names when tags carry no namespace).
type infoTableXML struct {
	Entries []struct {
		Issuer string `xml:"nameOfIssuer"`
		Class  string `xml:"titleOfClass"`
		CUSIP  string `xml:"cusip"`
		Value  string `xml:"value"`
		Amt    struct {
			Shares string `xml:"sshPrnamt"`
			Type   string `xml:"sshPrnamtType"`
		} `xml:"shrsOrPrnAmt"`
	} `xml:"infoTable"`
}

// Parse13F parses a 13F information-table XML. Pure function (fixture-tested).
func Parse13F(data []byte) ([]Holding13F, error) {
	var doc infoTableXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("edgar: parse 13F information table: %w", err)
	}
	out := make([]Holding13F, 0, len(doc.Entries))
	for _, e := range doc.Entries {
		h := Holding13F{
			Issuer: strings.TrimSpace(e.Issuer),
			Class:  strings.TrimSpace(e.Class),
			CUSIP:  strings.ToUpper(strings.TrimSpace(e.CUSIP)),
			Value:  xmlFloat(e.Value),
			Shares: xmlFloat(e.Amt.Shares),
			Type:   strings.TrimSpace(e.Amt.Type),
		}
		if h.CUSIP == "" {
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// filingIndex is the (partial) shape of a filing folder's index.json.
type filingIndex struct {
	Directory struct {
		Item []struct {
			Name string `json:"name"`
		} `json:"item"`
	} `json:"directory"`
}

// FindInfoTableFile picks the information-table XML out of a 13F filing
// folder listing: prefer a name containing "infotable"/"information", else
// any .xml that isn't the primary_doc cover page. Pure function.
func FindInfoTableFile(indexJSON []byte) (string, error) {
	var idx filingIndex
	if err := json.Unmarshal(indexJSON, &idx); err != nil {
		return "", fmt.Errorf("edgar: parse filing index: %w", err)
	}
	fallback := ""
	for _, it := range idx.Directory.Item {
		name := strings.TrimSpace(it.Name)
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".xml") {
			continue
		}
		if strings.Contains(lower, "infotable") || strings.Contains(lower, "information") {
			return name, nil
		}
		if !strings.Contains(lower, "primary_doc") && fallback == "" {
			fallback = name
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("edgar: no information-table xml in filing index")
}

// Fetch13FHoldings resolves a 13F-HR filing's information table (via the
// folder's index.json) and parses it.
func (c *FilingsClient) Fetch13FHoldings(ctx context.Context, cik int64, accession string) ([]Holding13F, error) {
	idxBody, err := c.get(ctx, c.FilingIndexURL(cik, accession))
	if err != nil {
		return nil, err
	}
	file, err := FindInfoTableFile(idxBody)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, c.FilingDocURL(cik, accession, file))
	if err != nil {
		return nil, err
	}
	return Parse13F(body)
}
