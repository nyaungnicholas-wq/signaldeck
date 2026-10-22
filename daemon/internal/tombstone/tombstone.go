// Package tombstone keeps a write-ahead record of account deletions OUTSIDE
// the database (2026-10-22). A backup taken before a deletion still holds the
// account, so restoring it would bring the member back; replaying this log
// over the restored copy (sdmaint replay-tombstones) erases them again.
//
// One JSON line per deletion, {"uid":N,"account_sha256":"<hex>"}, and nothing
// else: no address, no name, no time in the clear. The hash covers the
// account's address, username and sign-up time together (AccountHash), so it
// names one account instance: users.id is reused, and a person who deletes and
// signs up again under the same id, name and address gets a later created_ts,
// so a replay leaves the new account alone (2026-10-22 review).
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
	"strconv"
	"strings"
)

// Entry is one deletion.
type Entry struct {
	UID           int64  `json:"uid"`
	AccountSHA256 string `json:"account_sha256"`
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

// AccountHash is the hex SHA-256 of normalisedEmail + "\n" + username + "\n" +
// decimal(created_ts), each as the users row stores it. The address is
// lowercased at sign-up and trimmed here as well; an account with no address
// contributes the empty string.
func AccountHash(email, username string, createdTs int64) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email)) + "\n" + username + "\n" + strconv.FormatInt(createdTs, 10)))
	return hex.EncodeToString(sum[:])
}

// Append writes one line and fsyncs it before returning. Call it BEFORE the
// delete; if it fails, do not delete. Its errors name the file, never the
// address.
func Append(uid int64, username, email string, createdTs int64) error {
	p, err := Path()
	if err != nil {
		return err
	}
	line, err := json.Marshal(Entry{UID: uid, AccountSHA256: AccountHash(email, username, createdTs)})
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
			_, err = hex.DecodeString(e.AccountSHA256)
		}
		if err != nil || e.UID <= 0 || len(e.AccountSHA256) != 64 {
			// The pre-release email_sha256/username_sha256 lines land here too:
			// none exist in production, so they are refused, not converted.
			return fmt.Errorf(`%s line %d is not a tombstone: want {"uid":N,"account_sha256":"<64 hex>"} and nothing else`, path, n)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return sc.Err()
}
