package account

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempHomeAndCWD isolates $HOME and CWD for a test so FindLocalDir /
// GlobalDir don't touch the real filesystem.
func withTempHomeAndCWD(t *testing.T) (home, cwd string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	t.Setenv("HOME", home)
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
	return home, cwd
}

func TestFindLocalDirWalksUp(t *testing.T) {
	_, cwd := withTempHomeAndCWD(t)
	if err := os.MkdirAll(filepath.Join(cwd, localDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(cwd, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	dir, ok := FindLocalDir()
	if !ok {
		t.Fatal("expected to find local dir walking up")
	}
	want := filepath.Join(cwd, localDirName)
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
}

func TestFindLocalDirNoneFound(t *testing.T) {
	withTempHomeAndCWD(t)
	if _, ok := FindLocalDir(); ok {
		t.Error("expected no local dir to be found")
	}
}

func TestSaveLoadLocalAndGlobal(t *testing.T) {
	home, _ := withTempHomeAndCWD(t)

	localAcct := &Account{Alias: "loc", Email: "loc@x.com", Scope: ScopeLocal}
	if err := Save(localAcct); err != nil {
		t.Fatal(err)
	}
	globalAcct := &Account{Alias: "glob", Email: "glob@x.com", Scope: ScopeGlobal}
	if err := Save(globalAcct); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(home, ".config", "gic", "accounts", "glob.json")); err != nil {
		t.Errorf("global file not written: %v", err)
	}

	loaded, err := Load("loc")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Scope != ScopeLocal {
		t.Errorf("scope = %q, want local", loaded.Scope)
	}
	loaded, err = Load("glob")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Scope != ScopeGlobal {
		t.Errorf("scope = %q, want global", loaded.Scope)
	}
}

func TestListLocalShadowsGlobal(t *testing.T) {
	withTempHomeAndCWD(t)

	if err := Save(&Account{Alias: "dup", Email: "global@x.com", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Account{Alias: "dup", Email: "local@x.com", Scope: ScopeLocal}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Account{Alias: "onlyglobal", Email: "og@x.com", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}

	all, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d accounts, want 2: %+v", len(all), all)
	}
	// sorted by alias: "dup" < "onlyglobal"
	if all[0].Alias != "dup" || all[0].Email != "local@x.com" || all[0].Scope != ScopeLocal {
		t.Errorf("expected local 'dup' to shadow global, got %+v", all[0])
	}
	if all[1].Alias != "onlyglobal" || all[1].Scope != ScopeGlobal {
		t.Errorf("got %+v", all[1])
	}
}

func TestResolveByNumber(t *testing.T) {
	withTempHomeAndCWD(t)
	if err := Save(&Account{Alias: "a", Email: "a@x.com", Scope: ScopeLocal}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Account{Alias: "b", Email: "b@x.com", Scope: ScopeLocal}); err != nil {
		t.Fatal(err)
	}
	a, err := Resolve("1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Alias != "a" {
		t.Errorf("got %q, want a", a.Alias)
	}
	if _, err := Resolve("99"); err == nil {
		t.Error("expected error for out-of-range index")
	}
}

func TestRenameStaysInScope(t *testing.T) {
	withTempHomeAndCWD(t)
	if err := Save(&Account{Alias: "old", Email: "x@x.com", Scope: ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if err := Rename("old", "new"); err != nil {
		t.Fatal(err)
	}
	a, err := Load("new")
	if err != nil {
		t.Fatal(err)
	}
	if a.Scope != ScopeGlobal {
		t.Errorf("scope = %q, want global", a.Scope)
	}
	if _, err := Load("old"); err == nil {
		t.Error("expected old alias to no longer exist")
	}
}

func TestRenameCollisionSameScope(t *testing.T) {
	withTempHomeAndCWD(t)
	if err := Save(&Account{Alias: "a", Email: "a@x.com", Scope: ScopeLocal}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Account{Alias: "b", Email: "b@x.com", Scope: ScopeLocal}); err != nil {
		t.Fatal(err)
	}
	if err := Rename("a", "b"); err == nil {
		t.Error("expected collision error")
	}
}
