package main

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sealBytes(t *testing.T, key, pt []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := sealStream(key, bytes.NewReader(pt), &b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func openBytes(key, ct []byte) ([]byte, error) {
	var b bytes.Buffer
	err := openStream(key, bytes.NewReader(ct), &b)
	return b.Bytes(), err
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b) //nolint:errcheck
	return b
}

// TestSealRoundTripsEveryChunkBoundary: sizes either side of the 1 MiB chunk,
// and an empty file (one empty final chunk), come back byte-identical.
func TestSealRoundTripsEveryChunkBoundary(t *testing.T) {
	key := randBytes(32)
	for _, n := range []int{0, 1, sealChunk - 1, sealChunk, sealChunk + 1, 3*sealChunk + 12345} {
		pt := randBytes(n)
		got, err := openBytes(key, sealBytes(t, key, pt))
		if err != nil || !bytes.Equal(got, pt) {
			t.Fatalf("size %d: round trip failed (err %v, %d bytes back)", n, err, len(got))
		}
	}
}

// TestOpenRefusesAlteredFiles: each way a stored backup can be wrong must fail
// to open rather than yield a short, long or altered plaintext.
func TestOpenRefusesAlteredFiles(t *testing.T) {
	key := randBytes(32)
	pt := randBytes(3*sealChunk + 12345)
	ct := sealBytes(t, key, pt)
	full := sealChunk + 16 // one sealed middle chunk on disk
	chunk := func(i int) []byte { return ct[sealHdrLen+i*full : sealHdrLen+(i+1)*full] }

	flipped := bytes.Clone(ct)
	flipped[len(flipped)/2] ^= 1
	swapped := bytes.Clone(ct)
	copy(swapped[sealHdrLen:], chunk(1))
	copy(swapped[sealHdrLen+full:], chunk(0))
	other := sealBytes(t, key, pt) // same key and plaintext, different file
	spliced := append(bytes.Clone(ct[:sealHdrLen+full]), other[sealHdrLen+full:]...)

	for name, bad := range map[string][]byte{
		"tampered byte":                    flipped,
		"last chunk dropped (boundary)":    ct[:sealHdrLen+3*full],
		"last chunk cut short":             ct[:len(ct)-1],
		"only the header":                  ct[:sealHdrLen],
		"two chunks swapped":               swapped,
		"chunks spliced from another file": spliced,
		"trailing data":                    append(bytes.Clone(ct), 0),
	} {
		if _, err := openBytes(key, bad); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
	if _, err := openBytes(randBytes(32), ct); err == nil {
		t.Error("wrong key: opened")
	}
}

// TestOpenWritesNothingOnFailure: the command writes through a temp file, so a
// failed open leaves neither the output nor a temp file behind.
func TestOpenWritesNothingOnFailure(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "k")
	if err := os.WriteFile(keyFile, []byte(strings.Repeat("ab", 32)+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in, enc, out := filepath.Join(dir, "in"), filepath.Join(dir, "in.enc"), filepath.Join(dir, "out")
	pt := randBytes(sealChunk + 7)
	if err := os.WriteFile(in, pt, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := offsiteSeal([]string{"-key", keyFile, "-in", in, "-out", enc}, false); err != nil {
		t.Fatal(err)
	}
	if err := offsiteSeal([]string{"-key", keyFile, "-in", enc, "-out", out}, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, pt) {
		t.Fatal("file round trip is not byte-identical")
	}
	os.Remove(out) //nolint:errcheck
	ct, _ := os.ReadFile(enc)
	if err := os.WriteFile(enc, ct[:len(ct)-5], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := offsiteSeal([]string{"-key", keyFile, "-in", enc, "-out", out}, true); err == nil {
		t.Fatal("a truncated file opened")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 3 {
		t.Fatalf("a failed open left files behind: %v", left)
	}
}

func TestReadBackupKeyIsStrict(t *testing.T) {
	dir := t.TempDir()
	for content, ok := range map[string]bool{
		strings.Repeat("0f", 32):               true,
		"  " + strings.Repeat("0F", 32) + "\n": true,
		strings.Repeat("0f", 31) + "0":         false, // 63 characters
		strings.Repeat("0f", 33):               false, // 33 bytes
		strings.Repeat("zz", 32):               false, // not hex
		"":                                     false,
	} {
		p := filepath.Join(dir, "k")
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := readBackupKey(p)
		if (err == nil) != ok {
			t.Errorf("key %q: err=%v, want ok=%v", content, err, ok)
		}
		if err != nil && content != "" && strings.Contains(err.Error(), content[:8]) {
			t.Errorf("error quotes the key file: %v", err)
		}
	}
}

// TestKeygenRefusesToOverwrite: a second keygen on the same path must not
// replace the key (that would orphan every backup sealed with it), and the
// key it wrote is usable and owner-only.
func TestKeygenRefusesToOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "backup.key")
	if err := offsiteKeygen([]string{"-key", p}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(p)
	if _, err := readBackupKey(p); err != nil {
		t.Fatalf("keygen wrote an unreadable key: %v", err)
	}
	if err := offsiteKeygen([]string{"-key", p}); err == nil {
		t.Fatal("a second keygen overwrote the key")
	}
	if again, _ := os.ReadFile(p); !bytes.Equal(first, again) {
		t.Fatal("the key changed")
	}
}

func TestCheckOwnerOnlyACL(t *testing.T) {
	const path = `C:\Users\me\.signaldeck\backup.key`
	ok := path + " PC\\me:(F)\n\nSuccessfully processed 1 files; Failed processing 0 files\n"
	if err := checkOwnerOnlyACL(ok, "me"); err != nil {
		t.Fatalf("owner-only ACL refused: %v", err)
	}
	for name, out := range map[string]string{
		"inherited extra entry": path + " PC\\me:(F)\n" + strings.Repeat(" ", len(path)) + " BUILTIN\\Users:(I)(RX)\n",
		"someone else":          path + " PC\\notme:(F)\n",
		"read only":             path + " PC\\me:(R)\n",
		"no entries":            "Successfully processed 1 files\n",
	} {
		if err := checkOwnerOnlyACL(out, "me"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
