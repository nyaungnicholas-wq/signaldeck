package api

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
)

// TestDeviceKeyIsReadNotReplaced pins the 2026-10-05 review's S3: a stored key
// is used as it is (the old path replaced it on any read error, voiding every
// device cookie), a first use creates exactly one, and the memo belongs to one
// store rather than leaking a key across stores.
func TestDeviceKeyIsReadNotReplaced(t *testing.T) {
	ctx := context.Background()
	a := mcpTestStore(t)
	stored := strings.Repeat("ab", 32)
	if err := a.SetMeta(ctx, metaDeviceKey, stored); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(Deps{St: a}.deviceKey(ctx)); got != stored {
		t.Fatalf("deviceKey = %s, want the stored key %s", got, stored)
	}

	b := mcpTestStore(t)
	first := Deps{St: b}.deviceKey(ctx)
	if first == nil || hex.EncodeToString(first) == stored {
		t.Fatalf("a second store got %x; want its own new key, not the first store's memo", first)
	}
	if v, err := b.GetMeta(ctx, metaDeviceKey); err != nil || v != hex.EncodeToString(first) {
		t.Fatalf("stored %q (err %v); want the key just handed out", v, err)
	}
	if again := (Deps{St: b}).deviceKey(ctx); hex.EncodeToString(again) != hex.EncodeToString(first) {
		t.Fatal("a second call changed the key")
	}
}
