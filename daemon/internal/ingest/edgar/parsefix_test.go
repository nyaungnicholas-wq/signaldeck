package edgar

import (
	"encoding/json"
	"strings"
	"testing"
)

// EDGAR emits companyfacts `cik` as a bare number for most issuers and as a
// QUOTED number for some. A plain int64 field made the quoted form a hard parse
// failure for the entire document, discarding a company's whole fundamentals set
// — ~106 fundamentals_error events a day in production, e.g.
// "LHSW: edgar: parse companyfacts CIK 2004024: json: cannot unmarshal string
// into Go struct field factsResp.cik of type int64".
func TestCompanyFactsAcceptsCIKAsNumberOrString(t *testing.T) {
	cases := map[string]struct {
		body string
		want int64
	}{
		"bare number":   {`{"cik":320193,"facts":{}}`, 320193},
		"quoted number": {`{"cik":"2004024","facts":{}}`, 2004024},
		"null":          {`{"cik":null,"facts":{}}`, 0},
		"empty string":  {`{"cik":"","facts":{}}`, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var resp factsResp
			if err := json.Unmarshal([]byte(tc.body), &resp); err != nil {
				t.Fatalf("unmarshal failed on %s: %v", name, err)
			}
			if int64(resp.CIK) != tc.want {
				t.Fatalf("CIK = %d, want %d", int64(resp.CIK), tc.want)
			}
		})
	}
}

// A garbage cik must not fail the document NOR silently retarget the
// fundamentals onto another company: it yields 0, and the caller keeps the CIK
// it actually requested.
func TestMalformedCIKIsZeroNotAnError(t *testing.T) {
	var resp factsResp
	if err := json.Unmarshal([]byte(`{"cik":"not-a-number","facts":{}}`), &resp); err != nil {
		t.Fatalf("a malformed cik must not fail the whole document: %v", err)
	}
	if resp.CIK != 0 {
		t.Fatalf("CIK = %d, want 0 so the requested CIK is kept", int64(resp.CIK))
	}
}

// Several 13F filers (Two Sigma among them) declare encoding="us-ascii".
// Go's encoding/xml rejects any non-UTF-8 declaration without a CharsetReader,
// so the whole information table was unparseable — the live error was
// 'xml: encoding "us-ascii" declared but Decoder.CharsetReader is nil'.
func TestParse13FAcceptsLegacyCharsets(t *testing.T) {
	const tmpl = `<?xml version="1.0" encoding="%s"?>
<informationTable>
  <infoTable>
    <nameOfIssuer>ACME CORP</nameOfIssuer>
    <titleOfClass>COM</titleOfClass>
    <cusip>000360206</cusip>
    <value>12345</value>
    <shrsOrPrnAmt><sshPrnamt>500</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt>
  </infoTable>
</informationTable>`

	for _, enc := range []string{"us-ascii", "US-ASCII", "utf-8", "ISO-8859-1", "windows-1252"} {
		t.Run(enc, func(t *testing.T) {
			doc := strings.Replace(tmpl, "%s", enc, 1)
			holdings, err := Parse13F([]byte(doc))
			if err != nil {
				t.Fatalf("encoding %s must parse, got: %v", enc, err)
			}
			if len(holdings) != 1 {
				t.Fatalf("got %d holdings, want 1", len(holdings))
			}
			if holdings[0].Issuer != "ACME CORP" {
				t.Fatalf("issuer = %q", holdings[0].Issuer)
			}
		})
	}
}

// A genuinely non-ASCII-compatible encoding must still be REFUSED, so a
// document we cannot read fails loudly instead of being silently misparsed.
func TestParse13FRefusesUnknownCharset(t *testing.T) {
	doc := `<?xml version="1.0" encoding="utf-16"?><informationTable></informationTable>`
	if _, err := Parse13F([]byte(doc)); err == nil {
		t.Fatal("an unsupported charset must be refused, not silently accepted")
	}
}

// Form 4 shares the same decoder, so it inherits the same tolerance.
func TestParseForm4AcceptsUSASCII(t *testing.T) {
	doc := `<?xml version="1.0" encoding="us-ascii"?>
<ownershipDocument>
  <reportingOwner><reportingOwnerId><rptOwnerName>DOE JOHN</rptOwnerName></reportingOwnerId></reportingOwner>
</ownershipDocument>`
	f, err := ParseForm4([]byte(doc))
	if err != nil {
		t.Fatalf("us-ascii Form 4 must parse, got: %v", err)
	}
	if f.Insider != "DOE JOHN" {
		t.Fatalf("insider = %q, want DOE JOHN", f.Insider)
	}
}
