package backup

import "testing"

// The two-writer conflict, pinned. MetaOffsiteDir is written unconditionally on
// every backup, so if this worker reported its (unused) local directory while
// the market-close script uploaded to S3, the next db-backup run would clobber
// the recorded destination and the dashboard would read offsiteConfigured:false
// beside a lastOffsiteTs from minutes ago. That contradictory pair is the
// documented 2026-08-11 defect.
func TestOffsiteDestinationPrefersS3(t *testing.T) {
	cases := []struct {
		name, dir, s3, want string
	}{
		{"s3 only", "", "s3://bucket/nightly", "s3://bucket/nightly"},
		{"dir only", "D:\\backups", "", "D:\\backups"},
		// The one that actually bites: BOTH set. S3 must win, because when it is
		// set this worker copies nowhere, and naming the directory would
		// describe a copy that is not being made.
		{"both set", "D:\\backups", "s3://bucket/nightly", "s3://bucket/nightly"},
		{"neither", "", "", ""},
	}
	for _, c := range cases {
		w := &Worker{OffsiteDir: c.dir, OffsiteS3: c.s3}
		if got := w.offsiteDestination(); got != c.want {
			t.Errorf("%s: offsiteDestination() = %q, want %q", c.name, got, c.want)
		}
	}
}

// When S3 is configured this worker must stand down AND must not stamp a
// freshness timestamp. offsite() returning early is only half of it: the
// freshness alarm has to keep measuring the real upload, so that if the
// market-close script stops running, the alarm still fires. A timestamp written
// here because "S3 is configured" would report a backup nobody took.
func TestOffsiteStandsDownForS3WithoutClaimingSuccess(t *testing.T) {
	w := &Worker{OffsiteDir: "D:\\backups", OffsiteS3: "s3://bucket/nightly"}
	// A nil store would panic on any SetMeta call, so this doubles as the
	// assertion that nothing is recorded: reaching a meta write here fails
	// loudly rather than silently stamping freshness.
	got := w.offsite(nil, "ignored.db") //nolint:staticcheck // nil ctx is the probe
	if got == "" {
		t.Fatal("offsite() returned nothing for an S3-configured worker")
	}
	for _, want := range []string{"delegated", "s3://bucket/nightly"} {
		if !contains(got, want) {
			t.Errorf("offsite() = %q, want it to mention %q so the log says WHO is "+
				"responsible for the upload", got, want)
		}
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
