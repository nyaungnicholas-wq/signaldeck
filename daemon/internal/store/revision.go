package store

import "sync/atomic"

// CODE REVISION STAMPING.
//
// Every published number is produced by some binary, and until this existed the
// database could not name which one for any row it held — not for a single one
// of the 247k prediction_ledger entries. "What code produced this?" is the first
// question a reviewer asks about a result, and "we cannot tell" is not an
// answer a verdict survives.
//
// The stamp is set once at daemon startup from lineage.RevisionStamp() (the
// binary's embedded vcs.revision, suffixed "+dirty" when vcs.modified) and
// written onto every row the daemon freezes. It is deliberately a plain string
// with an empty default: an unstamped row reads as UNKNOWN, never as clean.
var codeRevision atomic.Value // string

// SetCodeRevision records the running build's revision stamp. Called once from
// lineage.RecordBuildRevision at startup; the zero value ("") means the binary
// embedded no revision, which the grader treats as unresolvable.
func SetCodeRevision(rev string) { codeRevision.Store(rev) }

// CodeRevision returns the stamp written onto rows this process freezes.
func CodeRevision() string {
	if v, ok := codeRevision.Load().(string); ok {
		return v
	}
	return ""
}
