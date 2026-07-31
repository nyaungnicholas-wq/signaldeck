package ledgeranchor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSigner_KeyIsCreatedOwnerOnlyAndStable: the key is generated on first use
// with 0600 in a 0700 directory, and a second load returns the SAME key. A
// regenerated key would silently orphan every anchor already signed — the
// anchors would stop verifying and read as tamper.
func TestSigner_KeyIsCreatedOwnerOnlyAndStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "anchor.key")

	sg, err := LoadOrCreateSigner(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// "Owner-only" means mode bits on Unix and a protected DACL on Windows, so
	// the assertion lives in keyperm_unix_test.go / keyperm_windows_test.go.
	assertKeyIsOwnerOnly(t, path)

	again, err := LoadOrCreateSigner(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.PublicKeyHex() != sg.PublicKeyHex() {
		t.Fatal("reload produced a different public key — the key is being regenerated, which orphans every existing anchor")
	}
	if len(sg.PublicKeyHex()) != 64 {
		t.Errorf("public key hex length = %d, want 64", len(sg.PublicKeyHex()))
	}
}

// TestSigner_RefusesWiderPermissions: a key any other local user can read
// cannot back the claim "regenerating history requires the key", so loading it
// fails closed instead of quietly tightening the mode — the file may already
// have been copied, and only the operator can judge that.
func TestSigner_RefusesWiderPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchor.key")
	if _, err := LoadOrCreateSigner(path); err != nil {
		t.Fatal(err)
	}
	widenKeyPermissions(t, path)
	_, err := LoadOrCreateSigner(path)
	if !errors.Is(err, ErrKeyPermissions) {
		t.Fatalf("err = %v, want ErrKeyPermissions", err)
	}
}

// TestSigner_RejectsUnusableKeyFile: a truncated or non-hex key file is an
// error, never a key that signs with whatever bytes were there. Silent
// acceptance would produce anchors nobody can verify.
func TestSigner_RejectsUnusableKeyFile(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"not hex", "this is not a key\n"},
		{"too short", "abcd\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "anchor.key")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreateSigner(path); err == nil {
				t.Fatal("loaded an unusable key file without error")
			}
		})
	}
	if _, err := LoadOrCreateSigner("  "); !errors.Is(err, ErrNoKeyPath) {
		t.Errorf("empty path err = %v, want ErrNoKeyPath", err)
	}
}

// TestRecord_SignatureBindsEveryClaimedField: the signature must cover the
// whole claim. If any field can be edited while the signature still verifies,
// an operator can restate what was anchored — which is the entire attack.
func TestRecord_SignatureBindsEveryClaimedField(t *testing.T) {
	sg, err := LoadOrCreateSigner(filepath.Join(t.TempDir(), "anchor.key"))
	if err != nil {
		t.Fatal(err)
	}
	rec := sg.Sign(1_700_000_000, 200, 200, "deadbeefcafe")
	if !rec.Verify() {
		t.Fatal("freshly signed record does not verify")
	}

	for _, tc := range []struct {
		name   string
		mutate func(r *Record)
	}{
		{"created_at", func(r *Record) { r.CreatedAt++ }},
		{"ledger_seq", func(r *Record) { r.LedgerSeq++ }},
		{"ledger_count", func(r *Record) { r.LedgerCount++ }},
		{"head_hash", func(r *Record) { r.HeadHash += "0" }},
		{"alg", func(r *Record) { r.Alg = "hmac" }},
		{"signature", func(r *Record) { r.Sig = strings.Repeat("00", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := rec
			tc.mutate(&m)
			if m.Verify() {
				t.Fatalf("record still verifies after editing %s — that field is not covered by the signature", tc.name)
			}
		})
	}

	// A different key must not verify this record.
	other, err := LoadOrCreateSigner(filepath.Join(t.TempDir(), "other.key"))
	if err != nil {
		t.Fatal(err)
	}
	swapped := rec
	swapped.PubKey = other.PublicKeyHex()
	if swapped.Verify() {
		t.Fatal("record verifies under an unrelated public key")
	}
}

// TestRecord_DigestBindsMessageSigAndKey: the digest is the only artifact that
// leaves the machine, so anything an operator could change without changing the
// digest is a hole in the external proof.
func TestRecord_DigestBindsMessageSigAndKey(t *testing.T) {
	sg, err := LoadOrCreateSigner(filepath.Join(t.TempDir(), "anchor.key"))
	if err != nil {
		t.Fatal(err)
	}
	rec := sg.Sign(1_700_000_000, 200, 200, "aaaa")
	base := rec.Digest()
	if len(base) != 64 {
		t.Fatalf("digest length = %d, want 64 hex chars", len(base))
	}
	for _, tc := range []struct {
		name   string
		mutate func(r *Record)
	}{
		{"head", func(r *Record) { r.HeadHash = "bbbb" }},
		{"seq", func(r *Record) { r.LedgerSeq = 201 }},
		{"sig", func(r *Record) { r.Sig = strings.Repeat("11", 64) }},
		{"pubkey", func(r *Record) { r.PubKey = strings.Repeat("22", 32) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := rec
			tc.mutate(&m)
			if m.Digest() == base {
				t.Fatalf("digest unchanged after editing %s — the published digest does not bind it", tc.name)
			}
		})
	}
	if !strings.Contains(rec.PublishLine(), base) {
		t.Errorf("publish line %q omits the digest", rec.PublishLine())
	}
}

// TestMessage_IsCanonical pins the signed bytes. The message format is the
// verification contract: if it changes, every previously published digest stops
// re-deriving, so a change here must be a deliberate new Version, not a drift.
func TestMessage_IsCanonical(t *testing.T) {
	got := Message(1_700_000_000, 200, 199, "abc123")
	want := "signaldeck-ledger-anchor|v1|alg=ed25519|created_at=1700000000|ledger_seq=200|ledger_count=199|head=abc123"
	if got != want {
		t.Errorf("message =\n%q\nwant\n%q", got, want)
	}
}

// TestDefaultKeyPath_PrefersEnvAndStaysOutsideTheDatabase: the env override
// wins, and the fallback is a dotdir in $HOME rather than anywhere beside the
// database file — a key travelling inside the backup it is meant to police
// would re-open exactly the hole anchoring closes.
func TestDefaultKeyPath_PrefersEnvAndStaysOutsideTheDatabase(t *testing.T) {
	t.Setenv(EnvKeyPath, "/tmp/explicit-anchor.key")
	if got := DefaultKeyPath(); got != "/tmp/explicit-anchor.key" {
		t.Fatalf("env override ignored: %q", got)
	}
	t.Setenv(EnvKeyPath, "")
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory on this machine")
	}
	if got := DefaultKeyPath(); got != filepath.Join(home, ".signaldeck", "ledger_anchor.key") {
		t.Errorf("default key path = %q, want ~/.signaldeck/ledger_anchor.key", got)
	}
}
