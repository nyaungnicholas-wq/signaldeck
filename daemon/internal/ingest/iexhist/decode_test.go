package iexhist

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func le16(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le64(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }

func sym8(s string) []byte {
	b := []byte("        ")
	copy(b, s)
	return b
}

// trade builds a 38-byte trade report. price is in dollars.
func trade(flags byte, sym string, size uint32, price float64) []byte {
	m := []byte{'T', flags}
	m = append(m, le64(1)...)
	m = append(m, sym8(sym)...)
	m = append(m, le32(size)...)
	m = append(m, le64(uint64(int64(price*10000+0.5)))...)
	return append(m, le64(7)...)
}

func official(kind byte, sym string, price float64) []byte {
	m := []byte{'X', kind}
	m = append(m, le64(1)...)
	m = append(m, sym8(sym)...)
	return append(m, le64(uint64(int64(price*10000+0.5)))...)
}

// frame wraps messages in IEX-TP, UDP, IPv4 and Ethernet.
func frame(msgs ...[]byte) []byte {
	var body []byte
	for _, m := range msgs {
		body = append(body, le16(uint16(len(m)))...)
		body = append(body, m...)
	}
	tp := make([]byte, 40)
	tp[0] = 1
	binary.LittleEndian.PutUint16(tp[12:], uint16(len(body)))
	binary.LittleEndian.PutUint16(tp[14:], uint16(len(msgs)))
	udp := append(make([]byte, 8), append(tp, body...)...)
	ip := make([]byte, 20)
	ip[0], ip[9] = 0x45, 17
	eth := make([]byte, 14)
	eth[12], eth[13] = 0x08, 0x00
	return append(eth, append(ip, udp...)...)
}

func pcapng(frames ...[]byte) []byte {
	block := func(typ uint32, body []byte) []byte {
		for len(body)%4 != 0 {
			body = append(body, 0)
		}
		total := uint32(len(body) + 12)
		out := append(le32(typ), le32(total)...)
		out = append(out, body...)
		return append(out, le32(total)...)
	}
	shb := append(le32(0x1A2B3C4D), make([]byte, 12)...)
	out := block(0x0A0D0D0A, shb)
	out = append(out, block(1, make([]byte, 8))...)
	for _, f := range frames {
		epb := make([]byte, 12)
		epb = append(epb, le32(uint32(len(f)))...)
		epb = append(epb, le32(uint32(len(f)))...)
		out = append(out, block(6, append(epb, f...))...)
	}
	return out
}

func pcapClassic(frames ...[]byte) []byte {
	out := append(le32(0xA1B2C3D4), make([]byte, 20)...)
	for _, f := range frames {
		out = append(out, make([]byte, 8)...)
		out = append(out, le32(uint32(len(f)))...)
		out = append(out, le32(uint32(len(f)))...)
		out = append(out, f...)
	}
	return out
}

func TestDailyBars(t *testing.T) {
	frames := [][]byte{
		frame(trade(0x40, "AAPL", 500, 90)),                             // pre-market: ignored entirely
		frame(trade(0, "AAPL", 100, 100), trade(0, "AAPL", 200, 103)),   // open 100, high 103
		frame(trade(0x20, "AAPL", 7, 150)),                              // odd lot: volume only, no price
		frame(trade(0, "AAPL", 100, 99), trade(0, "MSFT", 300, 50.25)),  // low 99
		frame(trade(0, "AAPL", 50, 101)),                                // last trade 101
		frame(official('M', "IEXG", 12.5), official('M', "MSFT", 50.5)), // official closes
		frame(trade(0x40, "AAPL", 900, 200)),                            // after hours: ignored
		{0xde, 0xad},                                                    // junk frame must not panic
	}
	for name, capture := range map[string][]byte{"pcapng": pcapng(frames...), "pcap": pcapClassic(frames...)} {
		bars, st, err := DailyBars(bytes.NewReader(capture))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		a := bars["AAPL"]
		if a.Open != 100 || a.High != 103 || a.Low != 99 || a.Close != 101 {
			t.Errorf("%s AAPL OHLC = %v/%v/%v/%v, want 100/103/99/101", name, a.Open, a.High, a.Low, a.Close)
		}
		if a.Volume != 457 || a.Trades != 5 {
			t.Errorf("%s AAPL volume %d trades %d, want 457 and 5 (extended hours excluded, odd lot included)", name, a.Volume, a.Trades)
		}
		if m := bars["MSFT"]; m.Close != 50.5 || !m.OfficialClose || m.Open != 50.25 {
			t.Errorf("%s MSFT = %+v, want open 50.25 and the official close 50.5", name, m)
		}
		if g := bars["IEXG"]; g.Close != 12.5 || g.Volume != 0 {
			t.Errorf("%s IEXG = %+v, want an official-close-only bar", name, g)
		}
		if st.Packets != 8 || st.Segments != 7 || st.Trades != 8 {
			t.Errorf("%s stats = %+v, want 8 packets, 7 segments, 8 trades", name, st)
		}
	}
}

func TestDailyBarsRejectsGarbageAndTruncation(t *testing.T) {
	if _, _, err := DailyBars(bytes.NewReader([]byte("not a capture at all"))); err == nil {
		t.Error("garbage input must be an error, not an empty day")
	}
	full := pcapng(frame(trade(0, "AAPL", 100, 100)), frame(trade(0, "AAPL", 100, 101)))
	bars, _, err := DailyBars(bytes.NewReader(full[:len(full)-10]))
	if err == nil {
		t.Error("a truncated capture must report an error")
	}
	if bars["AAPL"].Close != 100 {
		t.Errorf("bars read before the truncation must survive: %+v", bars["AAPL"])
	}
}
