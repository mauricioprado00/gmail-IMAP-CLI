package cli

import (
	"flag"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strings"

	"gic/internal/account"
	"gic/internal/compose"
	"gic/internal/imapc"
	"gic/internal/model"
	"gic/internal/session"
	"gic/internal/smtpc"
)

// SendSummary is the JSON printed after a successful send/reply/forward.
type SendSummary struct {
	Status     string   `json:"status"`
	MessageID  string   `json:"messageId"`
	From       string   `json:"from"`
	Recipients []string `json:"recipients"`
	Size       int      `json:"size"`
	RepliedTo  uint32   `json:"repliedTo,omitempty"`
	Forwarded  uint32   `json:"forwarded,omitempty"`
	Warning    string   `json:"warning,omitempty"`
}

// sendFlags are the flags shared by send, reply and forward.
type sendFlags struct {
	to, cc, bcc multiFlag
	attach      multiFlag
	subject     string
	body        string
	bodyFile    string
	dryRun      bool
}

func (f *sendFlags) register(fs *flag.FlagSet, bodyFlag string) {
	fs.Var(&f.to, "to", "recipient (repeatable, comma lists allowed)")
	fs.Var(&f.cc, "cc", "carbon-copy recipient (repeatable)")
	fs.Var(&f.bcc, "bcc", "blind recipient (repeatable; envelope only)")
	fs.Var(&f.attach, "attach", "file to attach (repeatable)")
	fs.StringVar(&f.body, bodyFlag, "", "plain-text body")
	fs.StringVar(&f.bodyFile, bodyFlag+"-file", "", "read the body from a file; - for stdin")
	fs.BoolVar(&f.dryRun, "dry-run", false, "print the message to stdout instead of sending")
}

// text returns the body from --body/--body-file (at most one of them).
func (f *sendFlags) text(bodyFlag string) (string, error) {
	if f.body != "" && f.bodyFile != "" {
		return "", fmt.Errorf("--%s and --%s-file are mutually exclusive", bodyFlag, bodyFlag)
	}
	if f.bodyFile == "" {
		return f.body, nil
	}
	var raw []byte
	var err error
	if f.bodyFile == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(f.bodyFile)
	}
	if err != nil {
		return "", fmt.Errorf("reading --%s-file: %w", bodyFlag, err)
	}
	return string(raw), nil
}

// recipients parses --to/--cc/--bcc.
func (f *sendFlags) recipients() (to, cc, bcc []*mail.Address, err error) {
	if to, err = compose.ParseAddresses(f.to); err != nil {
		return
	}
	if cc, err = compose.ParseAddresses(f.cc); err != nil {
		return
	}
	bcc, err = compose.ParseAddresses(f.bcc)
	return
}

func (f *sendFlags) attachments() ([]compose.Attachment, error) {
	var out []compose.Attachment
	for _, p := range f.attach {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading --attach %s: %w", p, err)
		}
		out = append(out, compose.Attachment{Name: filepath.Base(p), Data: data})
	}
	return out, nil
}

// Send implements `gic send`.
func Send(args []string, g Globals) {
	fs := flag.NewFlagSet("send", flag.ExitOnError)
	var f sendFlags
	f.register(fs, "body")
	fs.StringVar(&f.subject, "subject", "", "subject")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}
	if len(fs.Args()) > 0 {
		exitErr(fmt.Errorf("unexpected argument %q (recipients are given with --to)", fs.Args()[0]))
	}

	acct, err := session.ResolveAccount(g.Account)
	if err != nil {
		exitErr(err)
	}
	m, err := f.message(acct, "body")
	if err != nil {
		exitErr(err)
	}
	m.Subject = f.subject
	summary := deliver(acct, m, f.dryRun, g.Verbose)
	if summary != nil {
		printJSON(summary)
	}
}

// Reply implements `gic reply --uid <uid>`.
func Reply(args []string, g Globals) {
	fs := flag.NewFlagSet("reply", flag.ExitOnError)
	var f sendFlags
	f.register(fs, "body")
	uid := fs.Uint("uid", 0, "UID of the message to reply to (required)")
	all := fs.Bool("all", false, "reply to everyone on the original (To and Cc become Cc)")
	noQuote := fs.Bool("no-quote", false, "do not quote the original text below the body")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}
	if *uid == 0 {
		exitErr(fmt.Errorf("--uid is required"))
	}

	acct, err := session.ResolveAccount(g.Account)
	if err != nil {
		exitErr(err)
	}
	m, err := f.message(acct, "body")
	if err != nil {
		exitErr(err)
	}

	cl, mailbox, orig, err := fetchOriginal(g, uint32(*uid))
	if err != nil {
		exitErr(err)
	}
	defer cl.Logout()
	defer cl.Close()

	to, cc := orig.ReplyAddresses(acct.Email, *all)
	m.To = append(to, m.To...)
	m.Cc = append(cc, m.Cc...)
	m.Subject = compose.ReplySubject(orig.Subject())
	m.InReplyTo = orig.MessageID()
	m.References = orig.ReplyReferences()
	if !*noQuote {
		if q := orig.Quote(); q != "" {
			m.Text = strings.TrimRight(m.Text, "\n") + "\n\n" + q
		}
	}

	summary := deliver(acct, m, f.dryRun, g.Verbose)
	if summary == nil {
		return
	}
	summary.RepliedTo = uint32(*uid)
	if err := cl.StoreFlags([]uint32{uint32(*uid)}, true, []string{`\Answered`}); err != nil {
		summary.Warning = fmt.Sprintf("sent, but setting \\Answered on UID %d in %s failed: %v", *uid, mailbox, err)
	}
	printJSON(summary)
}

