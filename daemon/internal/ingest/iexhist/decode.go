// Package iexhist turns one IEX historical TOPS (or DEEP) capture into daily
// bars, one per symbol.
//
// WHY THIS EXISTS. Alpaca's licence forbids showing its data, or anything
// derived from it, to third parties. IEX publishes its own exchange feed as
// free T+1 pcap downloads whose terms allow distribution with the citation
// "Data provided for free by IEX." (https://www.iex.io/legal/hist-data-terms).
// A public SignalDeck record can be built on these bars and on nothing else.
//
// WHAT THE NUMBERS ARE. Every price and share here is an IEX execution. This is
// one venue's slice of the market, not the consolidated tape: volume is a few
// percent of the real total and the day's high and low can sit inside the true
// range. Callers must label it as IEX data and never mix it into a table that
// holds another provider's bars.
//
// A capture is ~12 GB compressed, so the decoder is a single streaming pass
// that keeps one small struct per symbol and nothing else.
package iexhist

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Bar is one symbol's regular-session day on IEX.
type Bar struct {
	Open, High, Low, Close float64
	Volume                 int64 // shares, regular session, odd lots included
	Trades                 int64 // executions counted in Volume
	OfficialClose          bool  // Close came from IEX's Official Closing Price
}

// Stats is the proof a file was actually read: an empty or truncated capture
// must be told apart from a quiet day.
type Stats struct {
	Packets  int64 // captured frames
	Segments int64 // IEX-TP segments carrying at least one message
	Messages int64 // messages of every type
	Trades   int64 // trade reports of any session
	Breaks   int64 // trade breaks (reported, not applied; see ponytail below)
}

// Sale-condition flag bits of a trade report (IEX TOPS / DEEP specification).
const (
	flagExtendedHours = 0x40 // outside the regular market session
	flagOddLot        = 0x20 // does not update last sale, high or low
)

const priceScale = 1e-4 // prices are int64 with four implied decimals

type acc struct {
	Bar
	seenPrice bool
}

// DailyBars reads an UNCOMPRESSED pcap or pcapng stream and returns the
// regular-session bar of every symbol that traded.
func DailyBars(r io.Reader) (map[string]Bar, Stats, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var st Stats
	accs := map[string]*acc{}
	onPacket := func(frame []byte) {
		st.Packets++
		payload := udpPayload(frame)
		if payload != nil {
			segment(payload, accs, &st)
		}
	}
	magic, err := br.Peek(4)
	if err != nil {
		return nil, st, fmt.Errorf("iexhist: empty capture: %w", err)
	}
	switch binary.LittleEndian.Uint32(magic) {
	case 0x0A0D0D0A:
		err = readPcapNG(br, onPacket)
	case 0xA1B2C3D4, 0xA1B23C4D:
		err = readPcap(br, binary.LittleEndian, onPacket)
	case 0xD4C3B2A1, 0x4D3CB2A1:
		err = readPcap(br, binary.BigEndian, onPacket)
	default:
		return nil, st, fmt.Errorf("iexhist: not a pcap or pcapng capture (magic %#x)", magic)
	}
	out := make(map[string]Bar, len(accs))
	for sym, a := range accs {
		if a.seenPrice {
			out[sym] = a.Bar
		}
	}
	return out, st, err
}

// readPcapNG walks pcapng blocks and hands every Enhanced Packet to fn.
func readPcapNG(br *bufio.Reader, fn func([]byte)) error {
	var order binary.ByteOrder = binary.LittleEndian
	head := make([]byte, 8)
	var body []byte
	for {
		if _, err := io.ReadFull(br, head); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("iexhist: truncated pcapng block header: %w", err)
		}
		typ := order.Uint32(head[0:4])
		if typ == 0x0A0D0D0A {
			// Section header: its byte-order magic decides how to read the rest.
			bom, err := br.Peek(4)
			if err != nil {
				return fmt.Errorf("iexhist: truncated pcapng section header: %w", err)
			}
			if binary.LittleEndian.Uint32(bom) == 0x1A2B3C4D {
				order = binary.LittleEndian
			} else {
				order = binary.BigEndian
			}
		}
		total := order.Uint32(head[4:8])
		if total < 12 || total > 1<<26 {
			return fmt.Errorf("iexhist: implausible pcapng block length %d", total)
		}
		n := int(total) - 8
		if cap(body) < n {
			body = make([]byte, n)
		}
		body = body[:n]
		if _, err := io.ReadFull(br, body); err != nil {
			return fmt.Errorf("iexhist: truncated pcapng block: %w", err)
		}
		if typ == 6 && n >= 24 { // Enhanced Packet Block
			capLen := int(order.Uint32(body[12:16]))
			if 20+capLen <= n-4 {
				fn(body[20 : 20+capLen])
			}
		}
	}
}

