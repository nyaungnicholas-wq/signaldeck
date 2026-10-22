// Package tombstone keeps a write-ahead record of account deletions OUTSIDE
// the database (2026-10-22). A backup taken before a deletion still holds the
// account, so restoring it would bring the member back; replaying this log
// over the restored copy (sdmaint replay-tombstones) erases them again.
//
// One JSON line per deletion, {"uid":N,"email_sha256":"<hex>",
// "username_sha256":"<hex>"}, and nothing else: no address, no name, no time.
// users.id is reused, so a replay must match ALL three before it deletes
// anything; the username hash is what tells apart two accounts with no address
// that held the same id (2026-10-22 review).
package tombstone

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one deletion.
type Entry struct {
	UID            int64  `json:"uid"`
	EmailSHA256    string `json:"email_sha256"`
	UsernameSHA256 string `json:"username_sha256"`
}

// Path is SIGNALDECK_TOMBSTONE_LOG, else ~/.signaldeck/deletions.jsonl.
func Path() (string, error) {
	if p := os.Getenv("SIGNALDECK_TOMBSTONE_LOG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".signaldeck", "deletions.jsonl"), nil
}

// EmailHash is the hex SHA-256 of the address as the users table stores it
// (lowercased at sign-up; trimmed here as well). An account with no address
// hashes the empty string.
func EmailHash(email string) string {
	return sha256Hex(strings.ToLower(strings.TrimSpace(email)))
}

// UsernameHash is the hex SHA-256 of the username exactly as stored.
func UsernameHash(username string) string { return sha256Hex(username) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Append writes one line and fsyncs it before returning. Call it BEFORE the
// delete; if it fails, do not delete. Its errors name the file, never the
// address.
func Append(uid int64, username, email string) error {
	p, err := Path()
	if err != nil {
		return err
	}
	line, err := json.Marshal(Entry{UID: uid, EmailSHA256: EmailHash(email), UsernameSHA256: UsernameHash(username)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// A crash mid-write leaves a torn last line with no newline; start a fresh
	// line so this record is not glued onto it (Replay skips the torn one).
	line = append(line, '\n')
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, fi.Size()-1); err == nil && last[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	if _, err := f.Write(line); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}

// Replay calls fn for each entry in file order. A torn line (a record cut off
// mid-write by a crash, so its delete never ran) is skipped with a note on
// stderr; any other malformed line stops it, naming the line.
func Replay(path string, fn func(Entry) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e Entry
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		err := dec.Decode(&e)
		if errors.Is(err, io.ErrUnexpectedEOF) {
			fmt.Fprintf(os.Stderr, "%s line %d is a torn (incomplete) record; skipped\n", path, n)
			continue
		}
		if err == nil {
			_, err = hex.DecodeString(e.EmailSHA256 + e.UsernameSHA256)
		}
		if err != nil || e.UID <= 0 || len(e.EmailSHA256) != 64 || len(e.UsernameSHA256) != 64 {
			return fmt.Errorf("%s line %d is not a tombstone", path, n)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return sc.Err()
}
