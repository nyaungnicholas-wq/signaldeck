package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tunBanner(host string) string {
	return `{"level":"info","message":"|  https://` + host + `.trycloudflare.com                    |"}` + "\n"
}

func tunFiller(n int) string {
	line := `{"level":"info","message":"GET /api/health 200 ms=1"}` + "\n"
	reps := (n + len(line) - 1) / len(line)
	return strings.Repeat(line, reps)
}

func tunWriteLog(t *testing.T, s string) string {
	path := filepath.Join(t.TempDir(), "tunnel.log")
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTunnelURLFindsBannerBeyondTheTail(t *testing.T) {
	log := tunBanner("first-banner") + `{"message":"GET /https://not-a-banner.trycloudflare.com/x 404"}` + "\n" + tunFiller(1300<<10)
	if strings.Index(log, "first-banner") >= 1024 {
		t.Fatalf("banner too far in")
	}
	if len(log) <= 1<<20 {
		t.Fatalf("log not large enough")
	}
	path := tunWriteLog(t, log)
	if got := lastTunnelURL(path); got != "https://first-banner.trycloudflare.com" {
		t.Fatalf("expected https://first-banner.trycloudflare.com, got %q", got)
	}
}

func TestTunnelURLNewestBannerWins(t *testing.T) {
	log := tunBanner("old-one") + tunFiller(600<<10) + tunBanner("new-one") + tunFiller(300<<10)
	path := tunWriteLog(t, log)
	if got := lastTunnelURL(path); got != "https://new-one.trycloudflare.com" {
		t.Fatalf("expected https://new-one.trycloudflare.com, got %q", got)
	}
}

func TestTunnelURLBannerStraddlingChunkBoundary(t *testing.T) {
	b := tunBanner("straddle")
	head := tunBanner("older") + tunFiller(10<<10)
	tailLen := (256 << 10) - len(b)/2
	tail := tunFiller(tailLen - 200)
	k := tailLen - len(tail) - 1
	if k < 1 {
		t.Fatalf("k < 1")
	}
	tail += strings.Repeat("x", k) + "\n"
	if len(tail) != tailLen {
		t.Fatalf("tail length mismatch")
	}
	log := head + b + tail
	boundary := len(log) - (256 << 10)
	if boundary <= len(head) || boundary >= len(head)+len(b) {
		t.Fatalf("boundary not in banner")
	}
	path := tunWriteLog(t, log)
	if got := lastTunnelURL(path); got != "https://straddle.trycloudflare.com" {
		t.Fatalf("expected https://straddle.trycloudflare.com, got %q", got)
	}
}

func TestTunnelURLNoBannerOrNoFile(t *testing.T) {
	log := tunFiller(300<<10) + `{"message":"see https://plain.trycloudflare.com"}` + "\n"
	path := tunWriteLog(t, log)
	if got := lastTunnelURL(path); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	missing := filepath.Join(t.TempDir(), "missing.log")
	if got := lastTunnelURL(missing); got != "" {
		t.Fatalf("expected empty for missing file, got %q", got)
	}
}
