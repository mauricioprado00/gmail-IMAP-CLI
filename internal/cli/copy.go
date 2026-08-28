package cli

import (
	"flag"
	"fmt"
	"time"

	"gic/internal/account"
	"gic/internal/imapc"
	"gic/internal/model"
	"gic/internal/search"
)

// CopySummary is the JSON printed by `gic copy` on completion.
type CopySummary struct {
	Matched int          `json:"matched"`
	Copied  int          `json:"copied"`
	Skipped int          `json:"skipped"`
	Failed  int          `json:"failed"`
	Results []CopyResult `json:"results"`
}

// CopyResult reports the outcome for one message.
type CopyResult struct {
	UID       uint32 `json:"uid"`
	MessageID string `json:"messageId,omitempty"`
	Subject   string `json:"subject,omitempty"`
	Status    string `json:"status"` // copied|skipped|failed
	Error     string `json:"error,omitempty"`
}

// Copy implements `gic copy --from-account A --to-account B [filters...]`.
func Copy(args []string) {
	fs := flag.NewFlagSet("copy", flag.ExitOnError)
	fromAccount := fs.String("from-account", "", "source account alias|N (required)")
	toAccount := fs.String("to-account", "", "destination account alias|N (required)")
	fromMailbox := fs.String("from-mailbox", "[Gmail]/All Mail", "source mailbox to search")
	toMailbox := fs.String("to-mailbox", "INBOX", "destination mailbox to append into")
	applyLabel := fs.String("apply-label", "", "label to apply on the destination after copying")
	dryRun := fs.Bool("dry-run", false, "preview matches without copying anything")
	skipDup := fs.Bool("skip-duplicates", false, "skip messages whose Message-ID already exists in the destination mailbox")
	limit := fs.Int("limit", 0, "cap number of results (0 = unlimited)")

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

	if err := fs.Parse(normalizeTimeFilterArgs(args)); err != nil {
		exitErr(err)
	}
	if *fromAccount == "" || *toAccount == "" {
		exitErr(fmt.Errorf("--from-account and --to-account are required"))
	}

	f := search.Filters{From: from, To: to, Subject: subject, Body: body, Label: label, Since: *since, Until: until}
	if len(header) > 0 {
		f.Header = map[string][]string{}
		for _, h := range header {
			name, value := splitHeaderFlag(h)
			f.Header[name] = append(f.Header[name], value)
		}
	}

	srcAcct, err := account.Resolve(*fromAccount)
	if err != nil {
		exitErr(err)
	}
	dstAcct, err := account.Resolve(*toAccount)
	if err != nil {
		exitErr(err)
	}

	mode := "full"
	if *dryRun {
		mode = "light"
	}

	srcCl, err := imapc.Connect(srcAcct.IMAPHost, srcAcct.IMAPPort, srcAcct.Email, srcAcct.AppPassword, false)
	if err != nil {
		exitErr(fmt.Errorf("connecting to source %s: %w", srcAcct.Email, err))
	}
	defer srcCl.Logout()
	defer srcCl.Close()

	if _, err := srcCl.Select(*fromMailbox); err != nil {
		exitErr(fmt.Errorf("selecting source mailbox %q: %w", *fromMailbox, err))
	}
	criteria, err := f.Build()
	if err != nil {
		exitErr(err)
	}
	uids, err := srcCl.UIDSearch(criteria)
	if err != nil {
		exitErr(err)
	}
	if *limit > 0 && len(uids) > *limit {
		uids = uids[:*limit]
	}

	frs, err := srcCl.UIDFetch(uids, model.FetchItems(mode))
	if err != nil {
		exitErr(err)
	}

	summary := CopySummary{Matched: len(frs)}

	if *dryRun {
		for _, fr := range frs {
			m := model.FromFetch(fr, *fromMailbox, mode)
			summary.Results = append(summary.Results, CopyResult{UID: m.UID, MessageID: m.MessageID, Subject: m.Subject, Status: "matched"})
		}
		printJSON(summary)
		return
	}

	dstCl, err := imapc.Connect(dstAcct.IMAPHost, dstAcct.IMAPPort, dstAcct.Email, dstAcct.AppPassword, false)
	if err != nil {
		exitErr(fmt.Errorf("connecting to destination %s: %w", dstAcct.Email, err))
	}
	defer dstCl.Logout()
	defer dstCl.Close()

	if err := dstCl.Create(*toMailbox); err != nil {
		exitErr(fmt.Errorf("creating destination mailbox %q: %w", *toMailbox, err))
	}
	if _, err := dstCl.Select(*toMailbox); err != nil {
		exitErr(fmt.Errorf("selecting destination mailbox %q: %w", *toMailbox, err))
	}

	var appendedUIDs []uint32
	for _, fr := range frs {
		m := model.FromFetch(fr, *fromMailbox, mode)
		res := CopyResult{UID: m.UID, MessageID: m.MessageID, Subject: m.Subject}

		if *skipDup && m.MessageID != "" {
			exists, err := dstCl.UIDSearch(fmt.Sprintf(`HEADER Message-ID "<%s>"`, m.MessageID))
			if err == nil && len(exists) > 0 {
				res.Status = "skipped"
				summary.Skipped++
				summary.Results = append(summary.Results, res)
				continue
			}
		}

		date := time.Now()
		if t, err := time.Parse(time.RFC1123Z, m.Date); err == nil {
			date = t
		}
		if err := dstCl.Append(*toMailbox, nil, date, fr.Raw); err != nil {
			res.Status = "failed"
			res.Error = err.Error()
			summary.Failed++
			summary.Results = append(summary.Results, res)
			continue
		}
		res.Status = "copied"
		summary.Copied++
		summary.Results = append(summary.Results, res)
	}

	if *applyLabel != "" && len(appendedUIDs) == 0 {
		// Re-search the destination by Message-ID isn't reliable enough
		// right after APPEND (UID isn't returned by all servers); instead
		// look the newly appended messages up by their Message-IDs.
		for _, r := range summary.Results {
			if r.Status != "copied" || r.MessageID == "" {
				continue
			}
			found, err := dstCl.UIDSearch(fmt.Sprintf(`HEADER Message-ID "<%s>"`, r.MessageID))
			if err == nil {
				appendedUIDs = append(appendedUIDs, found...)
			}
		}
		if err := dstCl.StoreLabels(appendedUIDs, true, []string{*applyLabel}); err != nil {
			exitErr(fmt.Errorf("applying label %q on destination: %w", *applyLabel, err))
		}
	}

	printJSON(summary)
}

func splitHeaderFlag(h string) (name, value string) {
	for i := 0; i < len(h); i++ {
		if h[i] == '=' {
			return h[:i], h[i+1:]
		}
	}
	return h, ""
}
