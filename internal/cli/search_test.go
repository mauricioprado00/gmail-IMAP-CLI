package cli

import "testing"

func TestParseSearchArgsTimeFlags(t *testing.T) {
	f, _, _, _, err := parseSearchArgs([]string{"-since", "-1month", "-1week", "--until", "+1year", "+2days"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Since != "-1month -1week" || f.Until != "+1year +2days" {
		t.Fatalf("got since=%q until=%q", f.Since, f.Until)
	}
}

func TestParseSearchArgsTimeShorthand(t *testing.T) {
	f, _, _, _, err := parseSearchArgs([]string{"until", "+1month", "+1week"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Until != "+1month +1week" {
		t.Fatalf("got until=%q", f.Until)
	}
}

func TestParseSearchArgsBeforeAlias(t *testing.T) {
	f, _, _, _, err := parseSearchArgs([]string{"--before", "2026-09-01"})
	if err != nil {
		t.Fatal(err)
	}
	if f.Until != "2026-09-01" {
		t.Fatalf("got until=%q", f.Until)
	}
}
