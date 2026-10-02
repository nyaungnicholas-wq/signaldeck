package ledgeranchor

// Trusted anchor keys. Record.Verify only checks the key stored in
// the same database row; without an external trusted set a writer could re‑sign
// a regenerated chain with a fresh key. ParseKeySet returns an error on malformed
// lines because a silently dropped pin would turn honest anchors into tamper
// evidence. TrustedKeys additionally trusts the daemon's current key file, as
// anyone able to replace that file can also read it (the key‑holder case is
// defended against only by external publication).

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

//go:embed pinned_pubkeys.txt
var pinnedPubKeys string

// KeySet maps lowercase hex Ed25519 public keys to a bool value for fast lookup.
type KeySet map[string]bool

// Has reports whether pub (case‑insensitive) is present in the set.
func (k KeySet) Has(pub string) bool {
	return k[strings.ToLower(pub)]
}

// ParseKeySet converts a newline‑separated list of hex public keys (optionally
// trailing comments) into a KeySet. Empty input yields a non‑nil empty set.
// Any malformed line causes an error; lines are not silently skipped.
func ParseKeySet(text string) (KeySet, error) {
	set := make(KeySet)
	if strings.TrimSpace(text) == "" {
		return set, nil
	}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if comment := strings.IndexByte(line, '#'); comment >= 0 {
			line = line[:comment]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pub := strings.ToLower(line)
		b, err := hex.DecodeString(pub)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("ledgeranchor: pinned key line %d is not a %d-byte hex public key", i+1, ed25519.PublicKeySize)
		}
		set[pub] = true
	}
	return set, nil
}

// PinnedKeys returns the hard‑coded set of trusted keys from pinned_pubkeys.txt.
// It panics if the embedded file cannot be parsed (which should never happen).
func PinnedKeys() KeySet {
	set, err := ParseKeySet(pinnedPubKeys)
	if err != nil {
		panic(err)
	}
	return set
}

// TrustedKeys returns the pinned key set augmented with the daemon's current
// signing key (if readable and correctly permissioned). It never creates a key
// file.
func TrustedKeys() KeySet {
	k := PinnedKeys()
	if pub := currentPublicKey(DefaultKeyPath()); pub != "" {
		k[pub] = true
	}
	return k
}

// currentPublicKey returns the hex‑encoded Ed25519 public key for the key file at
// path, or an empty string if the file cannot be read, has incorrect permissions,
// or any other error occurs. It does not create the file.
func currentPublicKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if err := enforceKeyPerm(path, fi); err != nil {
		return ""
	}
	signer, err := loadSigner(path)
	if err != nil {
		return ""
	}
	return signer.PublicKeyHex()
}
