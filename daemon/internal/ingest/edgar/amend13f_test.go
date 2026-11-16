package edgar

import (
	"strings"
	"testing"
)

func TestFold13F(t *testing.T) {
	t.Run("original then NEW HOLDINGS amendment", func(t *testing.T) {
		parts := []Part13F{
			{
				Amendment: false,
				Type:      "",
				Holdings: []Holding13F{
					{Issuer: "A", CUSIP: "A", Shares: 100, Value: 1000},
					{Issuer: "B", CUSIP: "B", Shares: 50, Value: 500},
					{Issuer: "A", CUSIP: "A", Shares: 10, Value: 100},
					{Issuer: "C", CUSIP: "C", Shares: 999, Value: 999, PutCall: "PUT"},
				},
			},
			{
				Amendment: true,
				Type:      "NEW HOLDINGS",
				Holdings: []Holding13F{
					{Issuer: "D", CUSIP: "D", Shares: 5, Value: 50},
					{Issuer: "B", CUSIP: "B", Shares: 1, Value: 10},
				},
			},
		}
		got := Fold13F(parts)
		want := []Holding13F{
			{Issuer: "A", CUSIP: "A", Shares: 110, Value: 1100},
			{Issuer: "B", CUSIP: "B", Shares: 51, Value: 510},
			{Issuer: "D", CUSIP: "D", Shares: 5, Value: 50},
		}
		if len(got) != len(want) {
			t.Fatalf("len got=%d want=%d", len(got), len(want))
		}
		for i := range want {
			if got[i].CUSIP != want[i].CUSIP || got[i].Shares != want[i].Shares || got[i].Value != want[i].Value {
				t.Fatalf("got[%d]=%+v want[%d]=%+v", i, got[i], i, want[i])
			}
		}
	})

	t.Run("original then RESTATEMENT amendment", func(t *testing.T) {
		parts := []Part13F{
			{Amendment: false, Type: "", Holdings: []Holding13F{{Issuer: "A", CUSIP: "A", Shares: 100, Value: 1000}}},
			{Amendment: true, Type: "RESTATEMENT", Holdings: []Holding13F{{Issuer: "B", CUSIP: "B", Shares: 7, Value: 70}}},
		}
		got := Fold13F(parts)
		want := []Holding13F{{Issuer: "B", CUSIP: "B", Shares: 7, Value: 70}}
		if len(got) != len(want) {
			t.Fatalf("len got=%d want=%d", len(got), len(want))
		}
		if got[0].CUSIP != want[0].CUSIP || got[0].Shares != want[0].Shares || got[0].Value != want[0].Value {
			t.Fatalf("got=%+v want=%+v", got, want)
		}
	})

	t.Run("nil input", func(t *testing.T) {
		got := Fold13F(nil)
		if len(got) != 0 {
			t.Fatalf("len got=%d want=0", len(got))
		}
	})
}

func TestParse13FAmendmentType(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?><edgarSubmission xmlns="http://www.sec.gov/edgar/thirteenffiler" xmlns:com="http://www.sec.gov/edgar/common"><headerData><submissionType>13F-HR/A</submissionType></headerData><formData><coverPage><reportCalendarOrQuarter>09-30-2026</reportCalendarOrQuarter><isAmendment>true</isAmendment><amendmentInfo><amendmentType>NEW HOLDINGS</amendmentType></amendmentInfo></coverPage></formData></edgarSubmission>`

	got, err := Parse13FAmendmentType([]byte(doc))
	if err != nil {
		t.Fatalf("err=%v want=nil", err)
	}
	if got != "NEW HOLDINGS" {
		t.Fatalf("got=%q want=%q", got, "NEW HOLDINGS")
	}

	doc2 := strings.Replace(doc, "NEW HOLDINGS", " restatement ", 1)
	got, err = Parse13FAmendmentType([]byte(doc2))
	if err != nil {
		t.Fatalf("err=%v want=nil", err)
	}
	if got != "RESTATEMENT" {
		t.Fatalf("got=%q want=%q", got, "RESTATEMENT")
	}

	doc3 := strings.Replace(doc, "<amendmentInfo><amendmentType>NEW HOLDINGS</amendmentType></amendmentInfo>", "", 1)
	got, err = Parse13FAmendmentType([]byte(doc3))
	if err != nil {
		t.Fatalf("err=%v want=nil", err)
	}
	if got != "" {
		t.Fatalf("got=%q want=%q", got, "")
	}
}
