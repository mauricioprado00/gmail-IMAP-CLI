// Package cli implements gic's subcommands.
package cli

import (
	"fmt"
	"os"
	"strings"

	"gic/internal/account"
	"gic/internal/imapc"
	"gic/internal/session"
)

// multiFlag is a flag.Value that accumulates repeated occurrences of the
// same flag into a slice (used for --from, --label, etc. so repeats mean
// OR, per docs/COMMANDS.md §4.1).
type multiFlag []string

func (m *multiFlag) String() string { return fmt.Sprintf("%v", []string(*m)) }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

const dateFilterFormat = "YYYY-MM-DD or signed offsets (+5days, -1month -1week; units: day/week/month/year)"

// normalizeTimeFilterArgs combines consecutive relative-date terms so the
// flag package treats expressions such as "--since -1month -1week" as one value.
func normalizeTimeFilterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		out = append(out, arg)
		if arg != "-since" && arg != "--since" &&
			arg != "-until" && arg != "--until" &&
			arg != "-before" && arg != "--before" {
			continue
		}
		if i+1 >= len(args) {
			continue
		}

		i++
		value := args[i]
		if isRelativeDateTerm(value) {
			for i+1 < len(args) && isRelativeDateTerm(args[i+1]) {
				i++
				value += " " + args[i]
			}
		}
		out = append(out, value)
	}
	return out
}

func isRelativeDateTerm(s string) bool {
	if len(s) < 3 || (s[0] != '+' && s[0] != '-') {
		return false
	}
	i := 1
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 1 {
		return false
	}
	switch strings.ToLower(s[i:]) {
	case "day", "days", "week", "weeks", "month", "months", "year", "years":
		return true
	default:
		return false
	}
}

// Globals holds the flags every gic command accepts.
type Globals struct {
	Account string // -s
	Mailbox string // -m/--mailbox
	Verbose bool   // -v/--verbose
}

// exitErr prints the error to stderr and exits 1.
func exitErr(err error) {
	fmt.Fprintln(os.Stderr, "gic: "+err.Error())
	os.Exit(1)
}

// connect resolves the selected account and opens+logs in an IMAP client.
func connect(g Globals) (*account.Account, *imapc.Client, error) {
	acct, err := session.ResolveAccount(g.Account)
	if err != nil {
		return nil, nil, err
	}
	cl, err := imapc.Connect(acct.IMAPHost, acct.IMAPPort, acct.Email, acct.AppPassword, g.Verbose)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to %s: %w", acct.Email, err)
	}
	return acct, cl, nil
}
