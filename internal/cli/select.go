package cli

import (
	"fmt"
	"os"

	"gic/internal/account"
	"gic/internal/session"
)

// Select implements `gic select <alias|N>` — prints an export statement,
// meant to be eval'd by the shell-function wrapper from `gic shell-init`.
func Select(args []string) {
	if len(args) != 1 {
		exitErr(fmt.Errorf("usage: gic select <alias|N>"))
	}
	a, err := account.Resolve(args[0])
	if err != nil {
		exitErr(err)
	}
	session.PrintExport(session.EnvAccount, a.Alias)
	fmt.Fprintf(os.Stderr, "\u2713 Selected %s for this session\n", a.Email)
}

// Unselect implements `gic unselect`.
func Unselect() {
	session.PrintUnset(session.EnvAccount)
	fmt.Fprintln(os.Stderr, "\u2713 Cleared selected account for this session")
}

// MailboxSelect implements `gic mailbox select <name>`.
func MailboxSelect(args []string) {
	if len(args) != 1 {
		exitErr(fmt.Errorf("usage: gic mailbox select <name>"))
	}
	session.PrintExport(session.EnvMailbox, args[0])
	fmt.Fprintf(os.Stderr, "\u2713 Selected mailbox %q for this session\n", args[0])
}

// MailboxUnselect implements `gic mailbox unselect`.
func MailboxUnselect() {
	session.PrintUnset(session.EnvMailbox)
	fmt.Fprintln(os.Stderr, "\u2713 Cleared selected mailbox for this session (defaults to INBOX)")
}

// MailboxList implements `gic mailbox list`.
func MailboxList(g Globals) {
	_, cl, err := connect(g)
	if err != nil {
		exitErr(err)
	}
	defer cl.Logout()
	defer cl.Close()

	boxes, err := cl.List()
	if err != nil {
		exitErr(err)
	}
	selected := session.ResolveMailbox("")
	if g.Mailbox != "" {
		selected = g.Mailbox
	}
	for i, b := range boxes {
		if b.Name == selected {
			fmt.Printf("%d. %s (selected)\n", i+1, b.Name)
		} else {
			fmt.Printf("%d. %s\n", i+1, b.Name)
		}
	}
}
