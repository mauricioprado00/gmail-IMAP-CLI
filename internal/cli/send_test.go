package cli

import "testing"

func TestForwardedName(t *testing.T) {
	cases := map[string]string{
		"Invoice #123 / Q3":  "Invoice 123  Q3.eml",
		"":                   "forwarded.eml",
		"  ../../etc/passwd": "etcpasswd.eml",
	}
	for in, want := range cases {
		if got := forwardedName(in); got != want {
			t.Errorf("forwardedName(%q) = %q, want %q", in, got, want)
		}
	}
}
