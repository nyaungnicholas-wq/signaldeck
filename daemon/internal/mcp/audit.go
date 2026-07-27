// LAYER 5 — audit and anomaly detection.
//
// Every call is recorded: client identity, tool, a HASH of the parameters
// (never the parameters — a description is a user's own text and there is no
// reason for this server to keep it), response size, outcome, latency and
// timestamp. The sink is append-only, in the same spirit as the prediction
// ledger: entries are written and never rewritten, so the record of an
// extraction attempt survives the attempt.
//
// Detection targets the three shapes extraction actually takes:
//
//	symbol-enumeration    — many distinct symbols from one client in one window.
//	                        The signature of "walk the universe".
//	monotonic-sweep       — consecutive requests ordered lexicographically.
//	                        Humans do not ask about AAPL, AAPU, AAWW in order.
//	sustained-max-rate    — running at the ceiling for minutes, which is a
//	                        script's behaviour and not a conversation's.
//
// A flag auto-throttles the client (budget.throttle) rather than banning it:
// a ban tells the extractor to change identity, while a throttle is slow and
// boring and keeps them observable. All three thresholds are set so ordinary
// advisory use — a handful of symbols in a session — is nowhere near them.
package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Detection thresholds.
const (
	anomalyWindow      = 30 * time.Minute
	enumerationSymbols = 20 // distinct symbols in the window
	sweepRun           = 5  // consecutive lexicographically-increasing symbols
	maxRateWindow      = 2 * time.Minute
	maxRateCalls       = 40 // calls within maxRateWindow
	trackedHistory     = 64 // per-client request memory
)

// auditEntry is one line of the append-only sink.
type auditEntry struct {
	At            time.Time `json:"at"`
	Client        string    `json:"client"`
	Tool          string    `json:"tool,omitempty"`
	ParamHash     string    `json:"paramHash,omitempty"`
	Bytes         int       `json:"bytes,omitempty"`
	Outcome       string    `json:"outcome"`
	Anomaly       string    `json:"anomaly,omitempty"`
	Dropped       []string  `json:"droppedFields,omitempty"`
	LatencyMicros int64     `json:"latencyMicros,omitempty"`
}

// hashParams fingerprints an argument blob so repeated and swept parameters
// are comparable in the log without the log holding the parameters.
func hashParams(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:8])
}

type callMark struct {
	at     time.Time
	symbol string
}

type clientTrace struct {
	recent  []callMark
	symbols map[string]time.Time
	flagged map[string]bool
}

type auditor struct {
	mu     sync.Mutex
	path   string
	now    func() time.Time
	traces map[string]*clientTrace

	// mem is the in-process tail of the sink. It exists so tests (and a future
	// operator view) can read the record without parsing a file, and is
	// bounded so it cannot become a memory leak on a long-running daemon.
	mem []auditEntry
	// lastErr holds the most recent sink-write failure, for the operator view.
	lastErr error
}

const memTail = 512

func newAuditor(path string, now func() time.Time) *auditor {
	return &auditor{path: path, now: now, traces: map[string]*clientTrace{}}
}

// record appends one entry. A failure to write the file is deliberately NOT
// fatal to the request: refusing to answer because the log is unwritable turns
// a full disk into an outage. It is, however, recorded in memory, and the
// write error surfaces on the next operator read via lastWriteErr.
func (a *auditor) record(e auditEntry) {
	if e.At.IsZero() {
		e.At = a.now()
	}
	a.mu.Lock()
	a.mem = append(a.mem, e)
	if len(a.mem) > memTail {
		a.mem = a.mem[len(a.mem)-memTail:]
	}
	path := a.path
	a.mu.Unlock()
	if path == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		a.mu.Lock()
		a.lastErr = err
		a.mu.Unlock()
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// entries returns the in-memory tail (tests, operator view).
func (a *auditor) entries() []auditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]auditEntry(nil), a.mem...)
}

// observe feeds one request to the detector and returns a flag name when the
// request completes an extraction pattern, or "" when it does not. A flag is
// returned once per pattern per client per window — repeating it every call
// would drown the sink in the middle of exactly the incident it is recording.
func (a *auditor) observe(clientID string, args toolArgs) string {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()

	t := a.traces[clientID]
	if t == nil {
		t = &clientTrace{symbols: map[string]time.Time{}, flagged: map[string]bool{}}
		a.traces[clientID] = t
	}
	// Expire everything outside the window first, so a client that goes quiet
	// is genuinely forgiven rather than accumulating forever.
	cut := now.Add(-anomalyWindow)
	kept := t.recent[:0]
	for _, m := range t.recent {
		if m.at.After(cut) {
			kept = append(kept, m)
		}
	}
	t.recent = kept
	for s, at := range t.symbols {
		if !at.After(cut) {
			delete(t.symbols, s)
		}
	}
	if len(t.symbols) == 0 && len(t.recent) == 0 {
		t.flagged = map[string]bool{}
	}

	t.recent = append(t.recent, callMark{at: now, symbol: args.Symbol})
	if len(t.recent) > trackedHistory {
		t.recent = t.recent[len(t.recent)-trackedHistory:]
	}
	if args.Symbol != "" {
		t.symbols[args.Symbol] = now
	}

	flag := ""
	switch {
	case len(t.symbols) >= enumerationSymbols && !t.flagged["symbol-enumeration"]:
		flag = "symbol-enumeration"
	case monotonicRun(t.recent) >= sweepRun && !t.flagged["monotonic-sweep"]:
		flag = "monotonic-sweep"
	case countSince(t.recent, now.Add(-maxRateWindow)) >= maxRateCalls && !t.flagged["sustained-max-rate"]:
		flag = "sustained-max-rate"
	}
	// The flag is not written to the sink here: record() takes the same mutex,
	// and a detection that exists only in memory is not in an append-only sink.
	// budget.throttle writes it, on this same call, to the file.
	if flag != "" {
		t.flagged[flag] = true
	}
	return flag
}

// monotonicRun returns the length of the trailing run of strictly increasing
// symbols. Strictly increasing, so a client legitimately re-asking about the
// same symbol never contributes to it.
func monotonicRun(marks []callMark) int {
	run, prev := 0, ""
	for _, m := range marks {
		if m.symbol == "" {
			run, prev = 0, ""
			continue
		}
		if prev != "" && m.symbol > prev {
			run++
		} else {
			run = 1
		}
		prev = m.symbol
	}
	return run
}

func countSince(marks []callMark, cut time.Time) int {
	n := 0
	for _, m := range marks {
		if m.at.After(cut) {
			n++
		}
	}
	return n
}
