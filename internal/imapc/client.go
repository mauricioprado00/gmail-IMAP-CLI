package imapc

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Client wraps Conn with higher-level Gmail-IMAP operations.
type Client struct {
	*Conn
}

// Connect dials, reads the greeting and logs in.
func Connect(host string, port int, user, pass string, debug bool) (*Client, error) {
	c, err := Dial(host, port)
	if err != nil {
		return nil, err
	}
	c.Debug = debug
	if err := c.Login(user, pass); err != nil {
		c.Close()
		return nil, err
	}
	return &Client{c}, nil
}

// Mailbox describes one entry from LIST.
type Mailbox struct {
	Name  string
	Flags []string
}

// List returns all mailboxes/labels.
func (c *Client) List() ([]Mailbox, error) {
	resp, err := c.Cmd(`LIST "" "*"`)
	if err != nil {
		return nil, err
	}
	var out []Mailbox
	for _, line := range resp.Untagged {
		if !strings.HasPrefix(line.Text, "LIST ") {
			continue
		}
		rest := strings.TrimPrefix(line.Text, "LIST ")
		lits := line.Literals
		p := findParen(rest, 0)
		if p == -1 {
			continue
		}
		flagsList, next := parseParenList(rest, p, &lits)
		remainder := strings.TrimSpace(rest[next:])
		// remainder: <delimiter> <mailbox-name>
		parts := splitTop(remainder, &lits)
		if len(parts) < 2 {
			continue
		}
		name := parts[len(parts)-1]
		out = append(out, Mailbox{Name: name, Flags: listToStrings(flagsList)})
	}
	return out, nil
}

// splitTop splits a string like `"/" "Some Name"` (or with a literal) into
// its top-level tokens (quoted strings/atoms), resolving literal
// sentinels against lits.
func splitTop(s string, lits *[][]byte) []string {
	var out []string
	pos := 0
	for pos < len(s) {
		for pos < len(s) && s[pos] == ' ' {
			pos++
		}
		if pos >= len(s) {
			break
		}
		v, next := parseValue(s, pos, lits)
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
		pos = next
	}
	return out
}

// MailboxStatus is the result of SELECT.
type MailboxStatus struct {
	Exists int
}

// Select opens a mailbox read-write.
func (c *Client) Select(mailbox string) (*MailboxStatus, error) {
	resp, err := c.Cmd(fmt.Sprintf("SELECT %s", quote(mailbox)))
	if err != nil {
		return nil, err
	}
	st := &MailboxStatus{}
	for _, line := range resp.Untagged {
		fields := strings.Fields(line.Text)
		if len(fields) == 2 && fields[1] == "EXISTS" {
			if n, err := strconv.Atoi(fields[0]); err == nil {
				st.Exists = n
			}
		}
	}
	return st, nil
}

// Create creates a mailbox/label. Returns nil if it already exists.
func (c *Client) Create(mailbox string) error {
	_, err := c.Cmd(fmt.Sprintf("CREATE %s", quote(mailbox)))
	if err != nil && strings.Contains(strings.ToUpper(err.Error()), "ALREADY EXISTS") {
		return nil
	}
	return err
}

// UIDSearch runs `UID SEARCH <criteria>` (criteria is raw IMAP search
// syntax, already built by the search package) and returns matching UIDs.
func (c *Client) UIDSearch(criteria string) ([]uint32, error) {
	resp, err := c.Cmd("UID SEARCH " + criteria)
	if err != nil {
		return nil, err
	}
	var uids []uint32
	for _, line := range resp.Untagged {
		if !strings.HasPrefix(line.Text, "SEARCH") {
			continue
		}
		for _, f := range strings.Fields(strings.TrimPrefix(line.Text, "SEARCH")) {
			if n, err := strconv.ParseUint(f, 10, 32); err == nil {
				uids = append(uids, uint32(n))
			}
		}
	}
	return uids, nil
}

// FetchResult holds parsed FETCH data for one message.
type FetchResult struct {
	UID     uint32
	Flags   []string
	Labels  []string
	Size    int
	Headers []byte // raw header bytes (light/standard modes)
	Text    []byte // raw BODY[TEXT] bytes (standard mode)
	Raw     []byte // full raw RFC822 message (full mode)
}

// UIDFetch fetches `items` for the given UIDs. items is a raw IMAP fetch
// data-item list, e.g. "UID FLAGS X-GM-LABELS RFC822.SIZE
// BODY.PEEK[HEADER.FIELDS (FROM TO SUBJECT DATE MESSAGE-ID)]".
func (c *Client) UIDFetch(uids []uint32, items string) ([]FetchResult, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	seq := joinUint32(uids, ",")
	resp, err := c.Cmd(fmt.Sprintf("UID FETCH %s (%s)", seq, items))
	if err != nil {
		return nil, err
	}
	var out []FetchResult
	for _, line := range resp.Untagged {
		fields := strings.Fields(line.Text)
		if len(fields) < 2 || fields[1] != "FETCH" {
			continue
		}
		lits := line.Literals
		p := findParen(line.Text, 0)
		if p == -1 {
			continue
		}
		list, _ := parseParenList(line.Text, p, &lits)
		m := asMap(list)
		fr := FetchResult{}
		if v, ok := m["UID"].(string); ok {
			if n, err := strconv.ParseUint(v, 10, 32); err == nil {
				fr.UID = uint32(n)
			}
		}
		if v, ok := m["FLAGS"].([]interface{}); ok {
			fr.Flags = listToStrings(v)
		}
		if v, ok := m["X-GM-LABELS"].([]interface{}); ok {
			fr.Labels = listToStrings(v)
		}
		if v, ok := m["RFC822.SIZE"].(string); ok {
			if n, err := strconv.Atoi(v); err == nil {
				fr.Size = n
			}
		}
		for k, v := range m {
			if !strings.HasPrefix(k, "BODY[") {
				continue
			}
			s, _ := v.(string)
			switch {
			case strings.HasPrefix(k, "BODY[HEADER"):
				fr.Headers = []byte(s)
			case k == "BODY[TEXT]":
				fr.Text = []byte(s)
			case k == "BODY[]":
				fr.Raw = []byte(s)
			}
		}
		out = append(out, fr)
	}
	return out, nil
}

// Append uploads a raw RFC822 message into mailbox, preserving flags and
// internal date.
func (c *Client) Append(mailbox string, flags []string, date time.Time, raw []byte) error {
	prefix := fmt.Sprintf("APPEND %s", quote(mailbox))
	if len(flags) > 0 {
		prefix += " (" + strings.Join(flags, " ") + ")"
	}
	prefix += " " + quote(date.Format("02-Jan-2006 15:04:05 -0700"))
	_, err := c.CmdLiteral(prefix, raw)
	return err
}

// StoreLabels adds or removes Gmail labels on the given UIDs via
// UID STORE ... X-GM-LABELS.
func (c *Client) StoreLabels(uids []uint32, add bool, labels []string) error {
	if len(uids) == 0 || len(labels) == 0 {
		return nil
	}
	op := "+X-GM-LABELS"
	if !add {
		op = "-X-GM-LABELS"
	}
	quoted := make([]string, len(labels))
	for i, l := range labels {
		quoted[i] = quote(l)
	}
	seq := joinUint32(uids, ",")
	_, err := c.Cmd(fmt.Sprintf("UID STORE %s %s (%s)", seq, op, strings.Join(quoted, " ")))
	return err
}

func joinUint32(nums []uint32, sep string) string {
	parts := make([]string, len(nums))
	for i, n := range nums {
		parts[i] = strconv.FormatUint(uint64(n), 10)
	}
	return strings.Join(parts, sep)
}
