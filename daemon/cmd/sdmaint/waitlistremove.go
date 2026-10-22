package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/nyaungnicholas-wq/signaldeck/internal/api"
	"github.com/nyaungnicholas-wq/signaldeck/internal/store"
)

// waitlistRemove removes one address from the waitlist on its owner's request.
// Dry run unless -apply. The address is normalised exactly as POST
// /api/waitlist stored it, and it is never echoed back.
func waitlistRemove(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("waitlist-remove", flag.ContinueOnError)
	dbPath := fs.String("db", "data/signaldeck.db", "path to signaldeck.db")
	raw := fs.String("email", "", "the address to remove")
	apply := fs.Bool("apply", false, "delete it (default: dry run, report only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	email, ok := api.NormaliseEmail(*raw)
	if !ok {
		return errors.New("-email is missing or not a valid address")
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close() //nolint:errcheck
	found, err := st.RemoveWaitlist(context.Background(), email, *apply)
	if err != nil {
		return err
	}
	msg := "removed 1 address from the waitlist"
	switch {
	case !found:
		msg = "not on the waitlist: nothing to remove"
	case !*apply:
		msg = "on the waitlist: would remove 1 address (dry run; pass -apply)"
	}
	_, err = fmt.Fprintln(out, msg)
	return err
}
