package main

// Off-site backup encryption (2026-10-22). The nightly GitHub release asset was
// a plain gzip of the whole database, protected only by the repo being private.
// offsite-seal / offsite-open wrap it in chunked AES-256-GCM, streaming 1 MiB at
// a time so the 1.3 GB file never sits in memory.
//
// Format: header = "SDBK1" | version (1 byte) | random nonce prefix (7 bytes),
// then chunks of up to 1 MiB of plaintext, each sealed with
// nonce = prefix | counter (uint32, big-endian) | last flag (1 byte)
// and the header as additional data. The counter stops reordering, the header
// stops a chunk moving between files, and the last flag stops truncation at a
// chunk boundary: open decides the flag from the chunk's POSITION (short, or
// followed by EOF), so a cut file, or one with bytes appended, fails to
// authenticate instead of opening short or long.

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	sealMagic   = "SDBK1"
	sealVersion = 1
	sealHdrLen  = len(sealMagic) + 1 + 7
	sealChunk   = 1 << 20
)

func defaultBackupKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".signaldeck", "backup.key")
}

// readBackupKey accepts exactly 64 hex characters, surrounding whitespace
// trimmed. Its errors never quote the file's content.
func readBackupKey(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%s is not a backup key: want exactly 64 hex characters (32 bytes)", path)
	}
	return key, nil
}

func newBackupAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// chunkNonce fills nonce with prefix | counter | last flag.
func chunkNonce(nonce, prefix []byte, ctr uint32, last bool) []byte {
	copy(nonce, prefix)
	binary.BigEndian.PutUint32(nonce[7:11], ctr)
	nonce[11] = 0
	if last {
		nonce[11] = 1
	}
	return nonce
}

// atEOF reports whether br has nothing left to read.
func atEOF(br *bufio.Reader) (bool, error) {
	if _, err := br.Peek(1); err == io.EOF {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return false, nil
}

func sealStream(key []byte, r io.Reader, w io.Writer) error {
	aead, err := newBackupAEAD(key)
	if err != nil {
		return err
	}
	hdr := make([]byte, sealHdrLen)
	copy(hdr, sealMagic)
	hdr[len(sealMagic)] = sealVersion
	prefix := hdr[len(sealMagic)+1:]
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	br := bufio.NewReader(r)
	buf := make([]byte, sealChunk)
	out := make([]byte, 0, sealChunk+aead.Overhead())
	nonce := make([]byte, aead.NonceSize())
	for ctr := uint32(0); ; ctr++ {
		n, err := io.ReadFull(br, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return err
		}
		last := n < sealChunk // an empty input seals one empty final chunk
		if !last {
			if last, err = atEOF(br); err != nil {
				return err
			}
		}
		out = aead.Seal(out[:0], chunkNonce(nonce, prefix, ctr, last), buf[:n], hdr)
		if _, err := w.Write(out); err != nil {
			return err
		}
		if last {
			return nil
		}
		if ctr == ^uint32(0) {
			return errors.New("input too large for one sealed file (counter would repeat a nonce)")
		}
	}
}

func openStream(key []byte, r io.Reader, w io.Writer) error {
	aead, err := newBackupAEAD(key)
	if err != nil {
		return err
	}
	hdr := make([]byte, sealHdrLen)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return errors.New("not a sealed backup: the header is missing or short")
	}
	if string(hdr[:len(sealMagic)]) != sealMagic || hdr[len(sealMagic)] != sealVersion {
		return errors.New("not a sealed backup: unknown magic or version")
	}
	prefix := hdr[len(sealMagic)+1:]
	br := bufio.NewReader(r)
	buf := make([]byte, sealChunk+aead.Overhead())
	out := make([]byte, 0, sealChunk)
	nonce := make([]byte, aead.NonceSize())
	for ctr := uint32(0); ; ctr++ {
		n, err := io.ReadFull(br, buf)
		if err == io.EOF {
			return errors.New("sealed backup is truncated: no final chunk")
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			return err
		}
		last := n < len(buf)
		if !last {
			if last, err = atEOF(br); err != nil {
				return err
			}
		}
		out, err = aead.Open(out[:0], chunkNonce(nonce, prefix, ctr, last), buf[:n], hdr)
		if err != nil {
			return fmt.Errorf("chunk %d does not authenticate: wrong key, or the file was tampered with, reordered, truncated or extended", ctr)
		}
		if _, err := w.Write(out); err != nil {
			return err
		}
		if last {
			return nil
		}
	}
}

