// Package envcfg records operator overrides the daemon REFUSED to use.
//
// THE HOLE THIS CLOSES
// --------------------
// Every env-var helper in this codebase has the same shape: read the variable,
// try to parse it, and on any failure return the default. That is the right
// RUNTIME behaviour — a malformed number must not take the daemon down — but it
// was also completely silent. An operator who set a value and got a different
// one had no way to find out, and nothing anywhere recorded the divergence.
//
// This is not hypothetical. The 2026-08-09 audit found
// SIGNALDECK_RISK_FLATTEN_DRAWDOWN — the rung that force-liquidates the whole
// book — discarding a rejected override in total silence: typing `15` for 15%
// ran the 0.25 default. That was fixed for ONE key family. Measured 2026-08-11,
// the same silent-fallback shape was still live in ten helpers across eight
// packages, covering ~50 variables.
//
// WHY THIS MATTERS MOST FOR RETENTION
// -----------------------------------
// The dangerous direction is not a rejected value that keeps too much data — it
// is one that keeps too little. An operator who LENGTHENS a retention window to
// protect data and mistypes it silently gets the shorter default, and the sweep
// deletes rows they meant to keep. Deletion is irreversible, so those keys are
// recorded as CRITICAL (RejectCritical) and make the fleet unhealthy rather
// than merely being logged.
//
// WHAT IT DELIBERATELY DOES NOT DO
// --------------------------------
// It does not change which value is used. The fallback is still the default and
// the daemon still starts; this package only makes the divergence visible. A
// config layer that refused to boot on a typo would be a different and much
// riskier change than the defect calls for.
package envcfg

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// Rejection is one override the daemon read, refused, and replaced.
type Rejection struct {
	Key    string `json:"key"`
	Raw    string `json:"raw"`    // redacted when the key names a secret
	Reason string `json:"reason"` // why it was refused
	Using  string `json:"using"`  // the value actually in force
	// Critical marks a key whose rejection has irreversible consequences —
	// today, the retention windows that drive row deletion.
	Critical bool `json:"critical"`
}

var (
	mu   sync.Mutex
	seen = map[string]Rejection{}
)

// secretish reports whether a key's VALUE must never be echoed. A rejected
// token is still a token, and a warning log is not a safe place to put one.
func secretish(key string) bool {
	k := strings.ToUpper(key)
	for _, s := range []string{
		"TOKEN", "SECRET", "PASSWORD", "PASSWD", "APIKEY", "KEY", "CREDENTIAL", "AUTH",
	} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// Reject records that `key` was set to something unusable, and that `using` is
// what is actually in force instead.
//
// Deduped by key: several of these helpers run once per sweep, so recording
// every call would turn one typo into an unbounded log. The first rejection for
// a key logs; later ones update the entry silently.
func Reject(key, raw, reason, using string) { record(key, raw, reason, using, false) }

// RejectCritical is Reject for a key whose rejection has irreversible
// consequences — the retention windows in internal/maintain, which decide which
// rows get DELETED.
func RejectCritical(key, raw, reason, using string) { record(key, raw, reason, using, true) }

func record(key, raw, reason, using string, critical bool) {
	if secretish(key) {
		raw = "(redacted)"
	}
	r := Rejection{Key: key, Raw: raw, Reason: reason, Using: using, Critical: critical}

	mu.Lock()
	_, dup := seen[key]
	seen[key] = r
	mu.Unlock()
	if dup {
		return
	}

	msg := "env override REJECTED — running the default instead"
	if critical {
		msg = "env override REJECTED on a key that governs DATA DELETION — " +
			"running the default instead, which may delete rows you meant to keep"
	}
	slog.Warn(msg, "key", key, "value", raw, "reason", reason, "using", using)
}

// Rejected returns every override refused so far, sorted by key.
func Rejected() []Rejection {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Rejection, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// HasCritical reports whether any refused override governs deletion.
func HasCritical() bool {
	mu.Lock()
	defer mu.Unlock()
	for _, r := range seen {
		if r.Critical {
			return true
		}
	}
	return false
}

// Reset clears the registry. Tests only.
func Reset() {
	mu.Lock()
	defer mu.Unlock()
	seen = map[string]Rejection{}
}
