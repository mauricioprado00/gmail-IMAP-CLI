// Package account manages per-Gmail-account JSON config/secret files,
// stored in one of two scopes: "local" (an `accounts/` directory
// discovered by walking up from the current working directory, like how
// `.git` is discovered) or "global" (`~/.config/gic/accounts/`, user-space
// wide). See docs/COMMANDS.md §1.1.
package account

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// Scope identifies where an account file lives.
type Scope string

const (
	ScopeLocal  Scope = "local"
	ScopeGlobal Scope = "global"

	// localDirName is the directory name searched for when walking up
	// from the CWD, and created (relative to CWD) if none is found.
	localDirName = "accounts"
)

// Account holds the persisted config/secrets for one Gmail account.
type Account struct {
	Alias       string    `json:"alias"`
	Email       string    `json:"email"`
	IMAPHost    string    `json:"imapHost"`
	IMAPPort    int       `json:"imapPort"`
	SMTPHost    string    `json:"smtpHost,omitempty"`
	SMTPPort    int       `json:"smtpPort,omitempty"`
	AppPassword string    `json:"appPassword"`
	Scope       Scope     `json:"scope,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`

	// dir is the directory this account file was loaded from (or will be
	// saved to); not serialized. Set by Load/List, and by ResolveDir for
	// newly-created accounts.
	dir string `json:"-"`
}

// Gmail SMTP defaults, used when an account file predates sending and has
// no smtpHost/smtpPort of its own (docs/COMMANDS.md §1.1).
const (
	DefaultSMTPHost = "smtp.gmail.com"
	DefaultSMTPPort = 465
)

// SMTP returns the SMTP endpoint for the account, falling back to the Gmail
// defaults field by field when the account file does not set them.
func (a *Account) SMTP() (host string, port int) {
	host, port = a.SMTPHost, a.SMTPPort
	if host == "" {
		host = DefaultSMTPHost
	}
	if port == 0 {
		port = DefaultSMTPPort
	}
	return host, port
}

// GlobalDir returns `~/.config/gic/accounts`.
func GlobalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "gic", "accounts"), nil
}

// FindLocalDir walks up from the current working directory looking for an
// existing `accounts/` directory (like `.git` discovery), returning it and
// true if found.
func FindLocalDir() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		candidate := filepath.Join(dir, localDirName)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// ResolveDir returns the directory to use for the given scope, creating it
// if necessary. For ScopeLocal, it reuses an existing `accounts/` dir found
// by walking up from the CWD if there is one; otherwise it creates
// `./accounts` in the CWD.
func ResolveDir(scope Scope) (string, error) {
	switch scope {
	case ScopeGlobal:
		dir, err := GlobalDir()
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		return dir, nil
	case ScopeLocal, "":
		if dir, ok := FindLocalDir(); ok {
			return dir, nil
		}
		if err := os.MkdirAll(localDirName, 0o755); err != nil {
			return "", err
		}
		return localDirName, nil
	default:
		return "", fmt.Errorf("invalid scope %q", scope)
	}
}

func filePath(dir, alias string) string {
	return filepath.Join(dir, alias+".json")
}

// loadFrom reads one account file from a specific directory/scope.
func loadFrom(dir string, scope Scope, alias string) (*Account, error) {
	b, err := os.ReadFile(filePath(dir, alias))
	if err != nil {
		return nil, err
	}
	var a Account
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("account %q: invalid JSON: %w", alias, err)
	}
	a.dir = dir
	a.Scope = scope
	return &a, nil
}

// Load finds an account by alias, checking local first, then global
// (local shadows global on collision, per docs/COMMANDS.md §1.1).
func Load(alias string) (*Account, error) {
	if dir, ok := FindLocalDir(); ok {
		if a, err := loadFrom(dir, ScopeLocal, alias); err == nil {
			return a, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if dir, err := GlobalDir(); err == nil {
		if a, lerr := loadFrom(dir, ScopeGlobal, alias); lerr == nil {
			return a, nil
		} else if !os.IsNotExist(lerr) {
			return nil, lerr
		}
	}
	return nil, fmt.Errorf("account %q not found (checked local and global)", alias)
}

// Exists reports whether an account file for alias already exists in the
// given scope specifically (used to check for collisions within one
// scope, e.g. before renaming).
func Exists(scope Scope, alias string) bool {
	dir, err := ResolveDir(scope)
	if err != nil {
		return false
	}
	_, err = os.Stat(filePath(dir, alias))
	return err == nil
}

// Save writes the account file into its resolved scope directory
// (creating it if needed), with 0600 permissions since it contains a
// secret (app password). a.Scope must be set to ScopeLocal or ScopeGlobal.
func Save(a *Account) error {
	dir, err := ResolveDir(a.Scope)
	if err != nil {
		return err
	}
	a.dir = dir
	a.UpdatedAt = time.Now().UTC()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = a.UpdatedAt
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath(dir, a.Alias), b, 0o600)
}

func listDir(dir string, scope Scope) ([]*Account, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Account
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		alias := e.Name()[:len(e.Name())-len(".json")]
		a, err := loadFrom(dir, scope, alias)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// List returns all accounts merged from both scopes (local shadows global
// on alias collision), sorted by alias. This order defines the "-s N"
// numbering used everywhere else, recomputed on every call.
func List() ([]*Account, error) {
	byAlias := map[string]*Account{}

	if dir, err := GlobalDir(); err == nil {
		globals, err := listDir(dir, ScopeGlobal)
		if err != nil {
			return nil, err
		}
		for _, a := range globals {
			byAlias[a.Alias] = a
		}
	}
	if dir, ok := FindLocalDir(); ok {
		locals, err := listDir(dir, ScopeLocal)
		if err != nil {
			return nil, err
		}
		for _, a := range locals {
			byAlias[a.Alias] = a // local shadows global
		}
	}

	aliases := make([]string, 0, len(byAlias))
	for alias := range byAlias {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	out := make([]*Account, 0, len(aliases))
	for _, alias := range aliases {
		out = append(out, byAlias[alias])
	}
	return out, nil
}

// Resolve looks up an account by numeric 1-based index (as printed by
// `gic account list`) or by alias.
func Resolve(aliasOrNum string) (*Account, error) {
	if aliasOrNum == "" {
		return nil, fmt.Errorf("no account specified")
	}
	if n, err := strconv.Atoi(aliasOrNum); err == nil {
		all, err := List()
		if err != nil {
			return nil, err
		}
		if n < 1 || n > len(all) {
			return nil, fmt.Errorf("no account #%d (have %d accounts)", n, len(all))
		}
		return all[n-1], nil
	}
	return Load(aliasOrNum)
}

// Rename renames the account file within its current scope and updates
// its alias field. Fails if newAlias already exists in that same scope
// (an alias may still collide across scopes; local shadows global).
func Rename(oldAlias, newAlias string) error {
	a, err := Load(oldAlias)
	if err != nil {
		return err
	}
	if Exists(a.Scope, newAlias) {
		return fmt.Errorf("account %q already exists in %s scope", newAlias, a.Scope)
	}
	oldDir := a.dir
	oldPath := filePath(oldDir, oldAlias)
	a.Alias = newAlias
	if err := Save(a); err != nil {
		return err
	}
	return os.Remove(oldPath)
}