// Forward implements `gic forward --uid <uid>`.
func Forward(args []string, g Globals) {
	fs := flag.NewFlagSet("forward", flag.ExitOnError)
	var f sendFlags
	f.register(fs, "note")
	uid := fs.Uint("uid", 0, "UID of the message to forward (required)")
	asAttachment := fs.Bool("as-attachment", false, "attach the original as message/rfc822 instead of inlining it")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}
	if *uid == 0 {
		exitErr(fmt.Errorf("--uid is required"))
	}

	acct, err := session.ResolveAccount(g.Account)
	if err != nil {
		exitErr(err)
	}
	m, err := f.message(acct, "note")
	if err != nil {
		exitErr(err)
	}
	if len(m.To)+len(m.Cc)+len(m.Bcc) == 0 {
		exitErr(fmt.Errorf("at least one --to, --cc or --bcc is required"))
	}

	cl, _, orig, raw, err := fetchOriginalRaw(g, uint32(*uid))
	if err != nil {
		exitErr(err)
	}
	defer cl.Logout()
	defer cl.Close()

	m.Subject = compose.ForwardSubject(orig.Subject())
	note := strings.TrimRight(m.Text, "\n")
	if *asAttachment {
		m.Text = note + "\n"
		m.Embedded = &compose.Embedded{Name: forwardedName(orig.Subject()), Raw: raw}
	} else {
		if note != "" {
			note += "\n\n"
		}
		m.Text = note + orig.ForwardBlock()
		m.RawParts = orig.Attachments
	}

	summary := deliver(acct, m, f.dryRun, g.Verbose)
	if summary != nil {
		summary.Forwarded = uint32(*uid)
		printJSON(summary)
	}
}

// message assembles the parts of a Message common to all three commands.
func (f *sendFlags) message(acct *account.Account, bodyFlag string) (*compose.Message, error) {
	text, err := f.text(bodyFlag)
	if err != nil {
		return nil, err
	}
	to, cc, bcc, err := f.recipients()
	if err != nil {
		return nil, err
	}
	attachments, err := f.attachments()
	if err != nil {
		return nil, err
	}
	return &compose.Message{
		From:        &mail.Address{Address: acct.Email},
		To:          to,
		Cc:          cc,
		Bcc:         bcc,
		Text:        text,
		Attachments: attachments,
	}, nil
}

// deliver builds the message and either prints it (dry run, returning nil)
// or submits it over SMTP and returns the summary.
func deliver(acct *account.Account, m *compose.Message, dryRun, verbose bool) *SendSummary {
	raw, err := m.Build()
	if err != nil {
		exitErr(err)
	}
	if dryRun {
		os.Stdout.Write(raw)
		return nil
	}
	host, port := acct.SMTP()
	env := smtpc.Envelope{From: acct.Email, Recipients: m.Recipients()}
	if err := smtpc.Send(host, port, acct.Email, acct.AppPassword, env, raw, verbose); err != nil {
		exitErr(err)
	}
	return &SendSummary{
		Status:     "sent",
		MessageID:  strings.Trim(m.MessageID, "<>"),
		From:       acct.Email,
		Recipients: env.Recipients,
		Size:       len(raw),
	}
}

// fetchOriginal connects, selects the current mailbox and fetches one full
// message. The client is returned open so the caller can flag the message.
func fetchOriginal(g Globals, uid uint32) (*imapc.Client, string, *compose.Original, error) {
	cl, mailbox, orig, _, err := fetchOriginalRaw(g, uid)
	return cl, mailbox, orig, err
}

func fetchOriginalRaw(g Globals, uid uint32) (*imapc.Client, string, *compose.Original, []byte, error) {
	_, cl, err := connect(g)
	if err != nil {
		return nil, "", nil, nil, err
	}
	mailbox := session.ResolveMailbox(g.Mailbox)
	if _, err := cl.Select(mailbox); err != nil {
		cl.Close()
		return nil, "", nil, nil, err
	}
	frs, err := cl.UIDFetch([]uint32{uid}, model.FetchItems("full"))
	if err != nil {
		cl.Close()
		return nil, "", nil, nil, err
	}
	if len(frs) != 1 || len(frs[0].Raw) == 0 {
		cl.Close()
		return nil, "", nil, nil, fmt.Errorf("UID %d not found in mailbox %q", uid, mailbox)
	}
	orig, err := compose.Parse(frs[0].Raw)
	if err != nil {
		cl.Close()
		return nil, "", nil, nil, err
	}
	return cl, mailbox, orig, frs[0].Raw, nil
}

// forwardedName derives a filename for a message/rfc822 attachment.
func forwardedName(subject string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == ' ':
			return r
		}
		return -1
	}, subject)
	name = strings.TrimSpace(name)
	if name == "" {
		name = "forwarded"
	}
	return name + ".eml"
}
