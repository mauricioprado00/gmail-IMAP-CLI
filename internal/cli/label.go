package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"gic/internal/model"
	"gic/internal/session"
)

// LabelList implements `gic label list`.
func LabelList(g Globals) {
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
	for i, b := range boxes {
		fmt.Printf("%d. %s\n", i+1, b.Name)
	}
}

// LabelCreate implements `gic label create <name>`.
func LabelCreate(args []string, g Globals) {
	if len(args) != 1 {
		exitErr(fmt.Errorf("usage: gic label create <name>"))
	}
	_, cl, err := connect(g)
	if err != nil {
		exitErr(err)
	}
	defer cl.Logout()
	defer cl.Close()

	if err := cl.Create(args[0]); err != nil {
		exitErr(err)
	}
	fmt.Printf("\u2713 Label %q ready\n", args[0])
}

// readUIDsFromStdin reads a JSON array in the `search` output shape from
// stdin and returns the UIDs found in it.
func readUIDsFromStdin() ([]uint32, error) {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var msgs []model.Message
	if err := json.Unmarshal(b, &msgs); err != nil {
		return nil, fmt.Errorf("parsing stdin as search-result JSON: %w", err)
	}
	uids := make([]uint32, 0, len(msgs))
	for _, m := range msgs {
		uids = append(uids, m.UID)
	}
	return uids, nil
}

func labelApplyOrRemove(args []string, g Globals, add bool) {
	fs := flag.NewFlagSet("label", flag.ExitOnError)
	uidFlag := fs.String("uid", "", "comma-separated UIDs")
	stdin := fs.Bool("stdin", false, "read UIDs from a search-result JSON array on stdin")
	noCreate := fs.Bool("no-create", false, "do not auto-create the label if missing")
	if len(args) == 0 {
		exitErr(fmt.Errorf("usage: gic label apply|remove <name> [--uid a,b,c | --stdin]"))
	}
	if err := fs.Parse(args[1:]); err != nil {
		exitErr(err)
	}
	name := args[0]

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

	if add && !*noCreate {
		if err := cl.Create(name); err != nil {
			exitErr(err)
		}
	}
	if _, err := cl.Select(session.ResolveMailbox(g.Mailbox)); err != nil {
		exitErr(err)
	}
	if err := cl.StoreLabels(uids, add, []string{name}); err != nil {
		exitErr(err)
	}
	verb := "Applied"
	if !add {
		verb = "Removed"
	}
	fmt.Printf("\u2713 %s label %q on %d message(s)\n", verb, name, len(uids))
}

// LabelApply implements `gic label apply <name> --uid ... | --stdin`.
func LabelApply(args []string, g Globals) { labelApplyOrRemove(args, g, true) }

// LabelRemove implements `gic label remove <name> --uid ... | --stdin`.
func LabelRemove(args []string, g Globals) { labelApplyOrRemove(args, g, false) }
