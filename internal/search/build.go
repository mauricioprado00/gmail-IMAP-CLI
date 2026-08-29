// Package search builds Gmail IMAP SEARCH criteria strings from gic's
// filter flags, implementing the AND (across filter types) / OR (repeated
// same filter) combination rule documented in docs/COMMANDS.md §4.1.
package search

import (
	"fmt"
	"sort"
	"strconv"
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
	Since  string // "2006-01-02" or a signed day offset such as "+5days"
	Until  string // exclusive upper bound; same formats as Since
	Before string // deprecated alias for Until
	// Seen restricts by read state: nil = no restriction, true = only
	// read (SEEN) messages, false = only unread (UNSEEN) messages.
	Seen *bool
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

	now := time.Now()
	if f.Since != "" {
		d, err := parseDate(f.Since, now)
		if err != nil {
			return "", fmt.Errorf("--since: %w", err)
		}
		parts = append(parts, "SINCE "+d)
	}
	until := f.Until
	untilFlag := "--until"
	if until == "" {
		until = f.Before
		untilFlag = "--before"
	}
	if until != "" {
		d, err := parseDate(until, now)
		if err != nil {
			return "", fmt.Errorf("%s: %w", untilFlag, err)
		}
		parts = append(parts, "BEFORE "+d)
	}

	if f.Seen != nil {
		if *f.Seen {
			parts = append(parts, "SEEN")
		} else {
			parts = append(parts, "UNSEEN")
		}
	}

	if len(parts) == 0 {
		return "ALL", nil
	}
	return strings.Join(parts, " "), nil
}

func parseDate(s string, now time.Time) (string, error) {
	t, err := time.Parse("2006-01-02", s)
	if err == nil {
		return t.Format("02-Jan-2006"), nil
	}

	var years, months, days int
	terms := strings.Fields(strings.ToLower(s))
	if len(terms) == 0 {
		return "", invalidDateError(s)
	}
	for _, term := range terms {
		numberEnd := 1
		if len(term) < 3 || (term[0] != '+' && term[0] != '-') {
			return "", invalidDateError(s)
		}
		for numberEnd < len(term) && term[numberEnd] >= '0' && term[numberEnd] <= '9' {
			numberEnd++
		}
		if numberEnd == 1 {
			return "", invalidDateError(s)
		}
		amount, err := strconv.Atoi(term[:numberEnd])
		if err != nil {
			return "", fmt.Errorf("invalid date offset %q: %w", term, err)
		}
		switch term[numberEnd:] {
		case "day", "days":
			days += amount
		case "week", "weeks":
			days += amount * 7
		case "month", "months":
			months += amount
		case "year", "years":
			years += amount
		default:
			return "", invalidDateError(s)
		}
	}
	t = now.AddDate(years, months, days)
	return t.Format("02-Jan-2006"), nil
}

func invalidDateError(s string) error {
	return fmt.Errorf(
		"invalid date %q (want YYYY-MM-DD or signed day/week/month/year offsets such as +5days or -1month -1week)",
		s,
	)
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
