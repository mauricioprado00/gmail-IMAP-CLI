package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"gic/internal/account"
	"gic/internal/imapc"
	"gic/internal/smtpc"

	"golang.org/x/term"
)

// AccountAdd implements `gic account add [alias] [--local|--global|-g]`.
func AccountAdd(args []string) {
	fs := flag.NewFlagSet("account add", flag.ExitOnError)
	local := fs.Bool("local", false, "save under ./accounts (default)")
	global := fs.Bool("global", false, "save under ~/.config/gic/accounts")
	fs.BoolVar(global, "g", false, "shorthand for --global")
	if err := fs.Parse(args); err != nil {
		exitErr(err)
	}
	if *local && *global {
		exitErr(fmt.Errorf("--local and --global/-g are mutually exclusive"))
	}
	scope := account.ScopeLocal
	if *global {
		scope = account.ScopeGlobal
	}

	rest := fs.Args()
	alias := ""
	if len(rest) > 0 {
		alias = rest[0]
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Gmail address: ")
	email, _ := reader.ReadString('\n')
	email = strings.TrimSpace(email)
	if email == "" {
		exitErr(fmt.Errorf("email address is required"))
	}
	if alias == "" {
		alias = email
	}

	fmt.Print("App password: ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		exitErr(fmt.Errorf("reading app password: %w", err))
	}
	password := strings.TrimSpace(string(pwBytes))

	a := &account.Account{
		Alias:       alias,
		Email:       email,
		IMAPHost:    "imap.gmail.com",
		IMAPPort:    993,
		SMTPHost:    account.DefaultSMTPHost,
		SMTPPort:    account.DefaultSMTPPort,
		AppPassword: password,
		Scope:       scope,
	}

	cl, err := imapc.Connect(a.IMAPHost, a.IMAPPort, a.Email, a.AppPassword, false)
	if err != nil {
		exitErr(fmt.Errorf("could not verify IMAP login: %w", err))
	}
	if _, err := cl.Select("INBOX"); err != nil {
		exitErr(fmt.Errorf("could not verify IMAP login (SELECT INBOX): %w", err))
	}
	cl.Logout()
	cl.Close()
	fmt.Printf("\u2713 Verified IMAP login for %s\n", email)

	if err := smtpc.Verify(a.SMTPHost, a.SMTPPort, a.Email, a.AppPassword); err != nil {
		exitErr(fmt.Errorf("could not verify SMTP login: %w", err))
	}
	fmt.Printf("\u2713 Verified SMTP login for %s\n", email)

	if err := account.Save(a); err != nil {
		exitErr(err)
	}
	dir, _ := account.ResolveDir(scope)
	fmt.Printf("\u2713 Saved %s/%s.json\n", dir, alias)
}

// AccountList implements `gic account list`.
func AccountList() {
	accts, err := account.List()
	if err != nil {
		exitErr(err)
	}
	for i, a := range accts {
		if a.Alias == a.Email {
			fmt.Printf("%d. %s [%s]\n", i+1, a.Email, a.Scope)
		} else {
			fmt.Printf("%d. %s (%s) [%s]\n", i+1, a.Email, a.Alias, a.Scope)
		}
	}
}

// AccountRename implements `gic account rename <old-alias> <new-alias>`.
func AccountRename(args []string) {
	if len(args) != 2 {
		exitErr(fmt.Errorf("usage: gic account rename <old-alias> <new-alias>"))
	}
	if err := account.Rename(args[0], args[1]); err != nil {
		exitErr(err)
	}
	fmt.Printf("\u2713 Renamed %s -> %s\n", args[0], args[1])
	fmt.Println("Note: any shell where you already ran `gic select " + args[0] + "` still has the old alias exported; run `gic select " + args[1] + "` there again.")
}
