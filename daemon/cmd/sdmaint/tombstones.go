package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
	"github.com/nyaungnicholas-wq/signaldeck/internal/tombstone"
)

// replayTombstones re-applies the account deletions in the tombstone log to a
// restored database (2026-10-22): a backup taken before a deletion still holds
// the account. users.id is reused, so an account is deleted only when its id
// matches AND the hash of its stored address, username and created_ts
// (tombstone.AccountHash) does: a later sign-up that reuses the id, name and
// address has a later created_ts and is left alone. The delete itself is
// store.DeleteAccount, the same tables and admin guard as the live path. Dry
// run unless -apply.
func replayTombstones(args []string) error {
	fs := flag.NewFlagSet("replay-tombstones", flag.ExitOnError)
	dbPath := fs.String("db", "", "the RESTORED database to replay deletions onto")
	defLog, _ := tombstone.Path()
	logPath := fs.String("log", defLog, "tombstone log (deletions.jsonl)")
	apply := fs.Bool("apply", false, "delete the matching accounts (default: report only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" || *logPath == "" || fs.NArg() > 0 {
		return errors.New("usage: sdmaint replay-tombstones -db RESTORED.db [-log deletions.jsonl] [-apply]")
	}
	// store.Open creates a missing file: a mistyped -db would replay onto a
	// fresh empty database and report "0 present", which reads as all clear.
	if _, err := os.Stat(*dbPath); err != nil {
		return fmt.Errorf("restored database: %w", err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	ctx := context.Background()

	var seen, matched, deleted int
	err = tombstone.Replay(*logPath, func(e tombstone.Entry) error {
		seen++
		u, ok, err := st.GetUserByID(ctx, e.UID)
		if err != nil || !ok {
			return err
		}
		email, created, ok, err := st.AccountEmailFor(ctx, e.UID, u.Username)
		if err != nil || !ok {
			return err
		}
		if tombstone.AccountHash(email, u.Username, created) != e.AccountSHA256 {
			return nil // the id now belongs to a different account, or a later one under the same name and address
		}
		if u.IsAdmin {
			fmt.Printf("skipped uid=%d: the operator account is never deleted by a replay\n", e.UID)
			return nil
		}
		matched++
		if !*apply {
			fmt.Printf("would delete uid=%d\n", e.UID)
			return nil
		}
		if err := st.DeleteAccount(ctx, e.UID, u.Username); err != nil {
			return fmt.Errorf("delete uid=%d: %w", e.UID, err)
		}
		deleted++
		fmt.Printf("deleted uid=%d\n", e.UID)
		return nil
	})
	if err != nil {
		return err
	}
	mode := ""
	if !*apply {
		mode = " (dry run: -apply deletes them)"
	}
	fmt.Printf("replay-tombstones: %d tombstones, %d deleted accounts present in this copy, %d deleted now%s\n", seen, matched, deleted, mode)
	return nil
}
