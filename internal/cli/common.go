// Package cli implements gic's subcommands.
package cli

import (
	"fmt"
	"os"

	"gic/internal/account"
	"gic/internal/imapc"
	"gic/internal/session"
)

// multiFlag is a flag.Value that accumulates repeated occurrences of the
// same flag into a slice (used for --from, --label, etc. so repeats mean
// OR, per docs/COMMANDS.md §4.1).
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprintf("%v", []string(*m)) }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

// Globals holds the flags every gic command accepts.
type Globals struct {
	Account string // -s
	Mailbox string // -m/--mailbox
	Verbose bool   // -v/--verbose
}

// exitErr prints the error to stderr and exits 1.
func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "gic: "+err.Error())
	os.Exit(1)
}

// connect resolves the selected account and opens+logs in an IMAP client.
func connect(g Globals) (*account.Account, *imapc.Client, error) {
	acct, err := session.ResolveAccount(g.Account)
	if err != nil {
		return nil, nil, err
	}
	cl, err := imapc.Connect(acct.IMAPHost, acct.IMAPPort, acct.Email, acct.AppPassword, g.Verbose)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", acct.Email, err)
	}
	return acct, cl, nil
}