// readPcap walks a classic libpcap file.
func readPcap(br *bufio.Reader, order binary.ByteOrder, fn func([]byte)) error {
	if _, err := br.Discard(24); err != nil {
		return fmt.Errorf("iexhist: truncated pcap header: %w", err)
	}
	head := make([]byte, 16)
	var frame []byte
	for {
		if _, err := io.ReadFull(br, head); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("iexhist: truncated pcap record header: %w", err)
		}
		n := int(order.Uint32(head[8:12]))
		if n > 1<<20 {
			return fmt.Errorf("iexhist: implausible pcap record length %d", n)
		}
		if cap(frame) < n {
			frame = make([]byte, n)
		}
		frame = frame[:n]
		if _, err := io.ReadFull(br, frame); err != nil {
			return fmt.Errorf("iexhist: truncated pcap record: %w", err)
		}
		fn(frame)
	}
}

// udpPayload strips Ethernet (with optional VLAN tags), IPv4 and UDP headers.
// Anything that is not IPv4/UDP returns nil.
func udpPayload(f []byte) []byte {
	off := 12
	for len(f) >= off+2 && binary.BigEndian.Uint16(f[off:]) == 0x8100 {
		off += 4
	}
	if len(f) < off+2 || binary.BigEndian.Uint16(f[off:]) != 0x0800 {
		return nil
	}
	off += 2
	if len(f) < off+20 || f[off]>>4 != 4 || f[off+9] != 17 {
		return nil
	}
	off += int(f[off]&0x0F) * 4
	if len(f) < off+8 {
		return nil
	}
	return f[off+8:]
}

// segment reads one IEX-TP segment: a 40-byte header, then length-prefixed
// messages.
func segment(p []byte, accs map[string]*acc, st *Stats) {
	const header = 40
	if len(p) < header || p[0] != 1 {
		return
	}
	payloadLen := int(binary.LittleEndian.Uint16(p[12:14]))
	count := int(binary.LittleEndian.Uint16(p[14:16]))
	if count == 0 || len(p) < header+payloadLen {
		return
	}
	st.Segments++
	body := p[header : header+payloadLen]
	for i := 0; i < count && len(body) >= 2; i++ {
		n := int(binary.LittleEndian.Uint16(body[0:2]))
		if len(body) < 2+n {
			return
		}
		message(body[2:2+n], accs, st)
		body = body[2+n:]
	}
}

func message(m []byte, accs map[string]*acc, st *Stats) {
	if len(m) == 0 {
		return
	}
	st.Messages++
	switch m[0] {
	case 'T': // trade report: flags, ts(8), symbol(8), size(4), price(8), id(8)
		if len(m) < 38 {
			return
		}
		st.Trades++
		flags := m[1]
		if flags&flagExtendedHours != 0 {
			return
		}
		size := int64(binary.LittleEndian.Uint32(m[18:22]))
		price := float64(int64(binary.LittleEndian.Uint64(m[22:30]))) * priceScale
		if price <= 0 {
			return
		}
		a := get(accs, m[10:18])
		a.Volume += size
		a.Trades++
		if flags&flagOddLot != 0 {
			return
		}
		if !a.seenPrice {
			a.seenPrice = true
			a.Open, a.High, a.Low = price, price, price
		}
		if price > a.High {
			a.High = price
		}
		if price < a.Low {
			a.Low = price
		}
		if !a.OfficialClose {
			a.Close = price
		}
	case 'B':
		// ponytail: a broken trade is counted, not unwound. Breaks are rare and
		// unwinding one needs every trade kept in memory. If Stats.Breaks is ever
		// material for a day, re-read that file keeping per-symbol trade lists.
		st.Breaks++
	case 'X': // official price: type('Q' open / 'M' close), ts(8), symbol(8), price(8)
		if len(m) < 26 {
			return
		}
		price := float64(int64(binary.LittleEndian.Uint64(m[18:26]))) * priceScale
		if price <= 0 {
			return
		}
		a := get(accs, m[10:18])
		switch m[1] {
		case 'Q':
			if a.seenPrice {
				a.Open = price
			}
		case 'M':
			a.Close, a.OfficialClose = price, true
			if !a.seenPrice {
				a.seenPrice = true
				a.Open, a.High, a.Low = price, price, price
			}
		}
	}
}

func get(accs map[string]*acc, raw []byte) *acc {
	sym := strings.TrimRight(string(raw), " \x00")
	a := accs[sym]
	if a == nil {
		a = &acc{}
		accs[sym] = a
	}
	return a
}
