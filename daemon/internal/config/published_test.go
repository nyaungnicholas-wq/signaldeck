package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPublishedReadsDotEnvLikeTheDaemon pins the deploy preflight's contract
// (2026-10-05 review S4): the preflight asks the binary (Published) instead of
// grepping daemon/.env, because the grep passed quoted-empty and junk values
// the daemon reads as unset, and refused a tunnel host in ALLOWED_HOSTS.
func TestPublishedReadsDotEnvLikeTheDaemon(t *testing.T) {
	cases := []struct {
		name, env string
		want      bool
	}{
		{"nothing", "SIGNALDECK_OPEN_SIGNUP=true\n", false},
		{"quoted empty url", "SIGNALDECK_PUBLIC_URL=\"\"\n", false},
		{"junk surface", "SIGNALDECK_PUBLIC_SURFACE=1x\n", false},
		{"later blank wins", "SIGNALDECK_PUBLIC_URL=https://a.example\nSIGNALDECK_PUBLIC_URL=\n", false},
		{"public url", "SIGNALDECK_PUBLIC_URL=https://a.example\n", true},
		{"tunnel log", "SIGNALDECK_TUNNEL_LOG=logs/quicktunnel.log\n", true},
		{"surface", "SIGNALDECK_PUBLIC_SURFACE=yes\n", true},
		{"tunnel host allowed", "SIGNALDECK_ALLOWED_HOSTS=127.0.0.1:8322,x.ngrok-free.dev\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "signaldeck", "daemon")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(tc.env), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"SIGNALDECK_PUBLIC_URL", "SIGNALDECK_TUNNEL_LOG", "SIGNALDECK_PUBLIC_SURFACE",
				"SIGNALDECK_ALLOWED_HOSTS", "SIGNALDECK_HTTP", "SIGNALDECK_OPEN_SIGNUP"} {
				t.Setenv(k, "") // real env wins over .env, and Load exports .env keys
			}
			t.Setenv("SIGNALDECK_ROOT", root)
			if got := Load().Published(); got != tc.want {
				t.Fatalf("Published() = %v, want %v for .env %q", got, tc.want, tc.env)
			}
		})
	}
}
