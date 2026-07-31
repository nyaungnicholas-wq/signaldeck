//go:build !windows

package api

import (
	"os"
	"strings"
	"testing"
)

// writeExposedKey writes a syntactically valid anchor key that other local
// users can read, so LoadOrCreateSigner refuses it.
func writeExposedKey(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Repeat("ab", 32)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
