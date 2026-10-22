// Package tombstone keeps a write-ahead record of account deletions OUTSIDE
// the database (2026-10-22). A backup taken before a deletion still holds the
// account, so restoring it would bring the member back; replaying this log
// over the restored copy (sdmaint replay-tombstones) erases them again.
//
// One JSON line per deletion, {"uid":N,"email_sha256":"<hex>"}, and nothing
// else: no address, no name, no time. users.id is reused, so a replay must
// match BOTH fields before it deletes anything.
package tombstone

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Entry is one deletion.
type Entry struct {
	UID         int64  `json:"uid"`
	EmailSHA256 string `json:"email_sha256"`
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
// (lowercased at sign-up; trimmed here as well). A legacy account with no
// address hashes the empty string, so its tombstone matches on uid alone.
func EmailHash(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(sum[:])
}

// Append writes one line and fsyncs it before returning. Call it BEFORE the
// delete; if it fails, do not delete. Its errors name the file, never the
// address.
func Append(uid int64, email string) error {
	p, err := Path()
	if err != nil {
		return err
	}
	line, err := json.Marshal(Entry{UID: uid, EmailSHA256: EmailHash(email)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}

// Replay calls fn for each entry in file order. A malformed line stops it,
// naming the line, rather than being skipped.
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
		if err == nil {
			_, err = hex.DecodeString(e.EmailSHA256)
		}
		if err != nil || e.UID <= 0 || len(e.EmailSHA256) != 64 {
			return fmt.Errorf("%s line %d is not a tombstone", path, n)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return sc.Err()
}
