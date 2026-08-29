package search

import (
	"testing"
	"time"
)

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

func TestBuildSinceUntilDateFormat(t *testing.T) {
	f := Filters{Since: "2026-01-01", Until: "2026-08-01"}
	got, err := f.Build()
	if err != nil {
		t.Fatal(err)
	}
	want := "SINCE 01-Jan-2026 BEFORE 01-Aug-2026"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseRelativeDate(t *testing.T) {
	now := time.Date(2026, time.August, 28, 20, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	tests := map[string]string{
		"+5days":          "02-Sep-2026",
		"-2days":          "26-Aug-2026",
		"+1day":           "29-Aug-2026",
		"-1week":          "21-Aug-2026",
		"-1month -1week":  "21-Jul-2026",
		"+1year +2months": "28-Oct-2027",
	}
	for input, want := range tests {
		got, err := parseDate(input, now)
		if err != nil {
			t.Fatalf("parseDate(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("parseDate(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildInvalidDate(t *testing.T) {
	f := Filters{Since: "not-a-date"}
	if _, err := f.Build(); err == nil {
		t.Error("expected error for invalid date")
	}
}

func TestBuildInvalidRelativeDateUnit(t *testing.T) {
	f := Filters{Since: "-1hour"}
	if _, err := f.Build(); err == nil {
		t.Error("expected error for unsupported relative date unit")
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

func TestBuildSeenFilter(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		seen *bool
		want string
	}{
		{"unset", nil, "ALL"},
		{"seen", &yes, "SEEN"},
		{"unseen", &no, "UNSEEN"},
	}
	for _, tc := range tests {
		got, err := (Filters{Seen: tc.seen}).Build()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestBuildSeenCombinesWithOtherFilters(t *testing.T) {
	unread := false
	got, err := (Filters{Label: []string{"accountant"}, Seen: &unread}).Build()
	if err != nil {
		t.Fatal(err)
	}
	want := `X-GM-LABELS "accountant" UNSEEN`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
