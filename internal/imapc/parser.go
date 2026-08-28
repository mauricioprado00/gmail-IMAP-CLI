package imapc

import "strings"

// parseParenList parses one IMAP "list" starting at s[pos]=='(' and returns
// the parsed elements plus the position just after the matching ')'.
// Elements are either string (atom/quoted-string/literal-sentinel resolved
// value) or []interface{} (nested list). NIL parses to nil.
func parseParenList(s string, pos int, literals *[][]byte) ([]interface{}, int) {
	// assumes s[pos] == '('
	pos++
	var out []interface{}
	for pos < len(s) {
		for pos < len(s) && s[pos] == ' ' {
			pos++
		}
		if pos >= len(s) {
			break
		}
		if s[pos] == ')' {
			pos++
			break
		}
		var v interface{}
		v, pos = parseValue(s, pos, literals)
		out = append(out, v)
	}
	return out, pos
}

// parseValue parses one atom, quoted string, literal-sentinel, or nested
// list at s[pos], returning the value and the position right after it.
func parseValue(s string, pos int, literals *[][]byte) (interface{}, int) {
	if pos >= len(s) {
		return nil, pos
	}
	switch s[pos] {
	case '(':
		return parseParenList(s, pos, literals)
	case '"':
		var sb strings.Builder
		pos++
		for pos < len(s) && s[pos] != '"' {
			if s[pos] == '\\' && pos+1 < len(s) {
				pos++
			}
			sb.WriteByte(s[pos])
			pos++
		}
		pos++ // closing quote
		return sb.String(), pos
	case 0: // literal sentinel written by readResponseLine
		var lit []byte
		if literals != nil && len(*literals) > 0 {
			lit = (*literals)[0]
			*literals = (*literals)[1:]
		}
		pos++
		return string(lit), pos
	default:
		start := pos
		for pos < len(s) {
			c := s[pos]
			if c == '[' {
				// FETCH section specifiers like BODY[HEADER.FIELDS (FROM
				// SUBJECT)] can contain parens that aren't nested IMAP
				// lists; consume the whole bracketed section atomically.
				depth := 1
				pos++
				for pos < len(s) && depth > 0 {
					switch s[pos] {
					case '[':
						depth++
					case ']':
						depth--
					}
					pos++
				}
				continue
			}
			if c == ' ' || c == '(' || c == ')' || c == 0 {
				break
			}
			pos++
		}
		atom := s[start:pos]
		if atom == "NIL" {
			return nil, pos
		}
		return atom, pos
	}
}

// findParen returns the index of the first '(' in s at or after `from`,
// or -1 if none.
func findParen(s string, from int) int {
	idx := strings.IndexByte(s[from:], '(')
	if idx == -1 {
		return -1
	}
	return from + idx
}

// listToStrings converts a parsed list of atoms into []string (nested
// lists are skipped).
func listToStrings(l []interface{}) []string {
	out := make([]string, 0, len(l))
	for _, v := range l {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// asMap converts a flat (key value key value ...) parsed list, as used by
// FETCH responses, into a map keyed by the uppercased atom name.
func asMap(l []interface{}) map[string]interface{} {
	m := map[string]interface{}{}
	for i := 0; i+1 < len(l); i += 2 {
		k, ok := l[i].(string)
		if !ok {
			continue
		}
		m[strings.ToUpper(k)] = l[i+1]
	}
	return m
}
