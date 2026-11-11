package edgar

import (
	"os"
	"path/filepath"
	"testing"
)

// A Form 4 names the ISSUER whose shares moved. Without it a company that is
// the reporting owner of another issuer had those trades stored as its own
// insider activity.
func TestParseForm4_ReadsIssuerCIK(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "form4_sale.xml"))
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseForm4(b)
	if err != nil {
		t.Fatal(err)
	}
	if f.IssuerCIK != 320193 {
		t.Fatalf("IssuerCIK = %d, want 320193 (zero-padded 0000320193 in the document)", f.IssuerCIK)
	}
	none, err := ParseForm4([]byte(`<ownershipDocument><reportingOwner/></ownershipDocument>`))
	if err != nil {
		t.Fatal(err)
	}
	if none.IssuerCIK != 0 {
		t.Fatalf("a document naming no issuer must read 0, got %d", none.IssuerCIK)
	}
}

// An option line on a 13F reports the UNDERLYING shares. It must be told apart
// from a share holding.
func TestParse13F_MarksOptionLines(t *testing.T) {
	const doc = `<informationTable>
  <infoTable><nameOfIssuer>APPLE INC</nameOfIssuer><titleOfClass>COM</titleOfClass><cusip>037833100</cusip><value>100</value>
    <shrsOrPrnAmt><sshPrnamt>10</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt></infoTable>
  <infoTable><nameOfIssuer>TESLA INC</nameOfIssuer><titleOfClass>COM</titleOfClass><cusip>88160R101</cusip><value>900</value>
    <shrsOrPrnAmt><sshPrnamt>90</sshPrnamt><sshPrnamtType>SH</sshPrnamtType></shrsOrPrnAmt><putCall>Put</putCall></infoTable>
</informationTable>`
	hs, err := Parse13F([]byte(doc))
	if err != nil || len(hs) != 2 {
		t.Fatalf("holdings = %+v err=%v", hs, err)
	}
	if hs[0].PutCall != "" {
		t.Errorf("share line PutCall = %q, want empty", hs[0].PutCall)
	}
	if hs[1].PutCall != "PUT" {
		t.Errorf("option line PutCall = %q, want PUT", hs[1].PutCall)
	}
}
