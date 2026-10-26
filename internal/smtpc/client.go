// Package smtpc sends mail over SMTP with an account's App Password. Sessions
// are short-lived and connect directly to the server; they never go through
// the IMAP connection-pooling daemon (docs/COMMANDS.md §10).
package smtpc

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Envelope is what the SMTP transaction needs beyond the message bytes.
type Envelope struct {
	From       string   // envelope sender (MAIL FROM)
	Recipients []string // every RCPT TO, including Bcc
}

// Dial opens a connection to host:port. Port 465 is implicit TLS; any other
// port is plain TCP that the session upgrades with STARTTLS before AUTH.
func Dial(host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := &net.Dialer{Timeout: 30 * time.Second}
	if port == 465 {
		return tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	}
	return dialer.Dial("tcp", addr)
}

// Send authenticates and submits one message. Verbose sessions trace the
// dialogue to stderr with the AUTH line redacted.
func Send(host string, port int, user, pass string, env Envelope, msg []byte, verbose bool) error {
	conn, err := Dial(host, port)
	if err != nil {
		return fmt.Errorf("connecting to %s:%d: %w", host, port, err)
	}
	defer conn.Close()
	return SendOn(conn, host, user, pass, env, msg, verbose)
}

// Verify logs in and quits without sending, so `account add` can confirm the
// App Password works for SMTP as well as IMAP.
func Verify(host string, port int, user, pass string) error {
	conn, err := Dial(host, port)
	if err != nil {
		return fmt.Errorf("connecting to %s:%d: %w", host, port, err)
	}
	defer conn.Close()
	c, err := login(conn, host, user, pass, false)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}

// SendOn runs the SMTP transaction over an already-open connection.
func SendOn(conn net.Conn, host, user, pass string, env Envelope, msg []byte, verbose bool) error {
	c, err := login(conn, host, user, pass, verbose)
	if err != nil {
		return err
	}
	defer c.Close()

	if err := c.Mail(env.From); err != nil {
		return fmt.Errorf("MAIL FROM: %w", err)
	}
	for _, rcpt := range env.Recipients {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("RCPT TO %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("sending message body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("server rejected message: %w", err)
	}
	return c.Quit()
}

// login greets the server, upgrades a cleartext connection with STARTTLS when
// the server offers it, and authenticates with PLAIN when the server
// advertises AUTH (a test server may not). net/smtp itself refuses PLAIN over
// cleartext to any host other than localhost.
func login(conn net.Conn, host, user, pass string, verbose bool) (*smtp.Client, error) {
	if verbose {
		conn = &traceConn{Conn: conn, w: os.Stderr}
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return nil, fmt.Errorf("SMTP greeting: %w", err)
	}
	if err := c.Hello(localName()); err != nil {
		c.Close()
		return nil, fmt.Errorf("EHLO: %w", err)
	}
	if _, isTLS := conn.(*tls.Conn); !isTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
				c.Close()
				return nil, fmt.Errorf("STARTTLS: %w", err)
			}
		}
	}
	if ok, _ := c.Extension("AUTH"); ok {
		if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
			c.Close()
			return nil, fmt.Errorf("SMTP login for %s failed: %w", user, err)
		}
	}
	return c, nil
}

func localName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "localhost"
}

// traceConn echoes the dialogue to w, prefixing client lines with "C: " and
// server lines with "S: ". The AUTH PLAIN argument (base64 credentials) is
// redacted, and the message body between DATA and the terminating "." is
// summarized as a line count rather than echoed.
type traceConn struct {
	net.Conn
	w         io.Writer
	rbuf      []byte
	wbuf      []byte
	inData    bool
	bodyLines int
}

func (t *traceConn) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	t.rbuf = t.emit("S: ", append(t.rbuf, p[:n]...), false)
	return n, err
}

func (t *traceConn) Write(p []byte) (int, error) {
	n, err := t.Conn.Write(p)
	t.wbuf = t.emit("C: ", append(t.wbuf, p[:n]...), true)
	return n, err
}

// emit prints every complete line in buf and returns the unterminated tail.
func (t *traceConn) emit(prefix string, buf []byte, client bool) []byte {
	for {
		i := strings.IndexByte(string(buf), '\n')
		if i < 0 {
			return buf
		}
		line := strings.TrimRight(string(buf[:i]), "\r")
		buf = buf[i+1:]
		if client {
			if t.inData {
				if line != "." {
					t.bodyLines++
					continue
				}
				fmt.Fprintf(t.w, "%s<%d body lines>\n", prefix, t.bodyLines)
				t.inData, t.bodyLines = false, 0
			} else if strings.EqualFold(line, "DATA") {
				t.inData = true
			} else if strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN ") {
				line = line[:len("AUTH PLAIN ")] + "<redacted>"
			}
		} else if t.inData && !strings.HasPrefix(line, "354") {
			t.inData = false // DATA refused: no body follows
		}
		fmt.Fprintln(t.w, prefix+line)
	}
}
