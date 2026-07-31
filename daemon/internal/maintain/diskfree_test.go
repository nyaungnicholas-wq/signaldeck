package maintain

import "testing"

// The VACUUM headroom guard is fail-closed: it skips a multi-GB rewrite unless
// diskFree returns a verified positive figure. A platform implementation that
// silently returns 0, or errors on a perfectly good directory, would stall
// compaction forever — so assert both halves on whatever OS we build for.
func TestDiskFreeReportsPositiveSpaceForTempDir(t *testing.T) {
	free, err := diskFree(t.TempDir())
	if err != nil {
		t.Fatalf("diskFree(TempDir) returned error: %v", err)
	}
	if free <= 0 {
		t.Fatalf("diskFree(TempDir) = %d, want > 0", free)
	}
}

func TestDiskFreeErrorsOnMissingPath(t *testing.T) {
	if _, err := diskFree(t.TempDir() + "/definitely-not-here"); err == nil {
		t.Fatal("diskFree(missing path) = nil error, want an error so the guard fails closed")
	}
}
