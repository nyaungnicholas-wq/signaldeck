// Package ledgeranchor turns the prediction ledger's INTERNAL CONSISTENCY into
// ANTERIORITY evidence by signing its head hashes with a key that does not live
// in the database.
//
// # The gap this closes
//
// The hash chain in internal/store proves that no stored row was edited,
// deleted or reordered: recomputing it reproduces every head, and a mutation
// breaks the recomputation at that exact seq. What it cannot prove is that the
// history existed when it says it did. An operator can delete every row, delete
// the in-DB verification checkpoint, and regenerate a fabricated chain through
// the same append path — the result verifies intact, because every anchor the
// chain has ever had lived inside the same file it is meant to police. That
// break is reproduced, not asserted, in
// store.TestLedger_ChainProvesConsistencyNotAnteriority.
//
// # What signing changes
//
// An anchor is a signature over (created_at, ledger_seq, ledger_count,
// head_hash). The private key is an Ed25519 seed in a 0600 file OUTSIDE the
// database; only the public key is stored in the DB, so verification needs no
// secret. To regenerate history and still produce an anchor over the OLD head,
// an operator would have to forge a signature — which is the property Ed25519
// provides. So anteriority holds for everything at or before the newest anchor
// whose head the current chain still reproduces.
//
// # What signing does NOT change (say it plainly)
//
//   - Nothing before the FIRST anchor is protected. The signature says "this
//     head existed at signing time", not "these rows were written earlier".
//   - An operator who holds the signing key can regenerate history AND re-sign
//     it. The signature raises the bar from "edit a SQLite file" to "also hold
//     the key", which is a real bar and not an absolute one. The only defence
//     against the key-holder is publication: Digest() emits a short string
//     suitable for posting somewhere that timestamps it independently, and a
//     posted digest cannot be retracted from a third party's record. This
//     package publishes nothing itself — that is an operator action.
//   - Key rotation is legitimate, so a new public key is not by itself proof of
//     fraud; it is a fact the verification surfaces and a reader must weigh
//     against whatever pubkey/digests were published externally.
package ledgeranchor

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// Alg names the signature scheme in every stored record, so a future
	// scheme change is a new value rather than a silent reinterpretation of
	// existing rows.
	Alg = "ed25519"
	// Version prefixes the signed bytes. It is INSIDE the signature: a record
	// signed under v1 cannot be replayed as a different version's payload.
	Version = "v1"
	// EnvKeyPath names the environment variable holding the signing key path.
	EnvKeyPath = "SIGNALDECK_LEDGER_ANCHOR_KEY"
	// domain separates these bytes from every other digest the platform signs
	// or hashes, so no payload from another chain can ever be mistaken for an
	// anchor payload.
	domain = "signaldeck-ledger-anchor"
)

var (
	// ErrNoKeyPath is returned when no key path could be resolved: with no
	// path there is no key outside the DB, so anchoring must fail rather than
	// fall back to something weaker.
	ErrNoKeyPath = errors.New("ledgeranchor: no signing key path (set " + EnvKeyPath + ")")
	// ErrKeyPermissions is returned for a key file readable by anyone but the
	// owner. A signing key any local process can read cannot support the claim
	// that regeneration requires the key, so this fails closed.
	ErrKeyPermissions = errors.New("ledgeranchor: signing key must be mode 0600 (owner-only)")
)

// Record is one signed commitment to a ledger head. Every field except Sig and
// PubKey is covered by the signature; PubKey identifies the verifying key and
// Sig is the signature itself.
type Record struct {
	CreatedAt   int64  `json:"createdAt"`   // unix seconds at signing
	LedgerSeq   int64  `json:"ledgerSeq"`   // the ledger row this head belongs to
	LedgerCount int64  `json:"ledgerCount"` // rows in the chain at signing time
	HeadHash    string `json:"headHash"`    // entry_hash of LedgerSeq
	Alg         string `json:"alg"`
	PubKey      string `json:"pubKey"` // hex ed25519 public key (32 bytes)
	Sig         string `json:"sig"`    // hex ed25519 signature over Message()
}

// Message is the canonical, byte-stable payload that gets signed. Fixed field
// order and decimal integers only — the same rule the ledger's own entry hash
// follows, so the bytes are identical on any machine and any Go version.
func Message(createdAt, ledgerSeq, ledgerCount int64, headHash string) string {
	var b strings.Builder
	b.WriteString(domain)
	b.WriteString("|")
	b.WriteString(Version)
	b.WriteString("|alg=")
	b.WriteString(Alg)
	b.WriteString("|created_at=")
	b.WriteString(strconv.FormatInt(createdAt, 10))
	b.WriteString("|ledger_seq=")
	b.WriteString(strconv.FormatInt(ledgerSeq, 10))
	b.WriteString("|ledger_count=")
	b.WriteString(strconv.FormatInt(ledgerCount, 10))
	b.WriteString("|head=")
	b.WriteString(headHash)
	return b.String()
}

// Message returns the signed bytes for this record.
func (r Record) Message() string {
	return Message(r.CreatedAt, r.LedgerSeq, r.LedgerCount, r.HeadHash)
}

