package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gic/internal/model"
	"gic/internal/search"
	"gic/internal/session"
)

var shorthandKeys = map[string]bool{
	"from": true, "to": true, "subject": true, "body": true, "label": true, "header": true,
	"since": true, "until": true,
}

// parseSearchArgs parses either the shorthand single-filter form
// (`search label accountant`) or the flag form (`search --from a --label b`).
// Returns the filters plus the shared options (mode/limit/all-mailboxes).
func parseSearchArgs(args []string) (search.Filters, string, int, bool, error) {
	var f search.Filters
	mode := "standard"
	limit := 0
	allMailboxes := false

	if len(args) > 0 && shorthandKeys[args[0]] && !strings.HasPrefix(args[0], "-") {
		key := args[0]
		if key == "header" {
			if len(args) < 3 {
				return f, mode, limit, allMailboxes, fmt.Errorf("usage: gic search header <name> <value>")
			}
			f.Header = map[string][]string{args[1]: {args[2]}}
			return f, mode, limit, allMailboxes, nil
		}
		if len(args) < 2 {
			return f, mode, limit, allMailboxes, fmt.Errorf("usage: gic search %s <value>", key)
		}
		switch key {
		case "from":
			f.From = []string{args[1]}
		case "to":
			f.To = []string{args[1]}
		case "subject":
			f.Subject = []string{args[1]}
		case "body":
			f.Body = []string{args[1]}
		case "label":
			f.Label = []string{args[1]}
		case "since":
			f.Since = strings.Join(args[1:], " ")
		case "until":
			f.Until = strings.Join(args[1:], " ")
		}
		return f, mode, limit, allMailboxes, nil
	}

	fs := flag.NewFlagSet("search", flag.ExitOnError)
	var from, to, subject, body, label, header multiFlag
	fs.Var(&from, "from", "match From header (repeatable = OR)")
	fs.Var(&to, "to", "match To header (repeatable = OR)")
	fs.Var(&subject, "subject", "match Subject (repeatable = OR)")
	fs.Var(&body, "body", "match body text (repeatable = OR)")
	fs.Var(&label, "label", "match Gmail label (repeatable = OR)")
	fs.Var(&header, "header", "match header, format NAME=VALUE (repeatable = OR within same NAME)")
	since := fs.String("since", "", "only messages since this date ("+dateFilterFormat+")")
	var until string
	fs.StringVar(&until, "until", "", "only messages before this date ("+dateFilterFormat+")")
	fs.StringVar(&until, "before", "", "alias for --until")
	fs.StringVar(&mode, "mode", "standard", "retrieval mode: light|standard|full")
	fs.IntVar(&limit, "limit", 0, "cap number of results (0 = unlimited)")
	fs.BoolVar(&allMailboxes, "all-mailboxes", false, "search across all mailboxes")
	if err := fs.Parse(normalizeTimeFilterArgs(args)); err != nil {
		return f, mode, limit, allMailboxes, err
	}

	f.From, f.To, f.Subject, f.Body, f.Label = from, to, subject, body, label
	f.Since, f.Until = *since, until
	if len(header) > 0 {
		f.Header = map[string][]string{}
		for _, h := range header {
			parts := strings.SplitN(h, "=", 2)
			if len(parts) != 2 {
				return f, mode, limit, allMailboxes, fmt.Errorf("--header must be NAME=VALUE, got %q", h)
			}
			f.Header[parts[0]] = append(f.Header[parts[0]], parts[1])
		}
	}
	return f, mode, limit, allMailboxes, nil
}

// Search implements `gic search ...`.
func Search(args []string, g Globals) {
	f, mode, limit, allMailboxes, err := parseSearchArgs(args)
	if err != nil {
		exitErr(err)
	}
	msgs, err := runSearch(g, f, mode, limit, allMailboxes)
	if err != nil {
		exitErr(err)
	}
	printJSON(msgs)
}

// runSearch executes the search and returns the parsed messages; shared by
// Search and Copy.
func runSearch(g Globals, f search.Filters, mode string, limit int, allMailboxes bool) ([]*model.Message, error) {
	if mode != "light" && mode != "standard" && mode != "full" {
		return nil, fmt.Errorf("invalid --mode %q (want light|standard|full)", mode)
	}
	_, cl, err := connect(g)
	if err != nil {
		return nil, err
	}
	defer cl.Logout()
	defer cl.Close()

	mailbox := "[Gmail]/All Mail"
	if !allMailboxes {
		mailbox = session.ResolveMailbox(g.Mailbox)
	}
	if _, err := cl.Select(mailbox); err != nil {
		return nil, fmt.Errorf("selecting mailbox %q: %w", mailbox, err)
	}

	criteria, err := f.Build()
	if err != nil {
		return nil, err
	}
	uids, err := cl.UIDSearch(criteria)
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(uids) > limit {
		uids = uids[:limit]
	}
	if len(uids) == 0 {
		return nil, nil
	}

	frs, err := cl.UIDFetch(uids, model.FetchItems(mode))
	if err != nil {
		return nil, err
	}
	msgs := make([]*model.Message, 0, len(frs))
	for _, fr := range frs {
		msgs = append(msgs, model.FromFetch(fr, mailbox, mode))
	}
	return msgs, nil
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if v == nil {
		fmt.Println("[]")
		return
	}
	if err := enc.Encode(v); err != nil {
		exitErr(err)
	}
}

// readUIDsFlag parses a comma-separated UID list flag value, e.g. "1,2,3".
func readUIDsFlag(s string) ([]uint32, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseUint(strings.TrimSpace(p), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid --uid value %q: %w", p, err)
		}
		out = append(out, uint32(n))
	}
	return out, nil
}
