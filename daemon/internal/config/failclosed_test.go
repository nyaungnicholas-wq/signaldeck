package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A security toggle must not be re-opened by a trailing comment.
//
// ops/GO-LIVE.md tells the operator to set SIGNALDECK_PUBLIC_READS=false. Both
// that flag and SIGNALDECK_OPEN_SIGNUP default to TRUE on a loopback bind, so
// before this was fixed an annotated line parsed to the literal string
// "false  # locked down", boolEnv did not recognise it, and the default won —
// the remediation silently did nothing.
func TestDotEnvStripsTrailingComment(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	body := "" +
		"SIGNALDECK_PUBLIC_READS=false  # locked down for the tunnel\n" +
		"SIGNALDECK_OPEN_SIGNUP=false\t# nobody else registers\n" +
		"PLAIN=true\n" +
		"# a whole-line comment\n" +
		"QUOTED=\"pa#ssword\"\n" +
		"HASH_NO_SPACE=abc#def\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := parseDotEnv(p)

	for k, want := range map[string]string{
		"SIGNALDECK_PUBLIC_READS": "false",
		"SIGNALDECK_OPEN_SIGNUP":  "false",
		"PLAIN":                   "true",
		// A '#' inside a quoted value is part of the secret, not a comment.
		"QUOTED": "pa#ssword",
		// No space before '#': not a comment either, so a secret containing a
		// bare '#' survives intact.
		"HASH_NO_SPACE": "abc#def",
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
}

// An unparseable value must fail closed, never fall through to an open default.
func TestBoolEnvFailsClosedOnGarbage(t *testing.T) {
	const k = "SIGNALDECK_TEST_BOOL_FAILCLOSED"
	cases := []struct {
		val  string
		def  bool
		want bool
		why  string
	}{
		{"", true, true, "unset must use the default"},
		{"", false, false, "unset must use the default"},
		{"false", true, false, "an explicit false must win over an open default"},
		{"true", false, true, "an explicit true must win over a closed default"},
		{"off", true, false, "off is false"},
		{"false  # locked down", true, false, "a value the parser cannot read must not mean open"},
		{"nope", true, false, "garbage must not mean open"},
	}
	for _, c := range cases {
		t.Setenv(k, c.val)
		if c.val == "" {
			os.Unsetenv(k) //nolint:errcheck
		}
		if got := boolEnv(k, c.def); got != c.want {
			t.Errorf("boolEnv(%q, def=%v) = %v, want %v — %s", c.val, c.def, got, c.want, c.why)
		}
	}
}
