package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gic/internal/model"
	"gic/internal/session"
)

// Fetch implements `gic fetch --uid ... | --stdin -o <path>`.
func Fetch(args []string, g Globals) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	uidFlag := fs.String("uid", "", "comma-separated UIDs")
	stdin := fs.Bool("stdin", false, "read UIDs from a search-result JSON array on stdin")
	out := fs.String("o", "", "output file (single UID) or directory (multiple); \"-\" for stdout")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}
	if *out == "" {
		exitErr(fmt.Errorf("-o is required"))
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

	mailbox := session.ResolveMailbox(g.Mailbox)
	if _, err := cl.Select(mailbox); err != nil {
		exitErr(err)
	}
	frs, err := cl.UIDFetch(uids, model.FetchItems("full"))
	if err != nil {
		exitErr(err)
	}

	if *out == "-" {
		if len(frs) != 1 {
			exitErr(fmt.Errorf("-o - only supports a single message, got %d", len(frs)))
		}
		os.Stdout.Write(frs[0].Raw)
		return
	}

	if len(frs) == 1 && filepath.Ext(*out) != "" {
		if err := os.WriteFile(*out, frs[0].Raw, 0o644); err != nil {
			exitErr(err)
		}
		fmt.Printf("\u2713 Wrote %s\n", *out)
		return
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		exitErr(err)
	}
	for _, fr := range frs {
		p := filepath.Join(*out, fmt.Sprintf("%d.eml", fr.UID))
		if err := os.WriteFile(p, fr.Raw, 0o644); err != nil {
			exitErr(err)
		}
		fmt.Printf("\u2713 Wrote %s\n", p)
	}
}