// writeFileAtomic streams into a temp file beside out and renames it into
// place only after fn, the flush and the fsync all succeed, so a failed open
// never leaves a partial file under the real name.
func writeFileAtomic(out string, fn func(io.Writer) error) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(out), ".sdbk-*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()           //nolint:errcheck
			os.Remove(tmp.Name()) //nolint:errcheck
		}
	}()
	bw := bufio.NewWriterSize(tmp, sealChunk)
	if err = fn(bw); err != nil {
		return err
	}
	if err = bw.Flush(); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), out)
}

// offsiteSeal runs offsite-seal (open=false) or offsite-open (open=true).
func offsiteSeal(args []string, open bool) error {
	name, stream := "offsite-seal", sealStream
	if open {
		name, stream = "offsite-open", openStream
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	keyPath := fs.String("key", defaultBackupKeyPath(), "backup key file (64 hex characters)")
	in := fs.String("in", "", "input file")
	out := fs.String("out", "", "output file (written to a temp file, renamed only on success)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" || fs.NArg() > 0 {
		return errors.New("usage: sdmaint " + name + " -key K -in X -out Y")
	}
	key, err := readBackupKey(*keyPath)
	if err != nil {
		return err
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	return writeFileAtomic(*out, func(w io.Writer) error { return stream(key, f, w) })
}

// offsiteKeygen writes a new random key as hex, never over an existing file,
// readable by the current user only. It never prints the key.
func offsiteKeygen(args []string) error {
	fs := flag.NewFlagSet("offsite-keygen", flag.ExitOnError)
	keyPath := fs.String("key", defaultBackupKeyPath(), "where to write the new key (an existing file is never overwritten)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyPath == "" || fs.NArg() > 0 {
		return errors.New("usage: sdmaint offsite-keygen [-key PATH]")
	}
	if err := os.MkdirAll(filepath.Dir(*keyPath), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(*keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; refusing to overwrite a backup key (losing it loses every backup sealed with it)", *keyPath)
	} else if err != nil {
		return err
	}
	key := make([]byte, 32)
	_, err = rand.Read(key)
	if err == nil {
		_, err = f.WriteString(hex.EncodeToString(key) + "\n")
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = restrictToOwner(*keyPath)
	}
	if err != nil {
		os.Remove(*keyPath) //nolint:errcheck // nothing has been sealed with it yet
		return fmt.Errorf("key NOT kept: %w", err)
	}
	fmt.Printf("offsite-keygen: wrote a new 32-byte key to %s (owner-only access verified)\n", *keyPath)
	fmt.Println("Copy it to OFFLINE storage now: without it no sealed off-site backup can be restored.")
	return nil
}

// restrictToOwner leaves path readable by the current user only, and checks.
func restrictToOwner(path string) error {
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if st.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s is open to other users (%v)", path, st.Mode().Perm())
		}
		return nil
	}
	user := os.Getenv("USERNAME")
	if user == "" {
		return errors.New("USERNAME is not set; cannot restrict the key file")
	}
	if out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", user+":F").CombinedOutput(); err != nil {
		return fmt.Errorf("icacls: %v: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("icacls", path).Output()
	if err != nil {
		return fmt.Errorf("icacls check: %v", err)
	}
	return checkOwnerOnlyACL(string(out), user)
}

// checkOwnerOnlyACL wants exactly one access entry in icacls output: the
// current user with full control (DOMAIN\user:(F) or user:(F)).
func checkOwnerOnlyACL(icacls, user string) error {
	var aces []string
	for _, line := range strings.Split(icacls, "\n") {
		if strings.Contains(line, ":(") {
			aces = append(aces, strings.ToLower(strings.TrimSpace(line)))
		}
	}
	want := strings.ToLower(user + ":(F)")
	if len(aces) != 1 || !(aces[0] == want || strings.HasSuffix(aces[0], `\`+want) || strings.HasSuffix(aces[0], " "+want)) {
		return fmt.Errorf("key file access is not owner-only: %d entries %q", len(aces), aces)
	}
	return nil
}
