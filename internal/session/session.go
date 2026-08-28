// Package session resolves the "selected" account/mailbox from environment
// variables (GIC_ACCOUNT / GIC_MAILBOX) and one-off flag overrides, and
// generates the shell-function wrapper needed to let `gic select` /
// `gic mailbox select` affect the calling shell's environment.
package session

import (
	"fmt"
	"os"

	"gic/internal/account"
)

const (
	// EnvAccount holds the selected account alias for the current shell.
	EnvAccount = "GIC_ACCOUNT"
	// EnvMailbox holds the selected mailbox/label name for the current shell.
	EnvMailbox = "GIC_MAILBOX"
	// DefaultMailbox is used when no mailbox is selected via env or flag.
	DefaultMailbox = "INBOX"
)

// ResolveAccount picks the account to operate on: the `-s` flag value takes
// priority, otherwise $GIC_ACCOUNT, otherwise an error.
func ResolveAccount(flagValue string) (*account.Account, error) {
	ref := flagValue
	if ref == "" {
		ref = os.Getenv(EnvAccount)
	}
	if ref == "" {
		return nil, fmt.Errorf("no account selected: run `gic select <alias|N>` or pass -s <alias|N>")
	}
	return account.Resolve(ref)
}

// ResolveMailbox picks the mailbox to operate on: the `-m` flag value takes
// priority, otherwise $GIC_MAILBOX, otherwise DefaultMailbox.
func ResolveMailbox(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(EnvMailbox); v != "" {
		return v
	}
	return DefaultMailbox
}

// PrintExport writes `export NAME=value` to stdout, for `eval "$(gic ...)"`.
func PrintExport(name, value string) {
	fmt.Printf("export %s=%q\n", name, value)
}

// PrintUnset writes `unset NAME` to stdout.
func PrintUnset(name string) {
	fmt.Printf("unset %s\n", name)
}

// ShellInit returns the shell function wrapper script. Source its output
// (or append it) from ~/.bashrc / ~/.zshrc so that `gic select`/`gic
// mailbox`/`gic unselect` can affect the current shell's environment: the
// real compiled binary is invoked as `gic-bin` and this function evals its
// output only for the subcommands that print export/unset statements.
const ShellInit = `gic() {
  case "$1" in
    select|unselect)
      eval "$(command gic-bin "$@")"
      ;;
    mailbox)
      case "$2" in
        select|unselect)
          eval "$(command gic-bin "$@")"
          ;;
        *)
          command gic-bin "$@"
          ;;
      esac
      ;;
    *)
      command gic-bin "$@"
      ;;
  esac
}
`