// Verify reports whether Sig is a valid signature over Message() under PubKey.
// It answers only "was this record signed by the holder of that key" — NOT
// whether the ledger still reproduces HeadHash, which the store checks against
// the live chain.
func (r Record) Verify() bool {
	if r.Alg != Alg {
		return false
	}
	pub, err := hex.DecodeString(r.PubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(r.Sig)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(pub), []byte(r.Message()), sig)
}

// Digest is the short string an operator posts somewhere with an independent
// timestamp (a git commit, a social post, an email to an allocator). It binds
// the signed message, the signature and the public key, so a later claim that
// "the anchor said something else" is checkable against the posted copy — which
// is the ONLY defence against an operator who holds the signing key.
func (r Record) Digest() string {
	h := sha256.New()
	h.Write([]byte(r.Message()))
	h.Write([]byte("\x1e"))
	h.Write([]byte(r.Sig))
	h.Write([]byte("\x1e"))
	h.Write([]byte(r.PubKey))
	return hex.EncodeToString(h.Sum(nil))
}

// PublishLine renders the digest as one short, copy-pasteable line. It is
// deliberately not the full record: the full record is served by the API, and
// what is worth pinning externally is the smallest string that cannot be
// re-derived from a rewritten database.
func (r Record) PublishLine() string {
	return fmt.Sprintf("SIGNALDECK-LEDGER-ANCHOR %s seq=%d count=%d ts=%d digest=%s",
		Version, r.LedgerSeq, r.LedgerCount, r.CreatedAt, r.Digest())
}

// Signer holds the Ed25519 private key loaded from outside the database.
type Signer struct {
	priv ed25519.PrivateKey
	path string
}

// DefaultKeyPath resolves the signing key path: the env override if set, else
// ~/.signaldeck/ledger_anchor.key. The default deliberately sits OUTSIDE the
// data directory, because the database file is backed up and copied verbatim
// (it has been synced to iCloud); a key travelling with its own database would
// re-open exactly the hole anchoring exists to close. Returns "" when no home
// directory is resolvable, which callers must treat as "anchoring unavailable".
func DefaultKeyPath() string {
	if p := strings.TrimSpace(os.Getenv(EnvKeyPath)); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".signaldeck", "ledger_anchor.key")
}

// LoadOrCreateSigner loads the key at path, generating it on first use with
// 0600 permissions (and a 0700 parent directory). An existing key file with
// wider permissions is REFUSED rather than silently tightened: the file may
// already have been read, so the honest response is to stop and make the
// operator decide whether the key is still trustworthy.
func LoadOrCreateSigner(path string) (*Signer, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrNoKeyPath
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("ledgeranchor: key dir: %w", err)
		}
	}
	fi, err := os.Stat(path)
	switch {
	case err == nil:
		if fi.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("%w (%s is %04o)", ErrKeyPermissions, path, fi.Mode().Perm())
		}
		return loadSigner(path)
	case os.IsNotExist(err):
		return createSigner(path)
	default:
		return nil, err
	}
}

// loadSigner reads a hex-encoded 32-byte seed and expands it to a private key.
// Storing the SEED (not the 64-byte expanded key) keeps the file to one line
// and makes a truncated or corrupted file a parse error rather than a key that
// silently signs with garbage.
func loadSigner(path string) (*Signer, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied by design
	if err != nil {
		return nil, err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("ledgeranchor: key file %s is not hex: %w", path, err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("ledgeranchor: key file %s holds %d bytes, want %d", path, len(seed), ed25519.SeedSize)
	}
	return &Signer{priv: ed25519.NewKeyFromSeed(seed), path: path}, nil
}

// createSigner generates a new key. O_EXCL makes the create atomic: if another
// process wins the race the file is read back instead of being overwritten,
// because overwriting a key would orphan every anchor already signed with it.
func createSigner(path string) (*Signer, error) {
	_, priv, err := ed25519.GenerateKey(nil) // crypto/rand
	if err != nil {
		return nil, err
	}
	seed := priv.Seed()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // operator-supplied path
	if err != nil {
		if os.IsExist(err) {
			return loadSigner(path)
		}
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(seed) + "\n"); err != nil {
		f.Close() //nolint:errcheck
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return &Signer{priv: priv, path: path}, nil
}

// PublicKeyHex is what gets stored in the database so any reader can verify an
// anchor without access to the secret.
func (s *Signer) PublicKeyHex() string {
	return hex.EncodeToString(s.priv.Public().(ed25519.PublicKey))
}

// Path is the file the key was loaded from (reported, never the key itself).
func (s *Signer) Path() string { return s.path }

// Sign produces the signed anchor record for one ledger head.
func (s *Signer) Sign(createdAt, ledgerSeq, ledgerCount int64, headHash string) Record {
	r := Record{
		CreatedAt:   createdAt,
		LedgerSeq:   ledgerSeq,
		LedgerCount: ledgerCount,
		HeadHash:    headHash,
		Alg:         Alg,
		PubKey:      s.PublicKeyHex(),
	}
	r.Sig = hex.EncodeToString(ed25519.Sign(s.priv, []byte(r.Message())))
	return r
}
