package search

import "testing"

func TestBuildAndAcrossFilterTypes(t *testing.T) {
	f := Filters{From: []string{"a@x.com"}, Label: []string{"accountant"}}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `FROM "a@x.com" X-GM-LABELS "accountant"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildOrWithinRepeatedFilter(t *testing.T) {
	f := Filters{Label: []string{"a", "b"}}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `OR X-GM-LABELS "a" X-GM-LABELS "b"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildOrWithThreeValues(t *testing.T) {
	f := Filters{Label: []string{"a", "b", "c"}}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `OR X-GM-LABELS "a" (OR X-GM-LABELS "b" X-GM-LABELS "c")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildEmptyIsAll(t *testing.T) {
	f := Filters{}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	if got != "ALL" {
		t.Errorf("got %q, want ALL", got)
	}
}

func TestBuildHeaderThreadID(t *testing.T) {
	f := Filters{Header: map[string][]string{"X-GM-THRID": {"1234567890123456789"}}}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := "X-GM-THRID 1234567890123456789"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildHeaderGeneric(t *testing.T) {
	f := Filters{Header: map[string][]string{"X-Custom": {"val"}}}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `HEADER "X-Custom" "val"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildSinceBeforeDateFormat(t *testing.T) {
	f := Filters{Since: "2026-01-01", Before: "2026-08-01"}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := "SINCE 01-Jan-2026 BEFORE 01-Aug-2026"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildInvalidDate(t *testing.T) {
	f := Filters{Since: "not-a-date"}
	if _, err := f.Build(); err == nil {
		t.Error("expected error for invalid date")
	}
}

func TestQuoteEscaping(t *testing.T) {
	f := Filters{Subject: []string{`say "hi"`}}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `SUBJECT "say \"hi\""`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
