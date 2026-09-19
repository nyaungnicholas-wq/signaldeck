package api

import "testing"

// A remote destination is off-machine BY CONSTRUCTION, and the volume test
// cannot describe it. filepath.VolumeName("s3://bucket/x") is "", so the old
// comparison answered "different volume" for an accidental reason — and would
// have answered identically for a typo. These pin the scheme check itself.
func TestIsRemoteOffsite(t *testing.T) {
	remote := []string{
		"s3://signaldeck-backups",
		"s3://signaldeck-backups/nightly",
		"S3://Signaldeck-Backups/Nightly", // scheme is case-insensitive
		"  s3://signaldeck-backups  ",     // env vars arrive with whitespace
	}
	for _, d := range remote {
		if !isRemoteOffsite(d) {
			t.Errorf("%q should count as a remote offsite destination", d)
		}
	}
	// The other direction matters more. Every one of these is a plausible
	// mis-configuration, and each must fall through to the volume test rather
	// than be waved through as "off-machine" on the strength of the word s3.
	local := []string{
		"",
		"C:\\Users\\Nicholas_N\\OneDrive\\SignalDeckBackups", // measured same volume
		"D:\\signaldeck-backups",
		"/c/Users/Nicholas_N/OneDrive",
		"signaldeck-backups",                                      // bare bucket name, not a URI
		"https://s3.console.aws.amazon.com/s3/buckets/signaldeck", // console URL
		"C:\\backups\\s3://weird",                                 // contains the scheme, not prefixed
	}
	for _, d := range local {
		if isRemoteOffsite(d) {
			t.Errorf("%q must NOT count as remote — it would report offsiteConfigured "+
				"true for a destination that never leaves the machine", d)
		}
	}
}
