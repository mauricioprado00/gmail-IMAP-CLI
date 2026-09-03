package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"time"

	"gic/internal/imapc"
	"gic/internal/mimeedit"
	"gic/internal/model"
	"gic/internal/session"
)

// AttachmentRemoveSummary is the JSON printed by `gic attachment remove`.
type AttachmentRemoveSummary struct {
	Matched            int                      `json:"matched"`
	AttachmentsRemoved int                      `json:"attachmentsRemoved"`
	Skipped            int                      `json:"skipped"`
	Failed             int                      `json:"failed"`
	DryRun             bool                     `json:"dryRun"`
	Results            []AttachmentRemoveResult `json:"results"`
}

// AttachmentRemoveResult reports the outcome for one source message.
type AttachmentRemoveResult struct {
	UID            uint32                `json:"uid"`
	ReplacementUID uint32                `json:"replacementUid,omitempty"`
	Subject        string                `json:"subject,omitempty"`
	Status         string                `json:"status"`
	Attachments    []mimeedit.Attachment `json:"attachments,omitempty"`
	Error          string                `json:"error,omitempty"`
}

// AttachmentRemove implements `gic attachment remove`.
func AttachmentRemove(args []string, g Globals) {
	fs := flag.NewFlagSet("attachment remove", flag.ExitOnError)
	uidFlag := fs.String("uid", "", "comma-separated message UIDs")
	stdin := fs.Bool("stdin", false, "read UIDs from a search-result JSON array on stdin")
	all := fs.Bool("all", false, "remove every attachment")
	dryRun := fs.Bool("dry-run", false, "report matching attachments without changing messages")
	var names multiFlag
	fs.Var(&names, "name", "exact attachment filename to remove (repeatable)")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}
	if *all == (len(names) > 0) {
		exitErr(fmt.Errorf("choose exactly one of --all or one or more --name flags"))
	}
	if *stdin && *uidFlag != "" {
		exitErr(fmt.Errorf("--uid and --stdin are mutually exclusive"))
	}

	mailbox := session.ResolveMailbox(g.Mailbox)
	var uids []uint32
	var err error
	if *stdin {
		uids, err = readAttachmentUIDsFromStdin(mailbox)
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

	var allMail, trash string
	if !*dryRun {
		uidPlus, err := cl.HasCapability("UIDPLUS")
		if err != nil {
			exitErr(fmt.Errorf("checking UIDPLUS support: %w", err))
		}
		if !uidPlus {
			exitErr(fmt.Errorf("server does not support UIDPLUS; attachment removal cannot safely replace messages"))
		}
		allMail, trash, err = gmailDeletionMailboxes(cl)
		if err != nil {
			exitErr(err)
		}
	}

	if _, err := cl.Select(mailbox); err != nil {
		exitErr(err)
	}

	nameSet := make(map[string]bool, len(names))
	for _, name := range names {
		nameSet[strings.ToLower(name)] = true
	}
	matchName := func(name string) bool {
		return *all || nameSet[strings.ToLower(name)]
	}

	summary := AttachmentRemoveSummary{Matched: len(uids), DryRun: *dryRun}
	for _, uid := range uids {
		frs, err := cl.UIDFetch([]uint32{uid}, model.FetchItems("full"))
		if err != nil {
			summary.Failed++
			summary.Results = append(summary.Results, AttachmentRemoveResult{
				UID: uid, Status: "failed", Error: err.Error(),
			})
			continue
		}
		if len(frs) != 1 {
			summary.Failed++
			summary.Results = append(summary.Results, AttachmentRemoveResult{
				UID: uid, Status: "failed", Error: "message UID not found in selected mailbox",
			})
			continue
		}
		fr := frs[0]
		message := model.FromFetch(fr, mailbox, "full")
		result := AttachmentRemoveResult{UID: fr.UID, Subject: message.Subject}
		rewritten, removed, marker, err := mimeedit.RemoveAttachments(fr.Raw, matchName)
		result.Attachments = removed
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			summary.Failed++
			summary.Results = append(summary.Results, result)
			continue
		}
		if len(removed) == 0 {
			result.Status = "skipped"
			summary.Skipped++
			summary.Results = append(summary.Results, result)
			continue
		}
		if *dryRun {
			result.Status = "would-remove"
			summary.AttachmentsRemoved += len(removed)
			summary.Results = append(summary.Results, result)
			continue
		}

		replacement, duplicates, err := findReusableReplacements(cl, fr.UID, message.MessageID, matchName)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			summary.Failed++
			summary.Results = append(summary.Results, result)
			continue
		}
		replacementUID, err := replaceMessageAttachments(
			cl, mailbox, allMail, trash, fr, message.Date, marker, rewritten,
			replacement, duplicates,
		)
		result.ReplacementUID = replacementUID
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			summary.Failed++
		} else {
			result.Status = "removed"
			summary.AttachmentsRemoved += len(removed)
		}
		summary.Results = append(summary.Results, result)
		if _, err := cl.Select(mailbox); err != nil {
			exitErr(fmt.Errorf("reselecting mailbox %q: %w", mailbox, err))
		}
	}
	printJSON(summary)
}

