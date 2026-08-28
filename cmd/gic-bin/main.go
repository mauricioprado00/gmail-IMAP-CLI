// Command gic-bin is the real gic binary. Install the `gic` shell function
// (see `gic shell-init`) to use `gic` transparently in your shell.
package main

import (
	"fmt"
	"os"

	"gic/internal/cli"
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
	case "fetch":
		cli.Fetch(rest, g)
	case "copy":
		cli.Copy(rest)
	case "shell-init":
		cli.ShellInit()
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `gic - Gmail IMAP CLI

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
  fetch [--uid..|--stdin] -o <path> download raw messages
  copy --from-account A --to-account B [filters...]
  shell-init                       print the shell function wrapper

See docs/COMMANDS.md for full details.`)
}
