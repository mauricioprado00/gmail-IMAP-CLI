package cli

import (
	"flag"
	"fmt"

	"gic/internal/session"
)

// markSeenOrUnseen implements the shared body of `gic mark seen` and
// `gic mark unseen`: it resolves the target UIDs (from --uid or a
// search-result JSON array on stdin) and adds/removes the IMAP `\Seen`
// system flag on them.
func markSeenOrUnseen(args []string, g Globals, seen bool) {
	name := "unseen"
	if seen {
		name = "seen"
	}
	fs := flag.NewFlagSet("mark "+name, flag.ExitOnError)
	uidFlag := fs.String("uid", "", "comma-separated UIDs")
	stdin := fs.Bool("stdin", false, "read UIDs from a search-result JSON array on stdin")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}

	var uids []uint32
	var err error
	if *stdin {
		uids, err = readUIDsFromStdin()
	} else {
		uids, err = readUIDsFlag(*uidFlag)
	}
	if err != nil {
		exitErr(err)
	}
	if len(uids) == 0 {
		exitErr(fmt.Errorf("no UIDs given (use --uid or --stdin)"))
	}

	_, cl, err := connect(g)
	if err != nil {
		exitErr(err)
	}
	defer cl.Logout()
	defer cl.Close()

	if _, err := cl.Select(session.ResolveMailbox(g.Mailbox)); err != nil {
		exitErr(err)
	}
	if err := cl.StoreFlags(uids, seen, []string{`\Seen`}); err != nil {
		exitErr(err)
	}
	fmt.Printf("\u2713 Marked %d message(s) as %s\n", len(uids), name)
}

// MarkSeen implements `gic mark seen [--uid ... | --stdin]`
// (undocumented alias: `gic mark read`).
func MarkSeen(args []string, g Globals) { markSeenOrUnseen(args, g, true) }

// MarkUnseen implements `gic mark unseen [--uid ... | --stdin]`
// (undocumented alias: `gic mark unread`).
func MarkUnseen(args []string, g Globals) { markSeenOrUnseen(args, g, false) }
