// iexhist-bars streams one IEX historical capture (.pcap.gz, local file or URL)
// and prints its daily bars as CSV: symbol,open,high,low,close,volume,trades,official_close.
// Nothing is stored: a 12 GB download is decoded as it arrives.
//
//	iexhist-bars -url https://.../20261110_IEXTP1_TOPS1.6.pcap.gz -out bars.csv
//
// Data provided for free by IEX. https://www.iex.io/legal/hist-data-terms
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/nyaungnicholas-wq/signaldeck/internal/ingest/iexhist"
)

func main() {
	url := flag.String("url", "", "capture URL (.pcap.gz)")
	file := flag.String("file", "", "local capture (.pcap.gz)")
	out := flag.String("out", "", "CSV output path (default stdout)")
	flag.Parse()

	var src io.ReadCloser
	switch {
	case *file != "":
		f, err := os.Open(*file)
		if err != nil {
			fail(err)
		}
		src = f
	case *url != "":
		resp, err := (&http.Client{Timeout: 3 * time.Hour}).Get(*url)
		if err != nil {
			fail(err)
		}
		if resp.StatusCode != http.StatusOK {
			fail(fmt.Errorf("GET %s: %s", *url, resp.Status))
		}
		src = resp.Body
	default:
		fail(fmt.Errorf("give -url or -file"))
	}
	defer src.Close() //nolint:errcheck

	zr, err := gzip.NewReader(bufio.NewReaderSize(src, 4<<20))
	if err != nil {
		fail(fmt.Errorf("gzip: %w", err))
	}
	start := time.Now()
	bars, st, derr := iexhist.DailyBars(zr)

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fail(err)
		}
		defer f.Close() //nolint:errcheck
		w = f
	}
	bw := bufio.NewWriter(w)
	_, _ = fmt.Fprintln(bw, "symbol,open,high,low,close,volume,trades,official_close")
	syms := make([]string, 0, len(bars))
	for s := range bars {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	for _, s := range syms {
		b := bars[s]
		_, _ = fmt.Fprintf(bw, "%s,%.4f,%.4f,%.4f,%.4f,%d,%d,%t\n", s, b.Open, b.High, b.Low, b.Close, b.Volume, b.Trades, b.OfficialClose)
	}
	if err := bw.Flush(); err != nil {
		fail(err)
	}
	fmt.Fprintf(os.Stderr, "iexhist-bars: %d symbols, %d packets, %d segments, %d messages, %d trades, %d breaks in %s\n",
		len(bars), st.Packets, st.Segments, st.Messages, st.Trades, st.Breaks, time.Since(start).Round(time.Second))
	// A capture that decoded to nothing, or stopped early, is a failure even
	// though some rows were written: the caller must not store a partial day.
	if derr != nil {
		fail(derr)
	}
	if st.Trades == 0 {
		fail(fmt.Errorf("no trade reports decoded: wrong feed or wrong format"))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "iexhist-bars: FAILED:", err)
	os.Exit(1)
}
