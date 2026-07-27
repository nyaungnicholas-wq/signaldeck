// signaldeck-mcp — the SignalDeck MCP server over stdio, plus the two
// operator commands the HTTP transport needs (mint a client key, generate a
// signing secret).
//
// Stdio is how Claude Code and Codex launch a local server: the client spawns
// this binary and speaks line-delimited JSON-RPC over its stdin/stdout. That
// caller is by construction the operator on their own machine, so it runs with
// the loopback-anonymous identity — still scoped, still budgeted, still
// audited, because "it is only me" is how the interesting bugs stay invisible.
//
// This binary does NOT listen on a socket. The HTTP transport lives inside the
// daemon (POST /mcp, mounted by internal/api/mcpmount.go) and is off unless
// SIGNALDECK_MCP_ENABLED is set. Nothing here deploys or exposes anything.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nyaungnicholas-wq/signaldeck/internal/config"
	"github.com/nyaungnicholas-wq/signaldeck/internal/mcp"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "new-secret":
			s, err := mcp.NewSecret()
			if err != nil {
				fatal(err)
			}
			fmt.Println(s)
			fmt.Fprintln(os.Stderr,
				"Set this as SIGNALDECK_MCP_SECRET. Every key already minted stops verifying if it changes.")
			return
		case "mint":
			if err := mint(os.Args[2:]); err != nil {
				fatal(err)
			}
			return
		case "-h", "--help", "help":
			usage()
			return
		}
	}
	if err := serveStdio(); err != nil && !errors.Is(err, io.EOF) {
		fatal(err)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `signaldeck-mcp — SignalDeck's MCP server (stdio) and key tooling

  signaldeck-mcp                      serve MCP over stdio (for Claude Code / Codex)
  signaldeck-mcp new-secret           print a fresh signing secret for SIGNALDECK_MCP_SECRET
  signaldeck-mcp mint -client NAME -scopes methodology,verdicts,record -ttl 720h
                                      mint a client key for the HTTP transport

Scopes: methodology, verdicts, record. A key with no scopes can call nothing.
`)
}

func mint(args []string) error {
	fs := flag.NewFlagSet("mint", flag.ExitOnError)
	client := fs.String("client", "", "client id (appears in every audit entry; revocable by this name)")
	scopes := fs.String("scopes", "", "comma-separated: methodology,verdicts,record")
	ttl := fs.Duration("ttl", 720*time.Hour, "key lifetime (keys must expire)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg := config.Load()
	if cfg.MCPSecret == "" {
		return errors.New("SIGNALDECK_MCP_SECRET is not set — run `signaldeck-mcp new-secret` first")
	}
	var list []string
	for _, s := range strings.Split(*scopes, ",") {
		if s = strings.TrimSpace(s); s != "" {
			list = append(list, s)
		}
	}
	key, err := mcp.MintKey(cfg.MCPSecret, *client, list, *ttl)
	if err != nil {
		return err
	}
	fmt.Println(key)
	fmt.Fprintf(os.Stderr,
		"client=%s scopes=%s expires=%s\nRevoke with SIGNALDECK_MCP_REVOKED=%s (by identity — re-minting will not resurrect it).\n",
		*client, strings.Join(list, ","), time.Now().Add(*ttl).UTC().Format(time.RFC3339), *client)
	return nil
}

func serveStdio() error {
	cfg := config.Load()
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", cfg.DBPath, err)
	}
	defer st.Close() //nolint:errcheck

	srv, err := mcp.New(mcp.Options{
		// Stdio is the operator's own process on the operator's own machine.
		// It is enabled here regardless of SIGNALDECK_MCP_ENABLED, which gates
		// the network-reachable HTTP transport — the thing that has compliance
		// implications. A local pipe does not.
		Enabled:            true,
		ReachablePrivately: true,
		Secret:             cfg.MCPSecret,
		AuditPath:          cfg.MCPAuditPath,
		DailyCallCap:       cfg.MCPDailyCalls,
		RevokedClients:     cfg.MCPRevoked,
		AnonymousScopes:    []string{mcp.ScopeMethodology, mcp.ScopeVerdicts, mcp.ScopeRecord},
	}, mcp.StoreSource{St: st},
		// A standalone stdio process has no daemon limiter to share, so it
		// carries its own — same bounds, same shape. It is not a no-op: the
		// daily aggregate and the anomaly detector run here too.
		newLocalLimiter(), constantTimeEqual)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return srv.ServeStdio(ctx, json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "signaldeck-mcp:", err)
	os.Exit(1)
}
