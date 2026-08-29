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

func TestParseSearchArgsSeenFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want *bool
	}{
		{"neither", []string{"--label", "x"}, nil},
		{"seen", []string{"--seen"}, boolPtr(true)},
		{"unseen", []string{"--unseen"}, boolPtr(false)},
		{"read alias", []string{"--read"}, boolPtr(true)},
		{"unread alias", []string{"--unread"}, boolPtr(false)},
	}
	for _, tc := range tests {
		f, _, _, _, err := parseSearchArgs(tc.args)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		switch {
		case tc.want == nil && f.Seen != nil:
			t.Errorf("%s: got Seen=%v, want nil", tc.name, *f.Seen)
		case tc.want != nil && f.Seen == nil:
			t.Errorf("%s: got Seen=nil, want %v", tc.name, *tc.want)
		case tc.want != nil && *f.Seen != *tc.want:
			t.Errorf("%s: got Seen=%v, want %v", tc.name, *f.Seen, *tc.want)
		}
	}
}

func TestParseSearchArgsSeenAndUnseenConflict(t *testing.T) {
	for _, args := range [][]string{
		{"--seen", "--unseen"},
		{"--read", "--unread"},
	} {
		if _, _, _, _, err := parseSearchArgs(args); err == nil {
			t.Errorf("%v: expected a mutually-exclusive error", args)
		}
	}
}

func boolPtr(b bool) *bool { return &b }