func readAttachmentUIDsFromStdin(mailbox string) ([]uint32, error) {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	var messages []model.Message
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, fmt.Errorf("parsing stdin as search-result JSON: %w", err)
	}
	uids := make([]uint32, 0, len(messages))
	for _, message := range messages {
		if message.Mailbox != "" && !strings.EqualFold(message.Mailbox, mailbox) {
			return nil, fmt.Errorf(
				"stdin UID %d belongs to mailbox %q, not selected mailbox %q",
				message.UID, message.Mailbox, mailbox,
			)
		}
		uids = append(uids, message.UID)
	}
	return uids, nil
}

func gmailDeletionMailboxes(cl *imapc.Client) (allMail, trash string, err error) {
	mailboxes, err := cl.List()
	if err != nil {
		return "", "", fmt.Errorf("listing Gmail special-use mailboxes: %w", err)
	}
	for _, mailbox := range mailboxes {
		for _, mailboxFlag := range mailbox.Flags {
			switch strings.ToLower(mailboxFlag) {
			case `\all`:
				allMail = mailbox.Name
			case `\trash`:
				trash = mailbox.Name
			}
		}
	}
	if allMail == "" || trash == "" {
		return "", "", fmt.Errorf("Gmail All Mail and Trash special-use mailboxes are required")
	}
	return allMail, trash, nil
}

