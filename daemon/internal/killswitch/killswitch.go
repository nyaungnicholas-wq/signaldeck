// Package killswitch is the platform's halt control: a file on disk that stops
// this repository from opening new positions, checked before every order.
//
// WHY A FILE. The halt has to be trippable by a human with no running process
// to talk to, by a cron job, by a monitoring hook, and by an operator over SSH
// with the daemon wedged. `touch ops/HALT` works in all four cases. An HTTP
// endpoint or an in-memory flag works in none of them, because the situations
// that justify a halt are exactly the situations where the process is the thing
// you do not trust.
//
// FAIL CLOSED, and this is the whole design. Three outcomes are possible when
// the switch is read, and only ONE of them may permit trading:
//
//	the file is definitively absent  -> RUNNING
//	the file is present              -> HALTED
//	the file could not be read at all -> HALTED
//
// The third case is the one that matters and the one naive implementations get
// wrong. A permission error, an unmounted volume, or a path that resolves into
// nothing is not evidence of safety; it is the absence of evidence. A check
// that cannot prove the system is safe must not report that it is. Treating an
// unreadable switch as "no halt requested" makes the control disappear at
// exactly the moment the machine is sick enough to be worth halting.
//
// EXITS ARE NOT BLOCKED BY THIS. Callers must consult Halted() before opening a
// position and must NOT use it to block a closing one. A halt that traps the
// book inside the position it was tripped by is a bigger risk than the one it
// was meant to control — the same doctrine internal/riskgate and internal/ev
// each state first in their own decision functions.
package killswitch

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvPath overrides where the halt file lives.
	EnvPath = "SIGNALDECK_KILL_SWITCH"
	// EnvHalt halts without a file, for environments where the process has no
	// writable path of its own (a container, a CI run, a one-off manual run).
	EnvHalt = "SIGNALDECK_HALT"
	// DefaultPath is relative to the daemon's working directory, so the switch
	// lives beside the repo it halts rather than somewhere a reader has to be
	// told about.
	DefaultPath = "ops/HALT"

	// maxReasonBytes bounds how much of the halt file becomes the audited
	// reason. The file is meant to hold a sentence about why; a reason longer
	// than this is a log-flooding accident, not an explanation.
	maxReasonBytes = 512
)

// State is one reading of the switch. Reason is always populated when Halted,
// so a refusal never has to be explained by the caller guessing.
type State struct {
	Halted bool   `json:"halted"`
	Reason string `json:"reason,omitempty"`
	Path   string `json:"path"`
}

// Path returns the halt-file path in force: EnvPath when set to something
// non-blank, DefaultPath otherwise.
func Path() string {
	if p := strings.TrimSpace(os.Getenv(EnvPath)); p != "" {
		return p
	}
	return DefaultPath
}

// Check reads the switch at the path in force.
func Check() State { return CheckPath(Path()) }

// CheckPath reads the switch at an explicit path.
//
// The environment override is consulted FIRST and can only ever halt, never
// clear a halt — an env var must not be able to talk the system past a file
// that is sitting on disk asking it to stop.
func CheckPath(path string) State {
	if halted, reason := envHalt(); halted {
		return State{Halted: true, Reason: reason, Path: path}
	}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		// Present. A directory counts: someone put something at the halt path,
		// and second-guessing what they meant is not this function's job.
		return State{Halted: true, Reason: haltReason(path, info.IsDir()), Path: path}

	case os.IsNotExist(err):
		// The ONLY outcome that permits trading, and only because the operating
		// system answered the question definitively.
		return State{Path: path}

	default:
		// Unreadable. Fail closed: this is the absence of evidence, not evidence
		// of absence, and the difference is the entire point of the control.
		return State{
			Halted: true,
			Path:   path,
			Reason: fmt.Sprintf(
				"halt switch at %s could not be read (%v) — halting, because a check "+
					"that cannot prove the system is safe must not report that it is", path, err),
		}
	}
}

// Halted is the one-line form for call sites that only branch on the answer.
func Halted() bool { return Check().Halted }

// envHalt reports whether EnvHalt demands a halt.
//
// Unset or explicitly false-y clears; anything else halts. An unparseable value
// halts on purpose: SIGNALDECK_HALT=maybe is an operator who meant something,
// and the safe reading of "something" is stop.
func envHalt() (bool, string) {
	raw, ok := os.LookupEnv(EnvHalt)
	if !ok {
		return false, ""
	}
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "", "0", "false", "no", "off":
		return false, ""
	case "1", "true", "yes", "on":
		return true, EnvHalt + " is set — new entries halted by environment"
	default:
		return true, fmt.Sprintf(
			"%s is set to %q, which is not a recognised boolean — halting, because an "+
				"unreadable halt instruction is still a halt instruction", EnvHalt, raw)
	}
}

// haltReason builds the audited reason for a present halt file, preferring
// whatever the operator wrote inside it.
func haltReason(path string, isDir bool) string {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	if isDir {
		return fmt.Sprintf("halt switch present at %s (a directory) — new entries halted", abs)
	}
	note := ""
	if b, err := os.ReadFile(path); err == nil {
		if len(b) > maxReasonBytes {
			b = b[:maxReasonBytes]
		}
		if s := strings.TrimSpace(string(b)); s != "" {
			note = ": " + strings.Join(strings.Fields(s), " ")
		}
	}
	// A file we can stat but not read still halts — note simply stays empty.
	return fmt.Sprintf("halt switch present at %s — new entries halted%s", abs, note)
}
