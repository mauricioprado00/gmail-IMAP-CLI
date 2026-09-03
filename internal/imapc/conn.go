// Package imapc is a minimal hand-written IMAP4rev1 client sufficient for
// gic's needs: LOGIN, LIST, SELECT, CREATE, UID SEARCH (incl. Gmail
// X-GM-LABELS/X-GM-THRID/X-GM-RAW extensions), UID FETCH, UID STORE
// (label and system-flag add/remove), and APPEND. It intentionally avoids
// third-party IMAP libraries so Gmail-specific extensions are easy to send
// as raw command text.
package imapc

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var osStderr io.Writer = os.Stderr

// Conn is a low-level tagged-command IMAP connection.
type Conn struct {
	conn   *tls.Conn
	r      *bufio.Reader
	w      io.Writer
	tagNum int
	Debug  bool // when true, prints protocol trace to stderr
}

var literalRe = regexp.MustCompile(`\{(\d+)\+?\}\r?\n$`)

// Dial connects and reads the server greeting.
func Dial(host string, port int) (*Conn, error) {
	tlsConn, err := tls.Dial("tcp", fmt.Sprintf("%s:%d", host, port), &tls.Config{ServerName: host})
	if err != nil {
		return nil, err
	}
	c := &Conn{conn: tlsConn, r: bufio.NewReaderSize(tlsConn, 64*1024), w: tlsConn}
	if _, _, err := c.readResponseLine(); err != nil { // greeting
		return nil, err
	}
	return c, nil
}

func (c *Conn) Close() error { return c.conn.Close() }

func (c *Conn) nextTag() string {
	c.tagNum++
	return fmt.Sprintf("a%03d", c.tagNum)
}

// readResponseLine reads one logical server line, transparently absorbing
// any IMAP literals ({n}) it contains. Each literal's raw bytes are
// returned in order in `literals`; the returned `line` has each literal's
// "{n}\r\n" marker replaced with a single 0x00 sentinel byte so a parser
// can locate literal positions without re-parsing the byte count.
func (c *Conn) readResponseLine() (line string, literals [][]byte, err error) {
	var sb strings.Builder
	for {
		raw, err := c.r.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		if m := literalRe.FindStringSubmatch(raw); m != nil {
			n, _ := strconv.Atoi(m[1])
			buf := make([]byte, n)
			if _, err := io.ReadFull(c.r, buf); err != nil {
				return "", nil, err
			}
			literals = append(literals, buf)
			prefix := raw[:len(raw)-len(m[0])]
			sb.WriteString(prefix)
			sb.WriteByte(0)
			continue // the bytes right after a literal continue the same logical line
		}
		sb.WriteString(strings.TrimRight(raw, "\r\n"))
		break
	}
	if c.Debug {
		fmt.Fprintf(dbgOut, "S: %s\n", sb.String())
	}
	return sb.String(), literals, nil
}

var dbgOut io.Writer = osStderr

// Response is the parsed result of a tagged command: all untagged lines
// (with their literals) plus the final status word (OK/NO/BAD) and text.
type Response struct {
	Untagged []Line
	Status   string // OK, NO, BAD
	Text     string
}

// Line is one untagged (`* ...`) server line plus any literal payloads it
// carried, in order of appearance.
type Line struct {
	Text     string
	Literals [][]byte
}

// Cmd sends "<tag> <command>" and collects the response.
func (c *Conn) Cmd(command string) (*Response, error) {
	tag := c.nextTag()
	if c.Debug {
		fmt.Fprintf(dbgOut, "C: %s %s\n", tag, command)
	}
	if _, err := fmt.Fprintf(c.w, "%s %s\r\n", tag, command); err != nil {
		return nil, err
	}
	resp := &Response{}
	for {
		line, lits, err := c.readResponseLine()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "* ") {
			resp.Untagged = append(resp.Untagged, Line{Text: line[2:], Literals: lits})
			continue
		}
		if strings.HasPrefix(line, tag+" ") {
			rest := strings.TrimPrefix(line, tag+" ")
			parts := strings.SplitN(rest, " ", 2)
			resp.Status = parts[0]
			if len(parts) > 1 {
				resp.Text = parts[1]
			}
			if resp.Status != "OK" {
				return resp, fmt.Errorf("IMAP command %q failed: %s %s", command, resp.Status, resp.Text)
			}
			return resp, nil
		}
		// continuation ("+ ...") or other unexpected line: ignore.
	}
}

// CmdLiteral sends a command whose single argument is a literal (used for
// APPEND, where the message body may contain characters unsafe to quote).
// argPrefix/argSuffix bracket the literal in the command line, e.g.
// APPEND `"INBOX" (\Seen) {n}` followed by the raw bytes (the suffix is empty;
// the client closes the command with CRLF after the literal bytes).
func (c *Conn) CmdLiteral(cmdPrefix string, literal []byte) (*Response, error) {
	tag := c.nextTag()
	header := fmt.Sprintf("%s %s {%d}\r\n", tag, cmdPrefix, len(literal))
	if c.Debug {
		fmt.Fprintf(dbgOut, "C: %s (+ %d byte literal)\n", strings.TrimRight(header, "\r\n"), len(literal))
	}
	if _, err := io.WriteString(c.w, header); err != nil {
		return nil, err
	}
	// Server must send a "+ go ahead" continuation before we write the
	// literal bytes.
	cont, _, err := c.readResponseLine()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(cont, "+") {
		return nil, fmt.Errorf("server did not send continuation for literal: %s", cont)
	}
	if _, err := c.w.Write(literal); err != nil {
		return nil, err
	}
	if _, err := io.WriteString(c.w, "\r\n"); err != nil {
		return nil, err
	}
	resp := &Response{}
	for {
		line, lits, err := c.readResponseLine()
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(line, "* ") {
			resp.Untagged = append(resp.Untagged, Line{Text: line[2:], Literals: lits})
			continue
		}
		if strings.HasPrefix(line, tag+" ") {
			rest := strings.TrimPrefix(line, tag+" ")
			parts := strings.SplitN(rest, " ", 2)
			resp.Status = parts[0]
			if len(parts) > 1 {
				resp.Text = parts[1]
			}
			if resp.Status != "OK" {
				return resp, fmt.Errorf("IMAP APPEND failed: %s %s", resp.Status, resp.Text)
			}
			return resp, nil
		}
	}
}

// Login authenticates. The app password is sent as an IMAP quoted string,
// so backslashes/quotes in it are escaped.
func (c *Conn) Login(user, pass string) error {
	_, err := c.Cmd(fmt.Sprintf("LOGIN %s %s", quote(user), quote(pass)))
	return err
}

func (c *Conn) Logout() {
	_, _ = c.Cmd("LOGOUT")
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
