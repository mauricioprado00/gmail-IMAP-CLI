// Package search builds Gmail IMAP SEARCH criteria strings from gic's
// filter flags, implementing the AND (across filter types) / OR (repeated
// same filter) combination rule documented in docs/COMMANDS.md §4.1.
package search

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Filters holds all filter flags for one `gic search`/`gic copy` call.
type Filters struct {
	From    []string
	To      []string
	Subject []string
	Body    []string
	Label   []string
	// Header maps header name -> list of values (OR'd within the same
	// name). "X-GM-THRID" and "X-GM-MSGID" are special-cased to use
	// Gmail's dedicated IMAP SEARCH keys instead of generic HEADER.
	Header map[string][]string
	Since  string // "2006-01-02"
	Before string // "2006-01-02"
}

// Build renders the filters into a single IMAP UID SEARCH criteria string.
func (f Filters) Build() (string, error) {
	var parts []string

	if g := orGroup("FROM", f.From, true); g != "" {
		parts = append(parts, g)
	}
	if g := orGroup("TO", f.To, true); g != "" {
		parts = append(parts, g)
	}
	if g := orGroup("SUBJECT", f.Subject, true); g != "" {
		parts = append(parts, g)
	}
	if g := orGroup("BODY", f.Body, true); g != "" {
		parts = append(parts, g)
	}
	if g := orGroup("X-GM-LABELS", f.Label, true); g != "" {
		parts = append(parts, g)
	}

	names := make([]string, 0, len(f.Header))
	for n := range f.Header {
		names = append(names, n)
	}
	sort.Strings(names) // deterministic output, easier to test
	for _, name := range names {
		values := f.Header[name]
		upper := strings.ToUpper(name)
		switch upper {
		case "X-GM-THRID", "X-GM-MSGID":
			if g := orGroup(upper, values, false); g != "" {
				parts = append(parts, g)
			}
		default:
			terms := make([]string, len(values))
			for i, v := range values {
				terms[i] = fmt.Sprintf("HEADER %s %s", quote(name), quote(v))
			}
			if g := nestOr(terms); g != "" {
				parts = append(parts, g)
			}
		}
	}

	if f.Since != "" {
		d, err := parseDate(f.Since)
		if err != nil {
			return "", fmt.Errorf("--since: %w", err)
		}
		parts = append(parts, "SINCE "+d)
	}
	if f.Before != "" {
		d, err := parseDate(f.Before)
		if err != nil {
			return "", fmt.Errorf("--before: %w", err)
		}
		parts = append(parts, "BEFORE "+d)
	}

	if len(parts) == 0 {
		return "ALL", nil
	}
	return strings.Join(parts, " "), nil
}

func parseDate(s string) (string, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return "", err
	}
	return t.Format("02-Jan-2006"), nil
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// orGroup builds `KEY "v1"` or, for multiple values, a nested OR of
// `KEY "vN"` terms (OR = repeated same filter, per docs §4.1).
func orGroup(key string, values []string, quoted bool) string {
	if len(values) == 0 {
		return ""
	}
	terms := make([]string, len(values))
	for i, v := range values {
		if quoted {
			terms[i] = fmt.Sprintf("%s %s", key, quote(v))
		} else {
			terms[i] = fmt.Sprintf("%s %s", key, v)
		}
	}
	return nestOr(terms)
}

// nestOr combines search-key terms with IMAP's binary OR key:
// OR t1 t2, or OR t1 (OR t2 (OR t3 t4)) for more than two.
func nestOr(terms []string) string {
	if len(terms) == 0 {
		return ""
	}
	if len(terms) == 1 {
		return terms[0]
	}
	rest := nestOr(terms[1:])
	if len(terms) > 2 {
		rest = "(" + rest + ")"
	}
	return "OR " + terms[0] + " " + rest
}
