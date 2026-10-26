// Command gic-bin is the real gic binary. Install the `gic` shell function
// (see `gic shell-init`) to use `gic` transparently in your shell.
package main

import (
	"flag"
	"fmt"
	"os"

	"gic/internal/cli"
	"gic/internal/imapd"
)

// extractGlobals scans args for -s, -m/--mailbox, -v/--verbose anywhere in
// the argument list (so they can appear before or interspersed with the
// subcommand), returning the parsed globals and the remaining args.
func extractGlobals(args []string) (cli.Globals, []string) {
	var g cli.Globals
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "-s":
			i++
			if i < len(args) {
				g.Account = args[i]
			}
		case "-m", "--mailbox":
			i++
			if i < len(args) {
				g.Mailbox = args[i]
			}
		case "-v", "--verbose":
			g.Verbose = true
		default:
			rest = append(rest, a)
		}
	}
	return g, rest
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "daemon" {
		runDaemon(os.Args[2:])
		return
	}
	g, args := extractGlobals(os.Args[1:])
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "account":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		switch rest[0] {
		case "add":
			cli.AccountAdd(rest[1:])
		case "list":
			cli.AccountList()
		case "rename":
			cli.AccountRename(rest[1:])
		default:
			usage()
			os.Exit(1)
		}
	case "select":
		cli.Select(rest)
	case "unselect":
		cli.Unselect()
	case "mailbox":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		switch rest[0] {
		case "select":
			cli.MailboxSelect(rest[1:])
		case "unselect":
			cli.MailboxUnselect()
		case "list":
			cli.MailboxList(g)
		default:
			usage()
			os.Exit(1)
		}
	case "search":
		cli.Search(rest, g)
	case "label":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		switch rest[0] {
		case "list":
			cli.LabelList(g)
		case "create":
			cli.LabelCreate(rest[1:], g)
		case "apply":
			cli.LabelApply(rest[1:], g)
		case "remove":
			cli.LabelRemove(rest[1:], g)
		default:
			usage()
			os.Exit(1)
		}
	case "mark":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		switch rest[0] {
		case "seen", "read":
			cli.MarkSeen(rest[1:], g)
		case "unseen", "unread":
			cli.MarkUnseen(rest[1:], g)
		default:
			usage()
			os.Exit(1)
		}
	case "attachment":
		if len(rest) == 0 {
			usage()
			os.Exit(1)
		}
		switch rest[0] {
		case "remove":
			cli.AttachmentRemove(rest[1:], g)
		default:
			usage()
			os.Exit(1)
		}
	case "fetch":
		cli.Fetch(rest, g)
	case "copy":
		cli.Copy(rest)
	case "send":
		cli.Send(rest, g)
	case "reply":
		cli.Reply(rest, g)
	case "forward":
		cli.Forward(rest, g)
	case "shell-init":
		cli.ShellInit()
	default:
		usage()
		os.Exit(1)
	}
}

func runDaemon(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gic-bin daemon start|serve|ping --socket <path>")
		os.Exit(1)
	}
	fs := flag.NewFlagSet("daemon "+args[0], flag.ExitOnError)
	socket := fs.String("socket", "", "Unix socket path")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(1)
	}
	if *socket == "" {
		fmt.Fprintln(os.Stderr, "gic: daemon --socket is required")
		os.Exit(1)
	}
	var err error
	switch args[0] {
	case "start":
		err = imapd.Start(*socket)
	case "serve":
		err = imapd.Serve(*socket)
	case "ping":
		err = imapd.Ping(*socket)
	default:
		err = fmt.Errorf("unknown daemon command %q", args[0])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gic: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `gic - Gmail CLI

Usage: gic [-s <alias|N>] [-m <mailbox>] [-v] <command> [args...]

Commands:
  account add [alias]              add a Gmail account (App Password)
  account list                     list configured accounts
  account rename <old> <new>       rename an account alias
  select <alias|N>                 select account for this shell session
  unselect                         clear selected account
  mailbox select <name>            select mailbox for this shell session
  mailbox unselect                 clear selected mailbox (back to INBOX)
  mailbox list                     list mailboxes/labels
  search [filters...]               search messages, prints JSON
  label list                       list labels
  label create <name>              create a label
  label apply <name> [--uid..|--stdin]
  label remove <name> [--uid..|--stdin]
  mark seen [--uid..|--stdin]      set \Seen (mark as read)
  mark unseen [--uid..|--stdin]    clear \Seen (mark as unread)
  attachment remove [--uid..|--stdin] (--all|--name <filename>)
  fetch [--uid..|--stdin] -o <path> download raw messages
  copy --from-account A --to-account B [filters...]
  send --to <addr> --subject <s> (--body <t>|--body-file <f>) [--attach <f>] [--dry-run]
  reply --uid <uid> [--all] (--body <t>|--body-file <f>) [--no-quote] [--dry-run]
  forward --uid <uid> --to <addr> [--note <t>] [--as-attachment] [--dry-run]
  shell-init                       print the shell function wrapper

See docs/COMMANDS.md for full details.`)
}