func findReusableReplacements(cl *imapc.Client, sourceUID uint32, originalMessageID string, matchName func(string) bool) (*imapc.FetchResult, []imapc.FetchResult, error) {
	if originalMessageID == "" {
		return nil, nil, nil
	}
	uids, err := cl.UIDSearch(fmt.Sprintf(`HEADER X-GIC-Original-Message-ID "<%s>"`, originalMessageID))
	if err != nil {
		return nil, nil, fmt.Errorf("searching for an earlier replacement: %w", err)
	}
	var candidates []imapc.FetchResult
	for _, uid := range uids {
		if uid == sourceUID {
			continue
		}
		frs, err := cl.UIDFetch([]uint32{uid}, model.FetchItems("full"))
		if err != nil || len(frs) != 1 {
			continue
		}
		candidate := frs[0]
		if !strings.Contains(strings.ToLower(string(candidate.Raw)), "\nx-gic-attachments-removed:") &&
			!strings.HasPrefix(strings.ToLower(string(candidate.Raw)), "x-gic-attachments-removed:") {
			continue
		}
		_, stillMatching, _, err := mimeedit.RemoveAttachments(candidate.Raw, matchName)
		if err == nil && len(stillMatching) == 0 {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return nil, nil, nil
	}
	return &candidates[len(candidates)-1], candidates[:len(candidates)-1], nil
}

func replaceMessageAttachments(cl *imapc.Client, mailbox, allMail, trash string, fr imapc.FetchResult, headerDate, marker string, rewritten []byte, reusable *imapc.FetchResult, duplicates []imapc.FetchResult) (uint32, error) {
	date := fr.InternalDate
	if date.IsZero() {
		date = time.Now()
		if parsed, err := mail.ParseDate(headerDate); err == nil {
			date = parsed
		}
	}
	var replacementUID uint32
	if reusable != nil {
		replacementUID = reusable.UID
	} else {
		var err error
		replacementUID, err = cl.Append(mailbox, appendableFlags(fr.Flags), date, rewritten)
		if err != nil {
			return 0, fmt.Errorf("appending replacement: %w", err)
		}
		if replacementUID == 0 {
			return 0, fmt.Errorf("server appended the replacement without returning APPENDUID; original message was retained")
		}
		if replacementUID == fr.UID {
			return replacementUID, fmt.Errorf("server returned the original UID for the replacement; original message was retained")
		}
		if err := verifyReplacement(cl, replacementUID, marker, len(rewritten)); err != nil {
			cleanupErr := deleteSelectedMessage(cl, replacementUID)
			if cleanupErr != nil {
				return replacementUID, fmt.Errorf("verifying replacement: %v; removing invalid replacement: %v", err, cleanupErr)
			}
			return 0, fmt.Errorf("verifying replacement: %w", err)
		}
	}

	labels := mutableLabels(fr.Labels, mailbox)
	if err := cl.StoreLabels([]uint32{replacementUID}, true, labels); err != nil {
		cleanupErr := deleteSelectedMessage(cl, replacementUID)
		if cleanupErr != nil {
			return replacementUID, fmt.Errorf("preserving labels: %v; removing incomplete replacement: %v", err, cleanupErr)
		}
		return 0, fmt.Errorf("preserving labels: %w", err)
	}
	if fr.GmailMessageID == "" {
		return replacementUID, fmt.Errorf("replacement was created, but source has no X-GM-MSGID; original message was retained")
	}
	if err := deleteGmailOriginal(cl, allMail, trash, fr.GmailMessageID); err != nil {
		return replacementUID, fmt.Errorf("replacement was created, but removing original UID %d failed: %w", fr.UID, err)
	}
	for _, duplicate := range duplicates {
		if duplicate.GmailMessageID == "" {
			return replacementUID, fmt.Errorf("original was removed, but duplicate replacement UID %d has no X-GM-MSGID", duplicate.UID)
		}
		if err := deleteGmailOriginal(cl, allMail, trash, duplicate.GmailMessageID); err != nil {
			return replacementUID, fmt.Errorf("original was removed, but deleting duplicate replacement UID %d failed: %w", duplicate.UID, err)
		}
	}
	return replacementUID, nil
}

func verifyReplacement(cl *imapc.Client, uid uint32, marker string, expectedSize int) error {
	frs, err := cl.UIDFetch([]uint32{uid}, "UID RFC822.SIZE BODY.PEEK[HEADER.FIELDS (X-GIC-ATTACHMENTS-REMOVED)]")
	if err != nil {
		return err
	}
	if len(frs) != 1 {
		return fmt.Errorf("replacement UID %d was not found", uid)
	}
	if frs[0].Size != expectedSize {
		return fmt.Errorf("replacement size is %d bytes, expected %d", frs[0].Size, expectedSize)
	}
	if !strings.Contains(string(frs[0].Headers), "X-Gic-Attachments-Removed: "+marker) &&
		!strings.Contains(string(frs[0].Headers), "X-GIC-Attachments-Removed: "+marker) {
		return fmt.Errorf("replacement marker was not found")
	}
	return nil
}

func deleteGmailOriginal(cl *imapc.Client, allMail, trash, gmailMessageID string) error {
	if _, err := cl.Select(allMail); err != nil {
		return fmt.Errorf("selecting All Mail: %w", err)
	}
	uids, err := cl.UIDSearch("X-GM-MSGID " + gmailMessageID)
	if err != nil {
		return fmt.Errorf("finding original in All Mail: %w", err)
	}
	if len(uids) != 1 {
		return fmt.Errorf("expected one original in All Mail, found %d", len(uids))
	}
	if err := cl.StoreLabels([]uint32{uids[0]}, true, []string{`\Trash`}); err != nil {
		return fmt.Errorf("moving original to Trash: %w", err)
	}

	if _, err := cl.Select(allMail); err != nil {
		return fmt.Errorf("reselecting All Mail: %w", err)
	}
	uids, err = cl.UIDSearch("X-GM-MSGID " + gmailMessageID)
	if err != nil {
		return fmt.Errorf("verifying original left All Mail: %w", err)
	}
	if len(uids) != 0 {
		return fmt.Errorf("original remains in All Mail after deletion")
	}

	if _, err := cl.Select(trash); err != nil {
		return fmt.Errorf("selecting Trash: %w", err)
	}
	uids, err = cl.UIDSearch("X-GM-MSGID " + gmailMessageID)
	if err != nil {
		return fmt.Errorf("finding original in Trash: %w", err)
	}
	if len(uids) == 0 {
		return nil
	}
	if len(uids) != 1 {
		return fmt.Errorf("expected one original in Trash, found %d", len(uids))
	}
	if err := deleteSelectedMessage(cl, uids[0]); err != nil {
		return fmt.Errorf("permanently deleting original from Trash: %w", err)
	}
	return nil
}

func deleteSelectedMessage(cl *imapc.Client, uid uint32) error {
	if err := cl.StoreFlags([]uint32{uid}, true, []string{`\Deleted`}); err != nil {
		return err
	}
	if err := cl.UIDExpunge([]uint32{uid}); err != nil {
		_ = cl.StoreFlags([]uint32{uid}, false, []string{`\Deleted`})
		return err
	}
	return nil
}

func appendableFlags(flags []string) []string {
	var out []string
	for _, value := range flags {
		switch strings.ToLower(value) {
		case `\seen`, `\answered`, `\flagged`, `\draft`:
			out = append(out, value)
		}
	}
	return out
}

func mutableLabels(labels []string, selectedMailbox string) []string {
	var out []string
	for _, label := range labels {
		if strings.EqualFold(label, selectedMailbox) {
			continue
		}
		switch strings.ToLower(label) {
		case `\all`, `\trash`:
			continue
		default:
			out = append(out, label)
		}
	}
	return out
}
