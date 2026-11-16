package edgar

import "testing"

// A Form 4/A names the original's filing date; an original Form 4 has none.
func TestParseForm4ReadsDateOfOriginalSubmission(t *testing.T) {
	f, err := ParseForm4([]byte(`<ownershipDocument><documentType>4/A</documentType>` +
		`<dateOfOriginalSubmission> 2026-03-03 </dateOfOriginalSubmission>` +
		`<reportingOwner><reportingOwnerId><rptOwnerName>DOE JOHN</rptOwnerName></reportingOwnerId></reportingOwner>` +
		`</ownershipDocument>`))
	if err != nil || f.AmendsDay != "2026-03-03" {
		t.Fatalf("AmendsDay=%q err=%v, want 2026-03-03", f.AmendsDay, err)
	}
	g, err := ParseForm4([]byte(`<ownershipDocument><documentType>4</documentType></ownershipDocument>`))
	if err != nil || g.AmendsDay != "" {
		t.Fatalf("original AmendsDay=%q err=%v, want empty", g.AmendsDay, err)
	}
}
