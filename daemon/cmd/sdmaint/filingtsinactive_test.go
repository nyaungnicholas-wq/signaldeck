package main

import "testing"

func TestCikFromURL(t *testing.T) {
	for url, want := range map[string]int64{
		"https://www.sec.gov/Archives/edgar/data/320193/000032019326000005/a.htm": 320193,
		"https://www.sec.gov/Archives/edgar/data/0000004977/x/y":                  4977,
		"https://www.sec.gov/Archives/edgar/data/abc/x":                           0,
		"https://www.sec.gov/Archives/edgar/data/0/x":                             0,
		"https://example.com/other":                                               0,
	} {
		got, ok := cikFromURL(url)
		if got != want || ok != (want > 0) {
			t.Errorf("%s: got %d,%v want %d", url, got, ok, want)
		}
	}
}
